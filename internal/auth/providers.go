package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/hi-naresh/unsolved/internal/store"
	"golang.org/x/oauth2"
)

// Endpoints are the provider URLs. Production uses DefaultEndpoints; tests
// point them at fake servers with UseEndpoints. They are not configuration.
type Endpoints struct {
	LinkedInIssuer string // OIDC issuer; discovery at <issuer>/.well-known/openid-configuration
	XAuthURL       string
	XTokenURL      string
	XUserURL       string // GET → {"data":{"id","name","username"}}
	XProfileBase   string // profile URL = XProfileBase + username
	GoogleIssuer   string
	GitHubAuthURL  string
	GitHubTokenURL string
	GitHubUserURL  string
	RedditAuthURL  string
	RedditTokenURL string
	RedditUserURL  string
}

// DefaultEndpoints are the real LinkedIn and X endpoints.
var DefaultEndpoints = Endpoints{
	LinkedInIssuer: "https://www.linkedin.com/oauth",
	XAuthURL:       "https://x.com/i/oauth2/authorize",
	XTokenURL:      "https://api.x.com/2/oauth2/token",
	XUserURL:       "https://api.x.com/2/users/me",
	XProfileBase:   "https://x.com/",
	GoogleIssuer:   "https://accounts.google.com",
	GitHubAuthURL:  "https://github.com/login/oauth/authorize",
	GitHubTokenURL: "https://github.com/login/oauth/access_token",
	GitHubUserURL:  "https://api.github.com/user",
	RedditAuthURL:  "https://www.reddit.com/api/v1/authorize",
	RedditTokenURL: "https://www.reddit.com/api/v1/access_token",
	RedditUserURL:  "https://oauth.reddit.com/api/v1/me",
}

// providers holds the endpoints and the lazily discovered LinkedIn OIDC
// provider (no network at boot; a failed discovery is retried next time).
type providers struct {
	mu     sync.Mutex
	ep     Endpoints
	oidc   *oidc.Provider
	google *oidc.Provider
	client *http.Client
}

func newProviders() *providers {
	return &providers{ep: DefaultEndpoints, client: &http.Client{Timeout: 10 * time.Second}}
}

// UseEndpoints replaces the provider endpoints. For tests only; call it
// before serving requests.
func (a *Auth) UseEndpoints(ep Endpoints) {
	a.prov.mu.Lock()
	defer a.prov.mu.Unlock()
	a.prov.ep = ep
	a.prov.oidc = nil
	a.prov.google = nil
}

func (p *providers) endpoints() Endpoints {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ep
}

func (p *providers) ctx(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, p.client)
}

func (p *providers) linkedIn(ctx context.Context) (*oidc.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.oidc != nil {
		return p.oidc, nil
	}
	pr, err := oidc.NewProvider(oidc.ClientContext(ctx, p.client), p.ep.LinkedInIssuer)
	if err != nil {
		return nil, fmt.Errorf("linkedin discovery: %w", err)
	}
	p.oidc = pr
	return pr, nil
}

func (p *providers) googleProvider(ctx context.Context) (*oidc.Provider, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.google != nil {
		return p.google, nil
	}
	pr, err := oidc.NewProvider(oidc.ClientContext(ctx, p.client), p.ep.GoogleIssuer)
	if err != nil {
		return nil, fmt.Errorf("google discovery: %w", err)
	}
	p.google = pr
	return pr, nil
}

// Enabled reports whether both server-side credentials have been configured.
func (a *Auth) Enabled(provider store.Provider) bool {
	switch provider {
	case store.ProviderLinkedin:
		return configuredCredential(a.cfg.LinkedInClientID) && configuredCredential(a.cfg.LinkedInClientSecret)
	case store.ProviderX:
		return configuredCredential(a.cfg.XClientID) && configuredCredential(a.cfg.XClientSecret)
	case store.ProviderGoogle:
		return configuredCredential(a.cfg.GoogleClientID) && configuredCredential(a.cfg.GoogleClientSecret)
	case store.ProviderGithub:
		return configuredCredential(a.cfg.GitHubClientID) && configuredCredential(a.cfg.GitHubClientSecret)
	case store.ProviderReddit:
		return configuredCredential(a.cfg.RedditClientID) && configuredCredential(a.cfg.RedditClientSecret) &&
			configuredCredential(a.cfg.RedditUserAgent) && !strings.ContainsAny(a.cfg.RedditUserAgent, "\r\n")
	}
	return false
}

func configuredCredential(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.EqualFold(value, "replace-me")
}

// identity is what a provider tells us about the person signing in.
type identity struct {
	Provider    store.Provider
	UID         string
	ProfileURL  string // may be empty (LinkedIn's OIDC claims carry none)
	DisplayName string
}

