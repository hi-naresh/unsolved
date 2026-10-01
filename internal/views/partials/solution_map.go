package partials

import (
	"fmt"
	"math"
	"strconv"
)

// MapNode is one solution drawn on the problem's solution map.
type MapNode struct {
	ID    string
	Kind  string
	X, Y  float64
	R     float64
	Votes int32
	Label string
	Top   bool
}

// SolutionMapView is the radial problem→solutions graph: the problem in the
// centre, one node per solution around it, sized by votes, coloured by kind.
type SolutionMapView struct {
	W, H   float64
	CX, CY float64
	Nodes  []MapNode
	Extra  int // solutions beyond the ones drawn
}

const mapMax = 10

// SolutionMap lays out up to 10 solutions (already ordered by score) on a
// ring around the problem.
func SolutionMap(sols []SolutionView) SolutionMapView {
	v := SolutionMapView{W: 320, H: 190, CX: 160, CY: 95}
	n := len(sols)
	if n > mapMax {
		v.Extra = n - mapMax
		n = mapMax
	}
	var maxVotes int32 = 1
	for _, s := range sols[:n] {
		if s.Vote.Count > maxVotes {
			maxVotes = s.Vote.Count
		}
	}
	for i, s := range sols[:n] {
		angle := -math.Pi/2 + 2*math.Pi*float64(i)/float64(n)
		if n == 1 {
			angle = 0
		}
		ring := 70.0
		if n > 6 && i%2 == 1 {
			ring = 64 // stagger dense maps so nodes don't collide
		}
		r := 8 + 12*math.Sqrt(float64(s.Vote.Count)/float64(maxVotes))
		v.Nodes = append(v.Nodes, MapNode{
			ID: s.ID, Kind: s.Kind, Votes: s.Vote.Count, Top: i == 0 && s.Vote.Count > 0,
			X: round1(v.CX + ring*1.25*math.Cos(angle)), Y: round1(v.CY + ring*math.Sin(angle)), R: round1(r),
			Label: SolutionKindLabel(s.Kind),
		})
	}
	return v
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// F formats a coordinate for SVG attributes.
func F(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// NodeTitle is the hover text for a map node.
func NodeTitle(n MapNode) string {
	return fmt.Sprintf("%s · %d vote%s", n.Label, n.Votes, plural(int64(n.Votes)))
}

func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// OutcomeTotals summarises "tried it" outcomes for the outcome bar.
type OutcomeTotals struct{ Worked, Partly, Failed, Total int64 }

func Outcomes(s SolutionView) OutcomeTotals {
	return OutcomeTotals{s.Worked, s.Partly, s.Failed, s.Worked + s.Partly + s.Failed}
}

// Pct is n as a percentage width of total, for inline styles.
func Pct(n, total int64) string {
	if total == 0 {
		return "0%"
	}
	return strconv.FormatFloat(float64(n)*100/float64(total), 'f', 1, 64) + "%"
}
