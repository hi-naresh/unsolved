package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
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
