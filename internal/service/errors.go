package service

import (
	"errors"
	"fmt"
)

// Typed errors returned by every service method. One piece of HTTP
// middleware (internal/http/errors.go) maps them to status codes.
var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
	ErrConflict  = errors.New("conflict")
)

// ErrValidation reports a bad field. Use errors.As to detect it.
type ErrValidation struct {
	Field string
	Msg   string
}

func (e ErrValidation) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Msg) }

// Invalid is shorthand for returning an ErrValidation.
func Invalid(field, msg string) error { return ErrValidation{Field: field, Msg: msg} }
