package jobs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// ---- One-tap outcome token -------------------------------------------------
//
// The SolvedPrompt email links carry a signed token that authenticates the
// poster for exactly one action: recording their outcome for one solution to
// one problem. It lives here (not in service) because the worker makes it and
// jobs must not import service; the service verifies it with
// ParseOutcomeToken.

const (
	// OutcomeTokenPurpose is the config.SigningKey purpose for the token.
	OutcomeTokenPurpose = "solved_prompt"
	// OutcomeTokenTTL is how long an emailed link stays valid.
	OutcomeTokenTTL = 14 * 24 * time.Hour

	outcomeTokenVersion = 1
	outcomePayloadLen   = 1 + 16 + 16 + 16 + 8 // version, problem, solution, poster, expiry
	outcomeTokenLen     = outcomePayloadLen + sha256.Size
)

var (
	// ErrOutcomeTokenInvalid: malformed, tampered with or signed with another key.
	ErrOutcomeTokenInvalid = errors.New("outcome token invalid")
	// ErrOutcomeTokenExpired: well-formed and correctly signed, but past its expiry.
	ErrOutcomeTokenExpired = errors.New("outcome token expired")
)

// OutcomeClaims is what an outcome token asserts.
type OutcomeClaims struct {
	ProblemID  uuid.UUID
	SolutionID uuid.UUID
	PosterID   uuid.UUID
	Expires    time.Time // second precision
}

// MakeOutcomeToken signs c with key (config.SigningKey(OutcomeTokenPurpose))
// and returns it base64url-encoded (no padding; safe in a URL path).
func MakeOutcomeToken(key []byte, c OutcomeClaims) string {
	b := make([]byte, outcomePayloadLen, outcomeTokenLen)
	b[0] = outcomeTokenVersion
	copy(b[1:17], c.ProblemID[:])
	copy(b[17:33], c.SolutionID[:])
	copy(b[33:49], c.PosterID[:])
	binary.BigEndian.PutUint64(b[49:57], uint64(c.Expires.Unix()))
	m := hmac.New(sha256.New, key)
	m.Write(b)
	return base64.RawURLEncoding.EncodeToString(m.Sum(b))
}

// ParseOutcomeToken verifies token's signature (constant time) and expiry
// against now. A bad signature is ErrOutcomeTokenInvalid even when the token
// has also expired.
func ParseOutcomeToken(key []byte, token string, now time.Time) (OutcomeClaims, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != outcomeTokenLen || b[0] != outcomeTokenVersion {
		return OutcomeClaims{}, ErrOutcomeTokenInvalid
	}
	m := hmac.New(sha256.New, key)
	m.Write(b[:outcomePayloadLen])
	if !hmac.Equal(m.Sum(nil), b[outcomePayloadLen:]) {
		return OutcomeClaims{}, ErrOutcomeTokenInvalid
	}
	var c OutcomeClaims
	copy(c.ProblemID[:], b[1:17])
	copy(c.SolutionID[:], b[17:33])
	copy(c.PosterID[:], b[33:49])
	exp := int64(binary.BigEndian.Uint64(b[49:57]))
	c.Expires = time.Unix(exp, 0).UTC()
	if now.Unix() >= exp {
		return c, ErrOutcomeTokenExpired
	}
	return c, nil
}

// ---- Worker ------------------------------------------------------------------

// SolvedPromptWorker emails the poster when their problem's top solution
// first crosses SOFT_SOLVED_THRESHOLD, asking whether it worked.
type SolvedPromptWorker struct {
	river.WorkerDefaults[SolvedPromptArgs]
	d Deps
}

func (w *SolvedPromptWorker) Work(ctx context.Context, job *river.Job[SolvedPromptArgs]) error {
	return SendSolvedPrompt(ctx, w.d, job.Args)
}

