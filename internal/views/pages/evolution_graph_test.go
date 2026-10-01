package pages

import (
	"strings"
	"testing"
	"time"

	"github.com/hi-naresh/unsolved/internal/views/partials"
)

func TestBuildEvolutionGraph(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rev := func(id string, depth int, votes int32, day int, current bool) EvolutionRevision {
		return EvolutionRevision{ID: id, Depth: depth, Title: "T " + id, Pain: "p", IsCurrent: current,
			Vote: partials.VoteButtonView{Count: votes}, Created: t0.AddDate(0, 0, day)}
	}
	// Pre-order tree:   a ─┬─ b ─┬─ c (current)
	//                    │     └─ d
	//                    └─ e ─── f
	// e was written before c and d, so it gets an earlier version number.
	revs := []EvolutionRevision{
		rev("a", 0, 1, 0, false),
		rev("b", 1, 4, 1, false),
		rev("c", 2, 16, 5, true),
		rev("d", 2, 0, 6, false),
		rev("e", 1, 2, 2, false),
		rev("f", 2, 0, 7, false),
	}
	revs[1].Title = "Changed title"
	g := buildEvolutionGraph(revs)

	if len(g.Nodes) != 6 || len(g.Edges) != 5 || g.Depths != 3 {
		t.Fatalf("nodes=%d edges=%d depths=%d", len(g.Nodes), len(g.Edges), g.Depths)
	}
	wantParent := []int{-1, 0, 1, 1, 0, 4}
	wantVersion := []int{1, 2, 4, 5, 3, 6}
	// Leaves c, d, f take rows 0, 1, 2; b sits between c and d, a between b and e.
	wantRow := []float64{1.25, 0.5, 0, 1, 2, 2}
	for i, n := range g.Nodes {
		if n.Parent != wantParent[i] {
			t.Errorf("node %d parent = %d, want %d", i, n.Parent, wantParent[i])
		}
		if n.Version != wantVersion[i] {
			t.Errorf("node %d version = %d, want %d", i, n.Version, wantVersion[i])
		}
		if want := evoPadX + float64(revs[i].Depth)*evoColW; n.X != want {
			t.Errorf("node %d x = %v, want %v", i, n.X, want)
		}
		if want := evoPadTop + wantRow[i]*evoRowH; n.Y != want {
			t.Errorf("node %d y = %v, want %v", i, n.Y, want)
		}
		if n.R < evoMinR || n.R > evoMaxR {
			t.Errorf("node %d radius %v out of range", i, n.R)
		}
	}
	if g.Nodes[2].R != evoMaxR || g.Nodes[3].R != evoMinR || !(g.Nodes[1].R > g.Nodes[0].R) {
		t.Errorf("radius should grow with votes: %v %v %v %v", g.Nodes[0].R, g.Nodes[1].R, g.Nodes[2].R, g.Nodes[3].R)
	}
	// Lineage: a → b → c.
	for i, want := range []bool{true, true, true, false, false, false} {
		if g.Nodes[i].OnLineage != want {
			t.Errorf("node %d lineage = %v", i, g.Nodes[i].OnLineage)
		}
	}
	lineageEdges := 0
	for _, e := range g.Edges {
		if !strings.HasPrefix(e.D, "M") || !strings.Contains(e.D, " C") {
			t.Errorf("edge path %q is not a cubic bézier", e.D)
		}
		if e.OnLineage {
			lineageEdges++
		}
	}
	if lineageEdges != 2 {
		t.Errorf("lineage edges = %d, want 2", lineageEdges)
	}
	// First edge a→b starts at a's right edge and ends at b's left edge.
	a, b := g.Nodes[0], g.Nodes[1]
	if want := evolutionCurve(a.X+a.R, a.Y, b.X-b.R, b.Y); g.Edges[0].D != want {
		t.Errorf("edge a→b = %q, want %q", g.Edges[0].D, want)
	}
	if got := strings.Join(g.Nodes[1].Changed, ","); got != "title" {
		t.Errorf("b changed = %q, want title", got)
	}
	if !g.Nodes[0].IsRoot || g.Nodes[0].Caption != "first version" || !g.Nodes[2].IsCurrent {
		t.Error("root/current flags wrong")
	}
	if want := 2*evoPadX + 2*evoColW; g.Width != want {
		t.Errorf("width = %v, want %v", g.Width, want)
	}
	if want := evoPadTop + 2*evoRowH + evoPadBot; g.Height != want {
		t.Errorf("height = %v, want %v", g.Height, want)
	}

	if e := buildEvolutionGraph(nil); len(e.Nodes) != 0 || len(e.Edges) != 0 {
		t.Error("empty input should give an empty graph")
	}
	one := buildEvolutionGraph([]EvolutionRevision{rev("z", 0, 0, 0, true)})
	if len(one.Nodes) != 1 || len(one.Edges) != 0 || one.Nodes[0].Version != 1 || one.Width <= 0 || one.Height <= 0 {
		t.Errorf("single revision graph: %+v", one)
	}
}
