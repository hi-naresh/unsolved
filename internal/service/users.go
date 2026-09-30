package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SignInWithIdentity finds the user for a provider identity, or creates the
// user and identity in one transaction (created = true → send to /welcome).
// New users get a placeholder handle they replace on /welcome.
func (s *Service) SignInWithIdentity(ctx context.Context, provider store.Provider, providerUID, profileURL, displayName string) (u store.User, created bool, err error) {
	ident, err := s.Store.GetIdentity(ctx, store.GetIdentityParams{Provider: provider, ProviderUid: providerUID})
	if err == nil {
		u, err = s.Store.GetUserByID(ctx, ident.UserID)
		if err != nil {
			return u, false, err
		}
		if u.DeletedAt != nil {
			return u, false, ErrForbidden
		}
		return u, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return u, false, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "New member"
	}
	if r := []rune(displayName); len(r) > 80 {
		displayName = string(r[:80])
	}
	id, err := uuid.NewV7()
	if err != nil {
		return u, false, err
	}
	var url *string
	if profileURL != "" {
		url = &profileURL
	}
	err = s.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
		var err error
		u, err = q.CreateUser(ctx, store.CreateUserParams{ID: id, Handle: placeholderHandle(), DisplayName: displayName})
		if err != nil {
			return err
		}
		return q.CreateIdentity(ctx, store.CreateIdentityParams{UserID: id, Provider: provider, ProviderUid: providerUID, ProfileUrl: url})
	})
	return u, err == nil, err
}

func placeholderHandle() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return "user_" + hex.EncodeToString(b)
}

// NewSession creates a server-side session and returns the raw cookie value
// (32 random bytes). Only its SHA-256 is stored.
func (s *Service) NewSession(ctx context.Context, userID uuid.UUID) (token string, expires time.Time, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", expires, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	expires = s.Now().Add(s.Cfg.SessionTTL)
	err = s.Store.CreateSession(ctx, store.CreateSessionParams{TokenHash: hashToken(token), UserID: userID, ExpiresAt: expires})
	return token, expires, err
}

// SessionUser returns the live user for a raw session token.
func (s *Service) SessionUser(ctx context.Context, token string) (store.User, error) {
	u, err := s.Store.GetSessionUser(ctx, hashToken(token))
	return u, notFound(err)
}

// EndSession deletes the session row (sign-out).
func (s *Service) EndSession(ctx context.Context, token string) error {
	return s.Store.DeleteSession(ctx, hashToken(token))
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Handle rules: the users.handle CHECK is ^[a-z0-9_]{3,24}$. Reserved handles
// are refused so nobody can pose as the site or as a scrubbed account.
var handleRE = regexp.MustCompile(`^[a-z0-9_]{3,24}$`)

var reservedHandles = map[string]bool{
	"admin": true, "administrator": true, "unsolved": true, "anonymous": true,
	"deleted": true, "moderator": true, "mod": true, "root": true,
	"system": true, "support": true, "staff": true, "help": true,
	"settings": true, "members": true, "welcome": true, "signin": true,
	"privacy": true, "about": true, "meta": true, "new": true, "search": true,
}

const (
	maxDeclaredHistory = 2000
	maxEmail           = 254
	directoryPageSize  = 30
	profileMaxItems    = 30
)

// NormalizeHandle lowercases and trims user input before validation.
func NormalizeHandle(h string) string { return strings.ToLower(strings.TrimSpace(h)) }

// ValidateHandle checks format and reserved names (not uniqueness).
func ValidateHandle(h string) error {
	if !handleRE.MatchString(h) {
		return Invalid("handle", "Handles are 3–24 characters: lowercase letters, digits and underscores.")
	}
	if reservedHandles[h] || strings.HasPrefix(h, "deleted_") {
		return Invalid("handle", "That handle is reserved. Pick another.")
	}
	return nil
}

// handleTaken reports whether err is the users.handle unique violation.
func handleTaken(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "users_handle_key"
}

// GetUser returns a live (not deleted) user by id.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (store.User, error) {
	u, err := s.Store.GetUserByID(ctx, id)
	if err != nil {
		return u, notFound(err)
	}
	if u.DeletedAt != nil {
		return u, ErrNotFound
	}
	return u, nil
}