// SendSolvedPrompt sends the one email, or skips (returning nil) when the
// poster has no usable email, is deleted or suspended, the problem is no
// longer open and soft-solved, the poster already recorded an outcome for
// this solution, or POSTMARK_TOKEN is empty.
//
// Delivery is at-least-once: River's ByArgs uniqueness stops duplicate
// enqueues, and the send is the last step, so a successful send always
// completes the job. Only a crash between Postmark accepting the message and
// River recording completion can repeat it. The recipient address is never
// logged.
func SendSolvedPrompt(ctx context.Context, d Deps, args SolvedPromptArgs) error {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	log = log.With("problem_id", args.ProblemID, "solution_id", args.SolutionID)
	if d.Cfg.PostmarkToken == "" {
		log.InfoContext(ctx, "solved prompt skipped: postmark disabled")
		return nil
	}
	row, err := d.Store.GetSolvedPromptContext(ctx, store.GetSolvedPromptContextParams{
		ProblemID: args.ProblemID, SolutionID: args.SolutionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		log.InfoContext(ctx, "solved prompt skipped: problem or solution gone")
		return nil
	}
	if err != nil {
		return err
	}
	if reason := solvedPromptSkip(row); reason != "" {
		log.InfoContext(ctx, "solved prompt skipped: "+reason)
		return nil
	}
	from, err := senderAddress(d.Cfg.BaseURL)
	if err != nil {
		return err
	}
	token := MakeOutcomeToken(d.Cfg.SigningKey(OutcomeTokenPurpose), OutcomeClaims{
		ProblemID: row.ProblemID, SolutionID: row.SolutionID, PosterID: row.AuthorID,
		Expires: nowFn(d)().Add(OutcomeTokenTTL),
	})
	msg := solvedPromptEmail(d.Cfg.BaseURL, token, row)
	msg.From, msg.To = from, strings.TrimSpace(*row.Email)

	pm := &PostmarkClient{Token: d.Cfg.PostmarkToken}
	if err := pm.Send(ctx, msg); err != nil {
		var pe *PostmarkError
		if errors.As(err, &pe) && pe.Permanent() {
			// Retrying won't help (e.g. inactive recipient); don't burn attempts.
			log.WarnContext(ctx, "solved prompt refused by postmark", "status", pe.Status, "error_code", pe.ErrorCode)
			return nil
		}
		return err
	}
	log.InfoContext(ctx, "solved prompt sent")
	return nil
}

// solvedPromptSkip returns why no email should go out, or "".
func solvedPromptSkip(r store.GetSolvedPromptContextRow) string {
	switch {
	case r.Email == nil || strings.TrimSpace(*r.Email) == "":
		return "poster has no email"
	case r.AuthorDeleted:
		return "poster deleted"
	case r.AuthorSuspended:
		return "poster suspended"
	case r.State != store.ProblemStateOpen:
		return "problem not open"
	case !r.SoftSolved:
		return "problem no longer soft-solved"
	case r.PosterOutcome != "":
		return "poster already recorded an outcome"
	}
	return ""
}

// senderAddress is no-reply@<host of BASE_URL>.
func senderAddress(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("solved prompt: BASE_URL %q has no host", baseURL)
	}
	return "no-reply@" + u.Hostname(), nil
}

// OutcomeLink is the one-tap link for outcome ("worked", "partly", "failed").
func OutcomeLink(baseURL, token, outcome string) string {
	return baseURL + "/solved/" + token + "?outcome=" + outcome
}

var promptOutcomes = []struct{ value, label string }{
	{"worked", "It worked"},
	{"partly", "It partly worked"},
	{"failed", "It didn't work"},
}

// solvedPromptEmail builds subject and bodies; From and To are set by the caller.
func solvedPromptEmail(baseURL, token string, r store.GetSolvedPromptContextRow) PostmarkEmail {
	problemURL := baseURL + "/p/" + r.ProblemID.String()
	kind := solutionKindLabel(r.Kind)
	body := excerpt(r.Body, 280)
	intro := `A solution to your problem "` + r.Title + `" has enough votes to count as solved. Did it work for you?`

	var t strings.Builder
	t.WriteString(intro + "\n\n")
	t.WriteString("The solution (" + kind + "):\n" + body + "\n\n")
	for _, o := range promptOutcomes {
		t.WriteString(o.label + ": " + OutcomeLink(baseURL, token, o.value) + "\n")
	}
	t.WriteString("\nEach link asks you to confirm before anything is recorded. The links expire in 14 days.\n")
	t.WriteString("The problem: " + problemURL + "\n\n")
	t.WriteString("You're getting this because you posted this problem on Unsolved and added your email in settings: " + baseURL + "/settings\n")

	var h strings.Builder
	h.WriteString(`<!DOCTYPE html><html><body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;color:#1c1917;line-height:1.5">`)
	h.WriteString("<p>" + html.EscapeString(intro) + "</p>")
	h.WriteString(`<p style="margin:16px 0 4px;color:#57534e;font-size:14px">The solution (` + html.EscapeString(kind) + `):</p>`)
	h.WriteString(`<blockquote style="margin:0;padding:8px 12px;border-left:3px solid #d6d3d1;color:#44403c">` + html.EscapeString(body) + "</blockquote>")
	h.WriteString(`<p style="margin:24px 0">`)
	for _, o := range promptOutcomes {
		h.WriteString(`<a href="` + html.EscapeString(OutcomeLink(baseURL, token, o.value)) +
			`" style="display:inline-block;margin:0 8px 8px 0;padding:10px 16px;border-radius:6px;background:#1c1917;color:#ffffff;text-decoration:none">` +
			html.EscapeString(o.label) + "</a>")
	}
	h.WriteString("</p>")
	h.WriteString(`<p style="color:#57534e;font-size:14px">Each link asks you to confirm before anything is recorded. The links expire in 14 days. <a href="` +
		html.EscapeString(problemURL) + `">See the problem</a>.</p>`)
	h.WriteString(`<p style="color:#78716c;font-size:12px">You're getting this because you posted this problem on Unsolved and added your email in <a href="` +
		html.EscapeString(baseURL+"/settings") + `">settings</a>.</p>`)
	h.WriteString("</body></html>")

	return PostmarkEmail{
		Subject:       `Did it work? A solution to "` + r.Title + `" counts as solved`,
		TextBody:      t.String(),
		HtmlBody:      h.String(),
		MessageStream: "outbound",
	}
}

// solutionKindLabel mirrors views/partials.SolutionKindLabel (jobs can't
// import views).
func solutionKindLabel(k store.SolutionKind) string {
	switch k {
	case store.SolutionKindProcessChange:
		return "Change the process"
	case store.SolutionKindOffTheShelf:
		return "Use an existing tool"
	case store.SolutionKindCustomSoftware:
		return "Build something custom"
	case store.SolutionKindDontAutomate:
		return "Don't automate this"
	}
	return string(k)
}

// excerpt collapses whitespace and cuts s to at most n runes, adding "…".
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
