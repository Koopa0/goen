// Package google is Google's OAuth and OpenID Connect sign-in client; it holds
// the authorize URL, the code exchange and the identity it returns; it does not
// hold goen's sessions or its state cookie, which are package auth.
package google

import (
	"context"
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

	"github.com/koopa0/goen/internal/bearer"
	"github.com/koopa0/goen/internal/web"
)

const (
	authURL = "https://accounts.google.com/o/oauth2/v2/auth"
	//nolint:gosec // G101: a published endpoint URL that happens to contain the
	// word "token", not a credential.
	tokenURL    = "https://oauth2.googleapis.com/token"
	userInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
)

var errDisabled = errors.New("google: sign-in is not configured")

type Client struct {
	clientID     string
	clientSecret string
	redirectURL  string
	http         *http.Client
}

func New(clientID, clientSecret, baseURL string) (*Client, error) {
	if clientID == "" && clientSecret == "" {
		return &Client{}, nil
	}
	if clientID == "" || clientSecret == "" {
		return nil, errors.New("google: sign-in needs both a client id and a " +
			"client secret; one without the other cannot exchange an authorisation code")
	}
	origin, _, ok := web.SiteOrigin(baseURL)
	if !ok {
		return nil, fmt.Errorf("google: base URL %q cannot build a redirect URI", baseURL)
	}
	return &Client{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  origin + "/auth/google/callback",
		http:         &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *Client) Enabled() bool { return c != nil && c.clientID != "" }

func (c *Client) AuthorizeURL() (target string, state State, err error) {
	if !c.Enabled() {
		return "", State{}, errDisabled
	}
	raw := bearer.New()
	verifier := bearer.New()
	sum := sha256.Sum256([]byte(verifier))

	q := url.Values{
		"client_id":             {c.clientID},
		"redirect_uri":          {c.redirectURL},
		"response_type":         {"code"},
		"scope":                 {"openid email profile"},
		"state":                 {raw},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"prompt":                {"select_account"},
	}
	return authURL + "?" + q.Encode(),
		State{Value: raw, Verifier: verifier}, nil
}

// State is what one sign-in must keep until Google sends the browser back: the
// state the callback must echo and the PKCE verifier the exchange presents.
type State struct {
	Value    string
	Verifier string
}

type Identity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

func (c *Client) Exchange(ctx context.Context, code, verifier string) (Identity, error) {
	if !c.Enabled() {
		return Identity{}, errDisabled
	}
	token, err := c.token(ctx, code, verifier)
	if err != nil {
		return Identity{}, err
	}
	return c.userInfo(ctx, token)
}

func (c *Client) token(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"redirect_uri":  {c.redirectURL},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
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
			resp.StatusCode, oauthErrorCode(body))
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

// maxOAuthErrorCode bounds the one field of a refusal goen keeps; the longest
// code RFC 6749 defines is 22 bytes.
const maxOAuthErrorCode = 40

// oauthErrorCode drops the rest of the body before anything logs it: the
// description and whatever a proxy adds are free text and can carry the
// request's own code or the client's details.
func oauthErrorCode(body []byte) string {
	var refusal struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &refusal) != nil ||
		refusal.Error == "" || len(refusal.Error) > maxOAuthErrorCode {
		return "unrecognised"
	}
	for _, c := range refusal.Error {
		if (c < 'a' || c > 'z') && c != '_' {
			return "unrecognised"
		}
	}
	return refusal.Error
}

func (c *Client) userInfo(ctx context.Context, token string) (Identity, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, http.NoBody)
	if err != nil {
		return Identity{}, fmt.Errorf("build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
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
