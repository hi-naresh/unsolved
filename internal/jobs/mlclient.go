package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// EmbeddingDim is the vector size of all-MiniLM-L6-v2 and of
// problem_revisions.embedding.
const EmbeddingDim = 384

// ErrMLDisabled is returned by every MLClient method when ML_URL is empty.
var ErrMLDisabled = errors.New("ml service disabled (ML_URL empty)")

// mlHTTP is shared by every MLClient that doesn't bring its own. The overall
// timeout is a backstop; callers bound each call with their context.
var mlHTTP = &http.Client{Timeout: 30 * time.Second}

// MLClient talks to the stateless ML service (ml/). It is safe to copy and to
// build per call: the connection pool lives in the shared http.Client.
type MLClient struct {
	BaseURL string       // ML_URL, no trailing slash; empty = disabled
	HTTP    *http.Client // nil = shared default
}

// NewMLClient builds a client for baseURL (config.Config.MLURL).
func NewMLClient(baseURL string) *MLClient {
	return &MLClient{BaseURL: strings.TrimRight(baseURL, "/")}
}

// Enabled reports whether ML_URL is set.
func (c *MLClient) Enabled() bool { return c != nil && c.BaseURL != "" }

// Embed returns one normalised 384-dim vector per text, in order.
func (c *MLClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	var out struct {
		Vectors [][]float32 `json:"vectors"`
	}
	if err := c.post(ctx, "/embed", map[string]any{"texts": texts}, &out); err != nil {
		return nil, err
	}
	if len(out.Vectors) != len(texts) {
		return nil, fmt.Errorf("ml embed: got %d vectors for %d texts", len(out.Vectors), len(texts))
	}
	for i, v := range out.Vectors {
		if len(v) != EmbeddingDim {
			return nil, fmt.Errorf("ml embed: vector %d has %d dims, want %d", i, len(v), EmbeddingDim)
		}
	}
	return out.Vectors, nil
}

// Tag returns zero-shot domain/theme labels for text.
func (c *MLClient) Tag(ctx context.Context, text string) ([]string, error) {
	var out struct {
		Tags []string `json:"tags"`
	}
	if err := c.post(ctx, "/tag", map[string]any{"text": text}, &out); err != nil {
		return nil, err
	}
	return out.Tags, nil
}

func (c *MLClient) post(ctx context.Context, path string, in, out any) error {
	if !c.Enabled() {
		return ErrMLDisabled
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = mlHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("ml %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ml %s: status %d: %s", path, resp.StatusCode, bytes.TrimSpace(msg))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("ml %s: decode: %w", path, err)
	}
	return nil
}

// VectorLiteral formats v as a pgvector text literal: [0.1,0.2,...]. Pass it
// to queries as sqlc.arg(x)::text::vector.
func VectorLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 12)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// ParseVector parses pgvector's text output ([0.1,0.2,...]).
func ParseVector(s string) ([]float32, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("parse vector: bad literal")
	}
	s = s[1 : len(s)-1]
	if s == "" {
		return []float32{}, nil
	}
	parts := strings.Split(s, ",")
	v := make([]float32, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, fmt.Errorf("parse vector: %w", err)
		}
		v[i] = float32(f)
	}
	return v, nil
}

// RevisionText is the text embedded for a problem revision. The duplicate
// check embeds the posting form with the same function so vectors compare.
func RevisionText(title, currentProcess, pain string) string {
	return strings.TrimSpace(title) + "\n\n" + strings.TrimSpace(currentProcess) + "\n\n" + strings.TrimSpace(pain)
}
