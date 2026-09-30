package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

const defaultPostmarkBaseURL = "https://api.postmarkapp.com"

// postmarkBaseURL overrides the Postmark API root. Tests point it at an
// httptest server with SetPostmarkBaseURL; production never sets it.
var postmarkBaseURL atomic.Pointer[string]

// SetPostmarkBaseURL points every Postmark send made afterwards at u and
// returns a func that restores the previous value. For tests only.
func SetPostmarkBaseURL(u string) (restore func()) {
	prev := postmarkBaseURL.Swap(&u)
	return func() { postmarkBaseURL.Store(prev) }
}

func currentPostmarkBaseURL() string {
	if p := postmarkBaseURL.Load(); p != nil {
		return *p
	}
	return defaultPostmarkBaseURL
}

// postmarkHTTP is shared by every PostmarkClient that doesn't bring its own.
var postmarkHTTP = &http.Client{Timeout: 15 * time.Second}

// PostmarkEmail is the body of POST /email, with Postmark's field names.
type PostmarkEmail struct {
	From          string `json:"From"`
	To            string `json:"To"`
	Subject       string `json:"Subject"`
	TextBody      string `json:"TextBody"`
	HtmlBody      string `json:"HtmlBody"`
	MessageStream string `json:"MessageStream"`
}

// PostmarkError is a non-200 answer from Postmark. Error() leaves Message
// out: Postmark quotes recipient addresses in it, and errors reach the logs.
type PostmarkError struct {
	Status    int    `json:"-"`
	ErrorCode int    `json:"ErrorCode"`
	Message   string `json:"Message"`
}

func (e *PostmarkError) Error() string {
	return fmt.Sprintf("postmark: status %d, error code %d", e.Status, e.ErrorCode)
}

// Permanent reports whether retrying cannot help: Postmark answers 422 when
// it refuses the message itself (inactive recipient, unverified sender, ...).
func (e *PostmarkError) Permanent() bool { return e.Status == http.StatusUnprocessableEntity }

// PostmarkClient sends transactional email through the Postmark HTTP API.
type PostmarkClient struct {
	Token   string       // POSTMARK_TOKEN (a server token)
	BaseURL string       // empty = the package default
	HTTP    *http.Client // nil = shared default
}

// Send posts one email. A non-200 answer is returned as a *PostmarkError.
func (c *PostmarkClient) Send(ctx context.Context, e PostmarkEmail) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	base := c.BaseURL
	if base == "" {
		base = currentPostmarkBaseURL()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/email", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Postmark-Server-Token", c.Token)
	hc := c.HTTP
	if hc == nil {
		hc = postmarkHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("postmark: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	pe := &PostmarkError{}
	if json.Unmarshal(raw, pe) != nil || pe.Message == "" {
		pe.Message = string(bytes.TrimSpace(raw))
	}
	pe.Status = resp.StatusCode
	return pe
}
