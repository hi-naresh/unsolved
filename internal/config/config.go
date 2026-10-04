// Package config loads every environment variable the server reads, once,
// into a typed struct. Nothing else in the codebase calls os.Getenv.
package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	BaseURL     string
	SessionTTL  time.Duration

	LinkedInClientID     string
	LinkedInClientSecret string
	XClientID            string
	XClientSecret        string
	GoogleClientID       string
	GoogleClientSecret   string
	GitHubClientID       string
	GitHubClientSecret   string
	RedditClientID       string
	RedditClientSecret   string
	RedditUserAgent      string

	PostmarkToken string

	RankHalfLife           time.Duration
	RankMinVotesToTakeOver int32
	SoftSolvedThreshold    int32
	RankWeightsJSON        string
	RankWeights            RankWeights

	MLURL        string
	SentryDSN    string
	AdminHandles []string
}

// RankWeights is the parsed form of the private RANK_WEIGHTS_JSON. Its values
// are never committed; the zero value (everyone weighs 1.0) is the launch
// behaviour.
type RankWeights struct {
	Tiers map[string]float64 `json:"tiers"` // standing_tier -> vote weight
	// Suspicious patterns are down-weighted by multiplying by this factor.
	SuspiciousFactor float64 `json:"suspicious_factor"`
	// Accounts younger than this many hours count as suspicious.
	NewAccountHours float64 `json:"new_account_hours"`
	// Summed weight at which a community state vote flips a problem (phase 4).
	StateVoteThreshold float64 `json:"state_vote_threshold"`
	// Standing points needed for each tier (phase 4).
	TierPoints map[string]float64 `json:"tier_points"`
}

// Load reads the environment. Missing required values are an error.
func Load() (Config, error) {
	var errs []string
	get := func(k string, required bool) string {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" && required {
			errs = append(errs, k+" is required")
		}
		return v
	}
	dur := func(k string, def time.Duration) time.Duration {
		v := get(k, false)
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, k+": invalid duration")
			return def
		}
		return d
	}
	num := func(k string, def int32) int32 {
		v := get(k, false)
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			errs = append(errs, k+": invalid integer")
			return def
		}
		return int32(n)
	}

	c := Config{
		DatabaseURL:            get("DATABASE_URL", true),
		BaseURL:                strings.TrimRight(get("BASE_URL", true), "/"),
		SessionTTL:             dur("SESSION_TTL", 720*time.Hour),
		LinkedInClientID:       get("LINKEDIN_CLIENT_ID", false),
		LinkedInClientSecret:   get("LINKEDIN_CLIENT_SECRET", false),
		XClientID:              get("X_CLIENT_ID", false),
		XClientSecret:          get("X_CLIENT_SECRET", false),
		GoogleClientID:         get("GOOGLE_CLIENT_ID", false),
		GoogleClientSecret:     get("GOOGLE_CLIENT_SECRET", false),
		GitHubClientID:         get("GITHUB_CLIENT_ID", false),
		GitHubClientSecret:     get("GITHUB_CLIENT_SECRET", false),
		RedditClientID:         get("REDDIT_CLIENT_ID", false),
		RedditClientSecret:     get("REDDIT_CLIENT_SECRET", false),
		RedditUserAgent:        get("REDDIT_USER_AGENT", false),
		PostmarkToken:          get("POSTMARK_TOKEN", false),
		RankHalfLife:           dur("RANK_HALF_LIFE", 720*time.Hour),
		RankMinVotesToTakeOver: num("RANK_MIN_VOTES_TO_TAKE_OVER", 3),
		SoftSolvedThreshold:    num("SOFT_SOLVED_THRESHOLD", 5),
		RankWeightsJSON:        get("RANK_WEIGHTS_JSON", false),
		MLURL:                  strings.TrimRight(get("ML_URL", false), "/"),
		SentryDSN:              get("SENTRY_DSN", false),
	}
	for _, h := range strings.Split(get("ADMIN_HANDLES", false), ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			c.AdminHandles = append(c.AdminHandles, h)
		}
	}
	if c.BaseURL != "" {
		if u, err := url.Parse(c.BaseURL); err != nil || u.Scheme == "" || u.Host == "" {
			errs = append(errs, "BASE_URL: must be an absolute URL")
		}
	}
	if c.RankWeightsJSON != "" {
		if err := json.Unmarshal([]byte(c.RankWeightsJSON), &c.RankWeights); err != nil {
			errs = append(errs, "RANK_WEIGHTS_JSON: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("config: %s", strings.Join(errs, "; "))
	}
	return c, nil
}

// SecureCookies is true unless BASE_URL is plain http (local development).
func (c Config) SecureCookies() bool { return !strings.HasPrefix(c.BaseURL, "http://") }

// IsAdmin reports whether handle is listed in ADMIN_HANDLES.
func (c Config) IsAdmin(handle string) bool {
	for _, h := range c.AdminHandles {
		if h == handle {
			return true
		}
	}
	return false
}

// SigningKey derives a purpose-specific HMAC key from the existing secrets
// (no separate config key exists for it). Keep the original inputs and order:
// adding provider credentials must not invalidate existing sessions.
func (c Config) SigningKey(purpose string) []byte {
	m := hmac.New(sha256.New, []byte("unsolved-signing-v1"))
	for _, s := range []string{c.LinkedInClientSecret, c.XClientSecret, c.PostmarkToken, c.DatabaseURL} {
		m.Write([]byte(s))
		m.Write([]byte{0})
	}
	root := m.Sum(nil)
	k := hmac.New(sha256.New, root)
	k.Write([]byte(purpose))
	return k.Sum(nil)
}