// CompleteWelcome sets the handle and directory opt-in chosen on /welcome.
func (s *Service) CompleteWelcome(ctx context.Context, userID uuid.UUID, handle string, inDirectory bool) error {
	handle = NormalizeHandle(handle)
	if err := ValidateHandle(handle); err != nil {
		return err
	}
	if err := s.guardAdminHandle(ctx, userID, handle); err != nil {
		return err
	}
	err := s.Store.SetUserHandleAndDirectory(ctx, store.SetUserHandleAndDirectoryParams{ID: userID, Handle: handle, InDirectory: inDirectory})
	if handleTaken(err) {
		return Invalid("handle", "That handle is taken. Pick another.")
	}
	return err
}

// Settings is the editable part of a user's profile.
type Settings struct {
	Handle          string
	InDirectory     bool
	DeclaredHistory string // empty clears; shown labelled unverified, zero weight
	Email           string // empty clears; used only for Solved prompts
}

// SettingsOf returns a user's current settings for the form.
func SettingsOf(u store.User) Settings {
	st := Settings{Handle: u.Handle, InDirectory: u.InDirectory}
	if u.DeclaredHistory != nil {
		st.DeclaredHistory = *u.DeclaredHistory
	}
	if u.Email != nil {
		st.Email = *u.Email
	}
	return st
}

// UpdateSettings validates and saves the settings form.
func (s *Service) UpdateSettings(ctx context.Context, userID uuid.UUID, in Settings) error {
	in.Handle = NormalizeHandle(in.Handle)
	if err := ValidateHandle(in.Handle); err != nil {
		return err
	}
	if err := s.guardAdminHandle(ctx, userID, in.Handle); err != nil {
		return err
	}
	history := strings.TrimSpace(in.DeclaredHistory)
	if utf8.RuneCountInString(history) > maxDeclaredHistory {
		return Invalid("declared_history", "Declared history is limited to 2000 characters.")
	}
	email := strings.TrimSpace(in.Email)
	if email != "" {
		if err := validateEmail(email); err != nil {
			return err
		}
	}
	err := s.Store.UpdateUserSettings(ctx, store.UpdateUserSettingsParams{
		ID: userID, Handle: in.Handle, InDirectory: in.InDirectory,
		DeclaredHistory: nilIfEmpty(history), Email: nilIfEmpty(email),
	})
	if handleTaken(err) {
		return Invalid("handle", "That handle is taken. Pick another.")
	}
	return err
}

