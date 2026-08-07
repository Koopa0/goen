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

// Google's OAuth 2.0 endpoints.
//
// Constants rather than discovery: the document at
// accounts.google.com/.well-known/openid-configuration has named these three
// URLs for a decade, and fetching it at startup would make goen fail to boot
// because somebody else's CDN was slow.
const (
	googleAuthURL = "https://accounts.google.com/o/oauth2/v2/auth"
	//nolint:gosec // G101: a published endpoint URL that happens to contain the
	// word "token", not a credential.
	googleTokenURL    = "https://oauth2.googleapis.com/token"
	googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
)

// Errors the sign-in handler branches on.
var (
	// ErrOAuthDisabled is a deployment with no Google credentials. The site
	// still signs people in with a password; only the button is absent.
	ErrOAuthDisabled = errors.New("account: google sign-in is not configured")
	// ErrOAuthState is a callback whose state does not match the cookie —
	// a forged or stale callback, and never something to act on.
	ErrOAuthState = errors.New("account: the sign-in request does not match this browser")
	// ErrOAuthUnverified is a Google account whose address Google itself has
	// not proved. Trusting it would let anybody who controls a Workspace domain
	// claim any address in it.
	ErrOAuthUnverified = errors.New("account: google has not verified that address")
	// ErrOAuthCollision is an address that already has a goen account which has
	// not proved the address. See linkOrCreate for why that cannot be linked.
	ErrOAuthCollision = errors.New("account: that address already has an unverified account here")
)

// oauthStateCookie carries the CSRF state and the PKCE verifier across the
// redirect to Google and back.
//
// __Host-, so only this exact origin can set it. That is what makes the
// state comparison meaningful: an attacker who could write this cookie could
// complete their OWN authorisation in the victim's browser and sign the victim
// into the attacker's account — a login CSRF that ends with the victim's next
// order landing in a stranger's history.
const oauthStateCookie = "__Host-goen_oauth"

// oauthStateTTL bounds how long an unfinished sign-in stays valid.
//
// Long enough to read a consent screen and pick an account, short enough that a
// state left in a shared browser is not a standing invitation.
const oauthStateTTL = 10 * time.Minute

// Google is the OAuth client. The zero value is DISABLED and answers
// ErrOAuthDisabled, which is the shape payment.Gateway and invoice.Gateway
// already have for absent credentials.
type Google struct {
	clientID     string
	clientSecret string
	redirectURL  string
	http         *http.Client
}

// NewGoogle returns a client for the given credentials.
//
// Both or neither. A client id without its secret cannot exchange a code, so a
// deployment that set one and forgot the other would offer a sign-in button that
// fails after the customer has already been to Google and consented — which
// looks like goen losing their account rather than a configuration mistake.
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
		// Registered with Google and compared by them on every exchange, which
		// is what stops an authorisation code being redirected somewhere else.
		redirectURL: strings.TrimSuffix(baseURL, "/") + "/auth/google/callback",
		http:        &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Enabled reports whether this deployment offers Google sign-in.
func (g *Google) Enabled() bool { return g != nil && g.clientID != "" }

// AuthorizeURL starts the flow: it returns where to send the browser and the
// state to remember.
//
// PKCE even though goen is a CONFIDENTIAL client with a secret. The secret
// protects the exchange; the verifier protects the CODE, which travels through
// the browser's address bar, the referrer of any resource the callback page
// loads, and every proxy log between. Without it a code lifted from any of those
// is redeemable by anybody who also has the secret — and secrets leak.
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
		// Google returns to the consent screen rather than silently reusing a
		// previous grant, so somebody on a shared machine can pick an account.
		"prompt": {"select_account"},
	}
	return googleAuthURL + "?" + q.Encode(),
		OAuthState{Value: raw, Verifier: verifier, Next: next}, nil
}

// OAuthState is what the browser has to carry across the redirect.
type OAuthState struct {
	Value    string
	Verifier string
	// Next is where to go once signed in. Validated as a same-site PATH when it
	// is read back, never trusted from the cookie — an open redirect on a
	// sign-in flow is how a phishing page borrows a real login.
	Next string
}

// Identity is who Google says this is.
type Identity struct {
	// Subject is Google's own stable id for the account. The EMAIL is not: a
	// Google account can change address, and a released Workspace address can be
	// reassigned to a different person. user_identities keys on this.
	Subject string
	Email   string
	// EmailVerified is Google's own claim about the address. False for some
	// Workspace configurations, and trusting it there would let a domain
	// administrator claim any address in their domain.
	EmailVerified bool
	Name          string
}

// Exchange turns the callback's code into an identity.
//
// Two calls to Google over TLS, and no ID token is parsed. The token endpoint
// answers this server directly, so the connection itself is what authenticates
// the response — a signature check on top of it would verify the same claim
// twice, and it would need a JWKS cache, a key-rotation policy and a JWT
// library, each of which is a thing to get wrong.
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

// token redeems the authorisation code.
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
		// Google's body names the reason — invalid_grant for a replayed code,
		// redirect_uri_mismatch for a misconfiguration. It is logged and never
		// shown: a customer cannot act on either.
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

// userInfo asks who the token belongs to.
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

// randomToken is a URL-safe 32-byte secret.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
