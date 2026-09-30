package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeVec is the fake ML service's embedding: a normalised bag of hashed
// words, so texts sharing most words are close.
func fakeVec(text string) []float32 {
	v := make([]float32, EmbeddingDim)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) })
	for _, w := range words {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%EmbeddingDim]++
	}
	var n float64
	for _, f := range v {
		n += float64(f * f)
	}
	if n == 0 {
		v[0], n = 1, 1
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / math.Sqrt(n))
	}
	return v
}

// fakeML starts an httptest ML service. delay slows /embed; status != 200
// makes it fail.
func fakeML(t *testing.T, delay time.Duration, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		if status != http.StatusOK {
			http.Error(w, "boom", status)
			return
		}
		switch r.URL.Path {
		case "/embed":
			var in struct{ Texts []string }
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			out := make([][]float32, len(in.Texts))
			for i, s := range in.Texts {
				out[i] = fakeVec(s)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"vectors": out})
		case "/tag":
			_ = json.NewEncoder(w).Encode(map[string]any{"tags": []string{"logistics", "data entry"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// insertProblem writes a user, problem and first revision with raw SQL (test
// setup only) and returns the problem and revision ids.
func insertProblem(t *testing.T, pool *pgxpool.Pool, title, process, pain string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	uid, pid, rid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	insertUser(t, pool, uid)
	_, err := pool.Exec(ctx, `
		WITH p AS (
		  INSERT INTO problems (id, domain_id, author_id, author_display, current_revision_id)
		  VALUES ($1, 3, $2, 'named', $3)
		)
		INSERT INTO problem_revisions (id, problem_id, author_id, author_display, title, current_process, pain, tried)
		VALUES ($3, $1, $2, 'named', $4, $5, $6, 'nothing yet')`,
		pid, uid, rid, title, process, pain)
	if err != nil {
		t.Fatalf("insert problem: %v", err)
	}
	return pid, rid
}

func insertUser(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	handle := "u_" + strings.ReplaceAll(id.String(), "-", "")[20:]
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id, handle, display_name) VALUES ($1, $2, 'Test')`, id, handle); err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func TestVectorLiteralRoundTrip(t *testing.T) {
	v := []float32{0.1, -0.25, 1e-7, 0}
	lit := VectorLiteral(v)
	if lit != "[0.1,-0.25,1e-07,0]" {
		t.Fatalf("literal %q", lit)
	}
	got, err := ParseVector(lit)
	if err != nil {
		t.Fatal(err)
	}
	for i := range v {
		if got[i] != v[i] {
			t.Fatalf("round trip %v != %v", got, v)
		}
	}
	if _, err := ParseVector("0.1,0.2"); err == nil {
		t.Fatal("want error for missing brackets")
	}
}

func TestMLClient(t *testing.T) {
	ctx := context.Background()
	if _, err := NewMLClient("").Embed(ctx, []string{"x"}); !errors.Is(err, ErrMLDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := NewMLClient("").Tag(ctx, "x"); !errors.Is(err, ErrMLDisabled) {
		t.Fatalf("disabled tag: %v", err)
	}

	c := NewMLClient(fakeML(t, 0, http.StatusOK).URL + "/")
	vecs, err := c.Embed(ctx, []string{"a b c", "d e f"})
	if err != nil || len(vecs) != 2 || len(vecs[0]) != EmbeddingDim {
		t.Fatalf("embed: %v %d", err, len(vecs))
	}
	tags, err := c.Tag(ctx, "x")
	if err != nil || fmt.Sprint(tags) != "[logistics data entry]" {
		t.Fatalf("tag: %v %v", tags, err)
	}

	if _, err := NewMLClient(fakeML(t, 0, http.StatusInternalServerError).URL).Embed(ctx, []string{"x"}); err == nil {
		t.Fatal("want error on 500")
	}
	slow := NewMLClient(fakeML(t, 300*time.Millisecond, http.StatusOK).URL)
	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := slow.Embed(tctx, []string{"x"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
}
