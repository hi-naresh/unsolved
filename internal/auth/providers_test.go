package auth

import (
	"bytes"
	"testing"

	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/store"
)

func TestEnabledRequiresProviderCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, id, secret string
		want             bool
	}{
		{"configured", "client-id", "client-secret", true},
		{"empty", "", "", false},
		{"missing secret", "client-id", "", false},
		{"missing id", "", "client-secret", false},
		{"example credentials", "replace-me", "replace-me", false},
		{"placeholder id", "replace-me", "client-secret", false},
		{"placeholder secret", "client-id", "replace-me", false},
		{"whitespace", "  ", "client-secret", false},
		{"padded placeholder", " Replace-Me ", "client-secret", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Auth{cfg: config.Config{LinkedInClientID: tc.id, LinkedInClientSecret: tc.secret, XClientID: tc.id, XClientSecret: tc.secret, GoogleClientID: tc.id, GoogleClientSecret: tc.secret, GitHubClientID: tc.id, GitHubClientSecret: tc.secret, RedditClientID: tc.id, RedditClientSecret: tc.secret}}
			a.cfg.RedditUserAgent = "web:unsolved-tests:v1 (by /u/test_operator)"
			for _, provider := range []store.Provider{store.ProviderLinkedin, store.ProviderX, store.ProviderGoogle, store.ProviderGithub, store.ProviderReddit} {
				if got := a.Enabled(provider); got != tc.want {
					t.Errorf("%s enabled = %v, want %v", provider, got, tc.want)
				}
			}
		})
	}
}

func TestRedditRequiresIdentifyingUserAgent(t *testing.T) {
	for _, ua := range []string{"", "  ", "replace-me", "client\r\nInjected: header"} {
		a := &Auth{cfg: config.Config{RedditClientID: "client", RedditClientSecret: "secret", RedditUserAgent: ua}}
		if a.Enabled(store.ProviderReddit) {
			t.Errorf("Reddit enabled with invalid User-Agent %q", ua)
		}
	}
}

func TestNewProviderSecretsDoNotRotateExistingSigningKey(t *testing.T) {
	base := config.Config{DatabaseURL: "db", LinkedInClientSecret: "li", XClientSecret: "x", PostmarkToken: "postmark"}
	want := base.SigningKey("session")
	base.GoogleClientSecret = "google"
	base.GitHubClientSecret = "github"
	base.RedditClientSecret = "reddit"
	if !bytes.Equal(want, base.SigningKey("session")) {
		t.Fatal("adding provider credentials rotated existing session key")
	}
}
