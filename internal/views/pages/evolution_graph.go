package pages

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// Layout constants for the evolution graph, in SVG user units.
const (
	evoColW   = 172.0 // horizontal distance between depths
	evoRowH   = 74.0  // vertical distance between leaves
	evoPadX   = 70.0
	evoPadTop = 54.0
	evoPadBot = 46.0
	evoMinR   = 7.0
	evoMaxR   = 17.0
)

// EvolutionNode is one revision placed on the graph.
type EvolutionNode struct {
	Index     int // position in EvolutionView.Revisions
	ID        string
	Version   int // 1-based, in creation order
	Parent    int // index of the parent revision, -1 for a root
	X, Y, R   float64
	Votes     int32
	IsCurrent bool
	IsRoot    bool
	OnLineage bool     // the current version or one of its ancestors
	Changed   []string // fields that differ from the parent ("title", "process", …)
	Tooltip   string
	Caption   string // short why-note (or "first version") under the label
}

// EvolutionEdge is a parent→child curve.
type EvolutionEdge struct {
	D         string // SVG path data (cubic bézier)
	OnLineage bool   // edge leads to the current version
}

// EvolutionGraph is the laid-out revision tree.
type EvolutionGraph struct {
	Width, Height float64
	Nodes         []EvolutionNode
	Edges         []EvolutionEdge
	Depths        int
}

// buildEvolutionGraph lays out revs, which are in tree pre-order with Depth
// (a node's parent is the nearest earlier node one level up). Depth runs
// left→right; leaves take consecutive rows top→bottom and each parent sits
// at the middle of its children (a simple tidy tree). Node radius grows with
// the square root of the vote count, between evoMinR and evoMaxR.
func buildEvolutionGraph(revs []EvolutionRevision) EvolutionGraph {
	n := len(revs)
	g := EvolutionGraph{Nodes: make([]EvolutionNode, n)}
	if n == 0 {
		return g
	}
	parent := make([]int, n)
	children := make([][]int, n)
	var stack []int // stack[d] = latest node seen at depth d
	maxVotes := int32(0)
	for i, r := range revs {
		d := r.Depth
		if d < 0 {
			d = 0
		}
		if d > len(stack) { // malformed depth jump: attach to the deepest node
			d = len(stack)
		}
		stack = append(stack[:d], i)
		parent[i] = -1
		if d > 0 {
			parent[i] = stack[d-1]
			children[parent[i]] = append(children[parent[i]], i)
		}
		if r.Vote.Count > maxVotes {
			maxVotes = r.Vote.Count
		}
		if d+1 > g.Depths {
			g.Depths = d + 1
		}
		g.Nodes[i].X = evoPadX + float64(d)*evoColW
	}

	// Rows: leaves in pre-order get 0, 1, 2…; a parent sits midway between
	// its first and last child. Children always come after their parent in
	// pre-order, so a reverse pass sees every child before its parent.
	row := make([]float64, n)
	leaf := 0
	for i := range revs {
		if len(children[i]) == 0 {
			row[i] = float64(leaf)
			leaf++
		}
	}
	for i := n - 1; i >= 0; i-- {
		if k := children[i]; len(k) > 0 {
			row[i] = (row[k[0]] + row[k[len(k)-1]]) / 2
		}
	}

	versions := evolutionVersions(revs)
	lineage := make([]bool, n)
	for i, r := range revs {
		if r.IsCurrent {
			for j := i; j >= 0; j = parent[j] {
				lineage[j] = true
			}
		}
	}

	for i, r := range revs {
		nd := &g.Nodes[i]
		nd.Index, nd.ID, nd.Parent = i, r.ID, parent[i]
		nd.Version = versions[i]
		nd.Y = evoPadTop + row[i]*evoRowH
		nd.R = evolutionRadius(r.Vote.Count, maxVotes)
		nd.Votes = r.Vote.Count
		nd.IsCurrent = r.IsCurrent
		nd.IsRoot = parent[i] < 0
		nd.OnLineage = lineage[i]
		if p := parent[i]; p >= 0 {
			nd.Changed = evolutionChanged(revs[p], r)
		}
		nd.Tooltip = evolutionTooltip(*nd, r)
		switch {
		case r.WhyNote != "":
			nd.Caption = evolutionFirstLine(r.WhyNote, 24)
		case nd.IsRoot:
			nd.Caption = "first version"
		}
	}
	for i := range revs {
		p := parent[i]
		if p < 0 {
			continue
		}
		a, b := g.Nodes[p], g.Nodes[i]
		g.Edges = append(g.Edges, EvolutionEdge{D: evolutionCurve(a.X+a.R, a.Y, b.X-b.R, b.Y), OnLineage: lineage[i]})
	}
	g.Width = 2*evoPadX + float64(g.Depths-1)*evoColW
	g.Height = evoPadTop + float64(leaf-1)*evoRowH + evoPadBot
	return g
}

// evolutionVersions numbers revisions 1…n in creation order (ties and a
// missing time fall back to tree order).
func evolutionVersions(revs []EvolutionRevision) []int {
	idx := make([]int, len(revs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ta, tb := revs[idx[a]].Created, revs[idx[b]].Created
		if ta.IsZero() || tb.IsZero() {
			return false
		}
		return ta.Before(tb)
	})
	out := make([]int, len(revs))
	for v, i := range idx {
		out[i] = v + 1
	}
	return out
}

func evolutionRadius(votes, max int32) float64 {
	if max <= 0 || votes <= 0 {
		return evoMinR
	}
	return evoMinR + (evoMaxR-evoMinR)*math.Sqrt(float64(votes)/float64(max))
}

// evolutionCurve is a horizontal S-curve from (x1,y1) to (x2,y2).
func evolutionCurve(x1, y1, x2, y2 float64) string {
	mx := (x1 + x2) / 2
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	return "M" + f(x1) + " " + f(y1) + " C" + f(mx) + " " + f(y1) + " " + f(mx) + " " + f(y2) + " " + f(x2) + " " + f(y2)
}

// evolutionChanged names the parts of r that differ from its parent p.
func evolutionChanged(p, r EvolutionRevision) []string {
	var out []string
	same := func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) }
	if !same(p.Title, r.Title) {
		out = append(out, "title")
	}
	if !same(p.CurrentProcess, r.CurrentProcess) {
		out = append(out, "process")
	}
	if !same(p.Pain, r.Pain) {
		out = append(out, "pain")
	}
	if !same(p.Tried, r.Tried) {
		out = append(out, "tried")
	}
	return out
}

func evolutionTooltip(nd EvolutionNode, r EvolutionRevision) string {
	var b strings.Builder
	b.WriteString("v" + strconv.Itoa(nd.Version))
	if nd.IsCurrent {
		b.WriteString(" (current)")
	}
	b.WriteString(": " + r.Title + "\n")
	b.WriteString(strconv.Itoa(int(r.Vote.Count)) + " votes")
	if r.WhyNote != "" {
		b.WriteString("\nWhy: " + evolutionFirstLine(r.WhyNote, 140))
	} else if nd.IsRoot {
		b.WriteString("\nFirst version")
	}
	return b.String()
}

// evolutionFirstLine is the first non-empty line of s, cut to n runes.
func evolutionFirstLine(s string, n int) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return userTruncate(l, n)
		}
	}
	return ""
}

func evoF(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
