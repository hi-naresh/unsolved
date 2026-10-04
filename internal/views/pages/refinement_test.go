package pages

import (
	"context"
	"strings"
	"testing"
)

func TestProblemPreviewOffersRefinement(t *testing.T) {
	var out strings.Builder
	if err := ProblemPreview(ProblemPageView{ID: "example", State: "open"}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Refine or correct", "Revision history", `/p/example/revise`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("preview missing %q", want)
		}
	}
}

func TestRevisionComparisonUsesBranchParent(t *testing.T) {
	var out strings.Builder
	v := EvolutionView{ProblemID: "problem", Revisions: []EvolutionRevision{
		{ID: "root", Title: "Original title", Pain: "Original pain"},
		{ID: "first", Depth: 1, Title: "First branch title", Pain: "Original pain"},
		{ID: "second", Depth: 1, Title: "Second branch title", Pain: "Original pain"},
	}}
	if err := EvolutionContent(v).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	_, second, ok := strings.Cut(out.String(), `id="compare-second"`)
	if !ok {
		t.Fatal("missing branch comparison")
	}
	for _, want := range []string{"Original title", "Second branch title", "Unchanged"} {
		if !strings.Contains(second, want) {
			t.Fatalf("comparison missing %q", want)
		}
	}
	if strings.Contains(second, "First branch title") {
		t.Fatal("comparison used sibling rather than parent")
	}
}