// oauthConfig builds the OAuth2 config for provider. It needs network only for
// LinkedIn's first discovery.
func (a *Auth) oauthConfig(ctx context.Context, provider store.Provider) (*oauth2.Config, error) {
	if !a.Enabled(provider) {
		return nil, errUnknownProvider
	}
	redirect := a.cfg.BaseURL + "/auth/" + string(provider) + "/callback"
	switch provider {
	case store.ProviderLinkedin:
		pr, err := a.prov.linkedIn(ctx)
		if err != nil {
			return nil, err
		}
		ep := pr.Endpoint()
		ep.AuthStyle = oauth2.AuthStyleInParams
		return &oauth2.Config{
			ClientID: a.cfg.LinkedInClientID, ClientSecret: a.cfg.LinkedInClientSecret,
			Endpoint: ep, RedirectURL: redirect, Scopes: []string{oidc.ScopeOpenID, "profile"},
		}, nil
	case store.ProviderX:
		ep := a.prov.endpoints()
		return &oauth2.Config{
			ClientID: a.cfg.XClientID, ClientSecret: a.cfg.XClientSecret,
			Endpoint:    oauth2.Endpoint{AuthURL: ep.XAuthURL, TokenURL: ep.XTokenURL, AuthStyle: oauth2.AuthStyleInHeader},
			RedirectURL: redirect, Scopes: []string{"users.read", "tweet.read"},
		}, nil
	case store.ProviderGoogle:
		pr, err := a.prov.googleProvider(ctx)
		if err != nil {
			return nil, err
		}
		return &oauth2.Config{
			ClientID: a.cfg.GoogleClientID, ClientSecret: a.cfg.GoogleClientSecret,
			Endpoint: pr.Endpoint(), RedirectURL: redirect, Scopes: []string{oidc.ScopeOpenID, "profile"},
		}, nil
	case store.ProviderGithub:
		ep := a.prov.endpoints()
		return &oauth2.Config{
			ClientID: a.cfg.GitHubClientID, ClientSecret: a.cfg.GitHubClientSecret,
			Endpoint:    oauth2.Endpoint{AuthURL: ep.GitHubAuthURL, TokenURL: ep.GitHubTokenURL, AuthStyle: oauth2.AuthStyleInParams},
			RedirectURL: redirect, Scopes: []string{"read:user"},
		}, nil
	case store.ProviderReddit:
		ep := a.prov.endpoints()
		return &oauth2.Config{
			ClientID: a.cfg.RedditClientID, ClientSecret: a.cfg.RedditClientSecret,
			Endpoint:    oauth2.Endpoint{AuthURL: ep.RedditAuthURL, TokenURL: ep.RedditTokenURL, AuthStyle: oauth2.AuthStyleInHeader},
			RedirectURL: redirect, Scopes: []string{"identity"},
		}, nil
	}
	return nil, errUnknownProvider
}

// fetchIdentity exchanges the code (with the PKCE verifier) and reads the
// provider's user id, profile URL and name. No email is requested.
func (a *Auth) fetchIdentity(ctx context.Context, provider store.Provider, code, verifier string) (identity, error) {
	ctx = a.prov.ctx(ctx)
	if provider == store.ProviderReddit {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 10 * time.Second,
			Transport: redditTransport{base: a.prov.client.Transport, userAgent: a.cfg.RedditUserAgent}})
	}
	conf, err := a.oauthConfig(ctx, provider)
	if err != nil {
		return identity{}, err
	}
	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return identity{}, fmt.Errorf("%s token exchange: %w", provider, err)
	}
	switch provider {
	case store.ProviderLinkedin:
		return a.linkedInIdentity(ctx, tok)
	case store.ProviderX:
		return a.xIdentity(ctx, conf, tok)
	case store.ProviderGoogle:
		return a.googleIdentity(ctx, tok)
	case store.ProviderGithub:
		return a.githubIdentity(ctx, conf, tok)
	case store.ProviderReddit:
		return a.redditIdentity(ctx, conf, tok)
	}
	return identity{}, errUnknownProvider
}

// Reddit requires an identifying User-Agent for both token and identity calls.
type redditTransport struct {
	base      http.RoundTripper
	userAgent string
}

func (t redditTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header.Set("User-Agent", t.userAgent)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(copy)
}

