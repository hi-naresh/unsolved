package service_test

import (
	"context"
	"testing"
)

func TestToggleInterest(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	poster, a, b := e.user(t), e.user(t), e.user(t)
	pid, _ := e.problem(t, poster, "Quoting custom furniture from sketches")

	steps := []struct {
		who        string
		interested bool
		count      int64
	}{{"a", true, 1}, {"b", true, 2}, {"a", false, 1}, {"a", true, 2}}
	for i, st := range steps {
		u := a
		if st.who == "b" {
			u = b
		}
		res, err := e.svc.ToggleInterest(ctx, pid, u)
		if err != nil {
			t.Fatal(err)
		}
		if res.Interested != st.interested || res.Count != st.count {
			t.Fatalf("step %d: %+v", i, res)
		}
	}
	pg, err := e.svc.ProblemPage(ctx, pid, &b)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Problem.InterestCount != 2 || !pg.Problem.ViewerInterested {
		t.Fatalf("page: count %d interested %v", pg.Problem.InterestCount, pg.Problem.ViewerInterested)
	}
}
