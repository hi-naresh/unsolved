package auth

import (
	"context"

	"github.com/google/uuid"
)

// User is the signed-in viewer, loaded from the session on every request.
type User struct {
	ID          uuid.UUID
	Handle      string
	DisplayName string
	Suspended   bool
	IsAdmin     bool
}

type ctxKey int

const (
	userKey ctxKey = iota
	csrfKey
)

// WithUser stores the viewer in ctx.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFrom returns the viewer, or nil when signed out.
func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(userKey).(*User)
	return u
}

// CSRFToken returns the request's CSRF token for forms and the htmx
// hx-headers attribute set once in the layout.
func CSRFToken(ctx context.Context) string {
	t, _ := ctx.Value(csrfKey).(string)
	return t
}