func (a *Auth) googleIdentity(ctx context.Context, tok *oauth2.Token) (identity, error) {
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return identity{}, fmt.Errorf("google: no id_token in token response")
	}
	pr, err := a.prov.googleProvider(ctx)
	if err != nil {
		return identity{}, err
	}
	idt, err := pr.Verifier(&oidc.Config{ClientID: a.cfg.GoogleClientID}).Verify(ctx, raw)
	if err != nil {
		return identity{}, fmt.Errorf("google: verify id_token: %w", err)
	}
	var claims struct {
		Name string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil {
		return identity{}, fmt.Errorf("google: claims: %w", err)
	}
	if idt.Subject == "" {
		return identity{}, fmt.Errorf("google: empty sub")
	}
	return identity{Provider: store.ProviderGoogle, UID: idt.Subject, DisplayName: claims.Name}, nil
}

var githubLogin = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
var redditName = regexp.MustCompile(`^[A-Za-z0-9_-]{3,20}$`)

func (a *Auth) githubIdentity(ctx context.Context, conf *oauth2.Config, tok *oauth2.Token) (identity, error) {
	ep := a.prov.endpoints()
	var user struct {
		ID    json.Number `json:"id"`
		Login string      `json:"login"`
		Name  string      `json:"name"`
	}
	if err := fetchUser(ctx, conf, tok, ep.GitHubUserURL, &user); err != nil {
		return identity{}, fmt.Errorf("github: user: %w", err)
	}
	if n, err := user.ID.Int64(); err != nil || n <= 0 {
		return identity{}, fmt.Errorf("github: invalid id")
	}
	id := identity{Provider: store.ProviderGithub, UID: string(user.ID), DisplayName: user.Name}
	if id.DisplayName == "" {
		id.DisplayName = user.Login
	}
	if githubLogin.MatchString(user.Login) {
		id.ProfileURL = "https://github.com/" + user.Login
	}
	return id, nil
}

func (a *Auth) redditIdentity(ctx context.Context, conf *oauth2.Config, tok *oauth2.Token) (identity, error) {
	ep := a.prov.endpoints()
	var user struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := fetchUser(ctx, conf, tok, ep.RedditUserURL, &user); err != nil {
		return identity{}, fmt.Errorf("reddit: me: %w", err)
	}
	if user.ID == "" {
		return identity{}, fmt.Errorf("reddit: empty id")
	}
	id := identity{Provider: store.ProviderReddit, UID: user.ID, DisplayName: user.Name}
	if redditName.MatchString(user.Name) {
		id.ProfileURL = "https://www.reddit.com/user/" + user.Name
	}
	return id, nil
}

func fetchUser(ctx context.Context, conf *oauth2.Config, tok *oauth2.Token, endpoint string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := conf.Client(ctx, tok).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(target); err != nil {
		return err
	}
	return nil
}

func (a *Auth) linkedInIdentity(ctx context.Context, tok *oauth2.Token) (identity, error) {
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return identity{}, fmt.Errorf("linkedin: no id_token in token response")
	}
	pr, err := a.prov.linkedIn(ctx)
	if err != nil {
		return identity{}, err
	}
	idt, err := pr.Verifier(&oidc.Config{ClientID: a.cfg.LinkedInClientID}).Verify(ctx, raw)
	if err != nil {
		return identity{}, fmt.Errorf("linkedin: verify id_token: %w", err)
	}
	var claims struct {
		Name       string `json:"name"`
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
		Profile    string `json:"profile"`
	}
	if err := idt.Claims(&claims); err != nil {
		return identity{}, fmt.Errorf("linkedin: claims: %w", err)
	}
	name := claims.Name
	if name == "" {
		name = strings.TrimSpace(claims.GivenName + " " + claims.FamilyName)
	}
	id := identity{Provider: store.ProviderLinkedin, UID: idt.Subject, DisplayName: name}
	if strings.HasPrefix(claims.Profile, "https://") {
		id.ProfileURL = claims.Profile
	}
	if id.UID == "" {
		return identity{}, fmt.Errorf("linkedin: empty sub")
	}
	return id, nil
}

func (a *Auth) xIdentity(ctx context.Context, conf *oauth2.Config, tok *oauth2.Token) (identity, error) {
	ep := a.prov.endpoints()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.XUserURL, nil)
	if err != nil {
		return identity{}, err
	}
	resp, err := conf.Client(ctx, tok).Do(req)
	if err != nil {
		return identity{}, fmt.Errorf("x: users/me: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return identity{}, fmt.Errorf("x: users/me: status %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return identity{}, fmt.Errorf("x: users/me: %w", err)
	}
	if body.Data.ID == "" {
		return identity{}, fmt.Errorf("x: users/me: empty id")
	}
	id := identity{Provider: store.ProviderX, UID: body.Data.ID, DisplayName: body.Data.Name}
	if validXUsername(body.Data.Username) {
		id.ProfileURL = ep.XProfileBase + body.Data.Username
	}
	return id, nil
}

func validXUsername(s string) bool {
	if s == "" || len(s) > 50 {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
