package account

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	googleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	//nolint:gosec // G101: a published endpoint URL that happens to contain the
	// word "token", not a credential.
	googleTokenURL    = "https://oauth2.googleapis.com/token"
	googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
)

var (
	// ErrOAuthDisabled is a deployment with no Google credentials.
	ErrOAuthDisabled = errors.New("account: google sign-in is not configured")
	// ErrOAuthState is a callback whose state does not match the cookie.
	ErrOAuthState = errors.New("account: the sign-in request does not match this browser")
	// ErrOAuthUnverified is a Google account whose address Google has not proved.
	ErrOAuthUnverified = errors.New("account: google has not verified that address")
	// ErrOAuthCollision is an address that already has an unproved goen account.
	ErrOAuthCollision = errors.New("account: that address already has an unverified account here")
)

// oauthStateCookie carries the CSRF state and the PKCE verifier to the callback.
const oauthStateCookie = "__Host-goen_oauth"

const oauthStateTTL = 10 * time.Minute

// Google is the OAuth client; the zero value is disabled.
type Google struct {
	clientID     string
	clientSecret string
	redirectURL  string
	http         *http.Client
}

// NewGoogle returns a client for the given credentials: both, or neither.
func NewGoogle(clientID, clientSecret, baseURL string) (*Google, error) {
	if clientID == "" && clientSecret == "" {
		return &Google{}, nil
	}
	if clientID == "" || clientSecret == "" {
		return nil, errors.New("account: google sign-in needs both a client id and a " +
			"client secret; one without the other cannot exchange an authorisation code")
	}
	if !strings.HasPrefix(baseURL, "http") {
		return nil, fmt.Errorf("account: base URL %q cannot build a redirect URI", baseURL)
	}
	return &Google{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  strings.TrimSuffix(baseURL, "/") + "/auth/google/callback",
		http:         &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Enabled reports whether this deployment offers Google sign-in.
func (g *Google) Enabled() bool { return g != nil && g.clientID != "" }

// AuthorizeURL returns where to send the browser and the state to remember.
func (g *Google) AuthorizeURL(next string) (target string, state OAuthState, err error) {
	if !g.Enabled() {
		return "", OAuthState{}, ErrOAuthDisabled
	}
	raw, err := randomToken()
	if err != nil {
		return "", OAuthState{}, err
	}
	verifier, err := randomToken()
	if err != nil {
		return "", OAuthState{}, err
	}
	sum := sha256.Sum256([]byte(verifier))

	q := url.Values{
		"client_id":             {g.clientID},
		"redirect_uri":          {g.redirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {raw},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return googleAuthURL + "?" + q.Encode(),
		OAuthState{Value: raw, Verifier: verifier, Next: next}, nil
}

// OAuthState is what the browser has to carry across the redirect.
type OAuthState struct {
	Value    string
	Verifier string
	Next     string
}

// Identity is who Google says this is.
type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// Exchange turns the callback's code into an identity.
func (g *Google) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	if !g.Enabled() {
		return Identity{}, ErrOAuthDisabled
	}
	token, err := g.token(ctx, code, verifier)
	if err != nil {
		return Identity{}, err
	}
	return g.userInfo(ctx, token)
}

func (g *Google) token(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"redirect_uri":  {g.redirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange the authorisation code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read the token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google refused the code exchange: %d %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode the token response: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("google returned no access token")
	}
	return out.AccessToken, nil
}

func (g *Google) userInfo(ctx context.Context, token string) (Identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoURL, http.NoBody)
	if err != nil {
		return Identity{}, fmt.Errorf("build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := g.http.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("read the google profile: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("read the profile response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("google refused the profile request: %d", resp.StatusCode)
	}
	var out struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Identity{}, fmt.Errorf("decode the profile: %w", err)
	}
	if out.Sub == "" {
		return Identity{}, errors.New("google returned a profile with no subject")
	}
	return Identity{
		Subject: out.Sub, Email: out.Email,
		EmailVerified: out.EmailVerified, Name: out.Name,
	}, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
