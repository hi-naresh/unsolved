package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
)

func TestCreateReportValidation(t *testing.T) {
	ctx := context.Background()
	s := modSvc(t)
	reporter, _ := modUser(t, s)
	author, _ := modUser(t, s)
	pid, prev := modProblem(t, s, author, store.DisplayModeNamed)
	srev := modSolution(t, s, pid, author)

	tests := []struct {
		name      string
		kind, id  string
		reason    string
		wantField string // ErrValidation field; "" = no validation error
		wantErr   error
	}{
		{name: "problem revision", kind: service.ReportProblemRevision, id: prev.String(), reason: "Spam link in the text"},
		{name: "solution revision", kind: service.ReportSolutionRevision, id: srev.String(), reason: "Advertising a product"},
		{name: "user", kind: service.ReportUser, id: author.String(), reason: "Posting abuse repeatedly"},
		{name: "bad kind", kind: "problem", id: prev.String(), reason: "Spam link in the text", wantField: "target_kind"},
		{name: "bad id", kind: service.ReportUser, id: "nope", reason: "Spam link in the text", wantField: "target_id"},
		{name: "reason too short", kind: service.ReportUser, id: author.String(), reason: " bad ", wantField: "reason"},
		{name: "reason too long", kind: service.ReportUser, id: author.String(), reason: strings.Repeat("x", 501), wantField: "reason"},
		{name: "missing target", kind: service.ReportProblemRevision, id: uuid.NewString(), reason: "Spam link in the text", wantErr: service.ErrNotFound},
		{name: "kind mismatch", kind: service.ReportSolutionRevision, id: prev.String(), reason: "Spam link in the text", wantErr: service.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.CreateReport(ctx, reporter, tt.kind, tt.id, tt.reason)
			var ve service.ErrValidation
			switch {
			case tt.wantField != "":
				if !errors.As(err, &ve) || ve.Field != tt.wantField {
					t.Fatalf("want validation error on %s, got %v", tt.wantField, err)
				}
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	var n int
	if err := s.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM reports WHERE reporter_id = $1`, reporter).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("want 3 stored reports, got %d", n)
	}
}