func validateEmail(email string) error {
	if utf8.RuneCountInString(email) > maxEmail {
		return Invalid("email", "Email addresses are limited to 254 characters.")
	}
	bad := Invalid("email", "That doesn't look like an email address.")
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || a.Name != "" {
		return bad
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return bad
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Profile is what /u/{handle} shows. It deliberately carries no user id.
type Profile struct {
	ReportID        string // user id as text, only for the report form; profiles are always named
	Handle          string
	DisplayName     string
	DeclaredHistory string
	InDirectory     bool
	Suspended       bool
	Links           []ProfileLink // only when InDirectory
	Contributions   []Contribution
}

type ProfileLink struct {
	Provider string
	URL      string
}

// Contribution is one named revision on a profile.
type Contribution struct {
	Kind      string // "problem" or "solution"
	ProblemID uuid.UUID
	Title     string
	IsFirst   bool // posted (first revision) rather than revised
	CreatedAt time.Time
}

// ProfileByHandle loads a public profile: named contributions only (the query
// filters author_display = 'named'), 30 most recent; social links only when
// in_directory. Deleted users are not found. At most 3 queries.
func (s *Service) ProfileByHandle(ctx context.Context, handle string) (Profile, error) {
	var p Profile
	u, err := s.Store.GetUserByHandle(ctx, NormalizeHandle(handle))
	if err != nil {
		return p, notFound(err)
	}
	if u.DeletedAt != nil {
		return p, ErrNotFound
	}
	p = Profile{ReportID: u.ID.String(), Handle: u.Handle, DisplayName: u.DisplayName, InDirectory: u.InDirectory, Suspended: u.SuspendedAt != nil}
	if u.DeclaredHistory != nil {
		p.DeclaredHistory = *u.DeclaredHistory
	}
	if u.InDirectory {
		ids, err := s.Store.ListIdentitiesByUser(ctx, u.ID)
		if err != nil {
			return p, err
		}
		for _, id := range ids {
			if id.ProfileUrl != nil && strings.HasPrefix(*id.ProfileUrl, "https://") {
				p.Links = append(p.Links, ProfileLink{Provider: string(id.Provider), URL: *id.ProfileUrl})
			}
		}
	}
	rows, err := s.Store.ListNamedContributions(ctx, store.ListNamedContributionsParams{UserID: u.ID, MaxRows: profileMaxItems})
	if err != nil {
		return p, err
	}
	for _, r := range rows {
		p.Contributions = append(p.Contributions, Contribution{Kind: r.Kind, ProblemID: r.ProblemID, Title: r.Title, IsFirst: r.IsFirst, CreatedAt: r.CreatedAt})
	}
	return p, nil
}

// Member is one row of the /members directory. It carries no user id.
type Member struct {
	Handle          string
	DisplayName     string
	DeclaredHistory string
}

// Directory lists opted-in, non-deleted members, 30 per page, keyset by
// (created_at, id). after is the cursor from the previous page ("" for the
// first); next is "" on the last page.
func (s *Service) Directory(ctx context.Context, after string) (members []Member, next string, err error) {
	arg := store.ListDirectoryParams{MaxRows: directoryPageSize + 1}
	if after != "" {
		t, id, ok := parseDirectoryCursor(after)
		if !ok {
			return nil, "", Invalid("after", "That page link is broken.")
		}
		arg.AfterCreatedAt, arg.AfterID = &t, &id
	}
	rows, err := s.Store.ListDirectory(ctx, arg)
	if err != nil {
		return nil, "", err
	}
	if len(rows) > directoryPageSize {
		rows = rows[:directoryPageSize]
		last := rows[len(rows)-1]
		next = strconv.FormatInt(last.CreatedAt.UnixMicro(), 10) + "_" + last.ID.String()
	}
	for _, r := range rows {
		m := Member{Handle: r.Handle, DisplayName: r.DisplayName}
		if r.DeclaredHistory != nil {
			m.DeclaredHistory = *r.DeclaredHistory
		}
		members = append(members, m)
	}
	return members, next, nil
}

func parseDirectoryCursor(s string) (time.Time, uuid.UUID, bool) {
	ts, idStr, ok := strings.Cut(s, "_")
	if !ok {
		return time.Time{}, uuid.Nil, false
	}
	us, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return time.UnixMicro(us).UTC(), id, true
}

// Admin rights come from ADMIN_HANDLES, so admin handles can never change
// hands through self-service: nobody can claim one on /welcome or /settings,
// and whoever holds one can't rename away from it (which would free it for
// someone else). An operator assigns one with `seed admin-handle`.
func (s *Service) guardAdminHandle(ctx context.Context, userID uuid.UUID, next string) error {
	u, err := s.Store.GetUserByID(ctx, userID)
	if err != nil {
		return notFound(err)
	}
	if u.Handle == next {
		return nil
	}
	if s.Cfg.IsAdmin(next) {
		return Invalid("handle", "That handle is reserved. Pick another.")
	}
	if s.Cfg.IsAdmin(u.Handle) {
		return Invalid("handle", "Admin handles can't be changed here.")
	}
	return nil
}

// AssignAdminHandle gives the user currently called fromHandle the admin
// handle adminHandle (which must be listed in ADMIN_HANDLES and unclaimed).
// Operator-only: run through cmd/seed with database access.
func (s *Service) AssignAdminHandle(ctx context.Context, fromHandle, adminHandle string) error {
	adminHandle = NormalizeHandle(adminHandle)
	if !s.Cfg.IsAdmin(adminHandle) {
		return Invalid("handle", adminHandle+" is not listed in ADMIN_HANDLES")
	}
	if !handleRE.MatchString(adminHandle) {
		return Invalid("handle", "invalid handle")
	}
	u, err := s.Store.GetUserByHandle(ctx, NormalizeHandle(fromHandle))
	if err != nil {
		return notFound(err)
	}
	if u.DeletedAt != nil {
		return ErrNotFound
	}
	err = s.Store.SetUserHandle(ctx, store.SetUserHandleParams{ID: u.ID, Handle: adminHandle})
	if handleTaken(err) {
		return ErrConflict
	}
	if err == nil {
		s.Log.Warn("admin handle assigned", "user_id", u.ID, "handle", adminHandle)
	}
	return err
}
