package partials

import (
	"reflect"
	"testing"
)

func TestSteps(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"1. Print the sheet\n2) Walk the floor\n\n- Type it up", []string{"Print the sheet", "Walk the floor", "Type it up"}},
		{"We print it. Then we walk it. Then we type it.", []string{"We print it.", "Then we walk it.", "Then we type it."}},
		{"One step only", []string{"One step only"}},
		{"", nil},
	}
	for _, tc := range tests {
		if got := Steps(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Steps(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExcerpt(t *testing.T) {
	if got := Excerpt("Short.", 50); got != "Short." {
		t.Fatalf("got %q", got)
	}
	long := "Invoices arrive as PDFs from nine carriers and nobody can match them to shipments quickly enough, so we overpay"
	if got := Excerpt(long, 60); len([]rune(got)) > 61 || got[len(got)-3:] != "…" {
		t.Fatalf("got %q", got)
	}
	if got := Excerpt("The first sentence is long enough to count. Second sentence.", 200); got != "The first sentence is long enough to count." {
		t.Fatalf("got %q", got)
	}
}
