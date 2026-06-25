package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"
)

// oauthClientID is the Google "installed app" (Desktop) OAuth client ID for
// Colab access. The client *ID* is public; the matching client *secret* is not
// committed — it is read from clientSecretEnv (see .env.example).
const (
	oauthClientID = "1014160490159-cvot3bea7tgkp72a4m29h20d9ddo6bne.apps.googleusercontent.com"

	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"

	// clientSecretEnv names the env var holding the Google OAuth client secret.
	clientSecretEnv = "LFLOW_COLAB_OAUTH_SECRET"
)

// oauthClientSecret resolves the Google OAuth client secret from the
// environment, falling back to a .env file in the working dir or an ancestor.
// For a Desktop OAuth client this value is not confidential (PKCE secures the
// flow), but it is kept out of source control.
func oauthClientSecret() (string, error) {
	if v := strings.TrimSpace(os.Getenv(clientSecretEnv)); v != "" {
		return v, nil
	}
	loadDotEnvOnce()
	if v := strings.TrimSpace(os.Getenv(clientSecretEnv)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s is not set — add it to your environment or a .env file (see .env.example)", clientSecretEnv)
}

var dotEnvOnce sync.Once

// loadDotEnvOnce loads KEY=VALUE pairs from the nearest .env file (cwd or an
// ancestor) into the process environment, without overriding existing vars.
func loadDotEnvOnce() {
	dotEnvOnce.Do(func() {
		dir, err := os.Getwd()
		if err != nil {
			return
		}
		for i := 0; i < 6; i++ {
			if applyDotEnv(filepath.Join(dir, ".env")) {
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return
			}
			dir = parent
		}
	})
}

func applyDotEnv(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return true
}

var oauthScopes = []string{
	"profile",
	"email",
	"https://www.googleapis.com/auth/colaboratory",
}

// refreshBuffer is how long before expiry we proactively refresh the token.
const refreshBuffer = 2 * time.Minute

// Token is a Google OAuth token. It is the value persisted (as JSON) by the
// caller's TokenStore — its layout mirrors golang.org/x/oauth2.Token so the
// stored blob stays compatible with the compute CLI's token.json.
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

// Valid reports whether the token has a non-expired access token.
func (t *Token) Valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	if t.Expiry.IsZero() {
		return true
	}
	return time.Until(t.Expiry) > refreshBuffer
}

// TokenStore loads and persists the OAuth token. The runtime package stays
// storage-agnostic; lflow provides a DB-backed implementation over the
// `system` table.
type TokenStore interface {
	Load(ctx context.Context) (*Token, error)
	Save(ctx context.Context, t *Token) error
}

// tokenResponse is the JSON returned by Google's token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func (r tokenResponse) toToken(prev *Token) *Token {
	tok := &Token{
		AccessToken:  r.AccessToken,
		TokenType:    r.TokenType,
		RefreshToken: r.RefreshToken,
	}
	if r.ExpiresIn > 0 {
		tok.Expiry = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	// Google omits the refresh token on refresh; carry the previous one over.
	if tok.RefreshToken == "" && prev != nil {
		tok.RefreshToken = prev.RefreshToken
	}
	return tok
}

// Login runs the browser-based OAuth2 loopback (PKCE) flow and returns a fresh
// token. It blocks until the user completes consent in the browser or ctx is
// canceled.
func Login(ctx context.Context) (*Token, error) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, err
	}
	state, err := randomURLString(16)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start loopback listener: %w", err)
	}
	defer listener.Close()
	redirectURI := fmt.Sprintf("http://%s/callback", listener.Addr().String())

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			http.Error(w, "authorization failed: "+e, http.StatusBadRequest)
			errCh <- fmt.Errorf("authorization error: %s", e)
			return
		}
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- fmt.Errorf("oauth state mismatch")
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("no authorization code in callback")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><body>Authorization complete. You may close this tab and return to the terminal.</body></html>")
		codeCh <- code
	})}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	authURL := buildAuthURL(redirectURI, state, challenge)
	openBrowser(authURL)

	var code string
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		return nil, err
	case code = <-codeCh:
	}

	return exchangeCode(ctx, code, verifier, redirectURI)
}

func buildAuthURL(redirectURI, state, challenge string) string {
	q := url.Values{}
	q.Set("client_id", oauthClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(oauthScopes, " "))
	q.Set("state", state)
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	q.Set("code_challenge_method", "S256")
	q.Set("code_challenge", challenge)
	return googleAuthURL + "?" + q.Encode()
}

func exchangeCode(ctx context.Context, code, verifier, redirectURI string) (*Token, error) {
	secret, err := oauthClientSecret()
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", oauthClientID)
	form.Set("client_secret", secret)
	form.Set("code_verifier", verifier)

	resp, err := postToken(ctx, form)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}
	return resp.toToken(nil), nil
}

// refreshToken exchanges a refresh token for a new access token.
func refreshToken(ctx context.Context, prev *Token) (*Token, error) {
	if prev == nil || prev.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available (re-run: lflow auth colab)")
	}
	secret, err := oauthClientSecret()
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", prev.RefreshToken)
	form.Set("client_id", oauthClientID)
	form.Set("client_secret", secret)

	resp, err := postToken(ctx, form)
	if err != nil {
		return nil, fmt.Errorf("refresh token (re-run: lflow auth colab): %w", err)
	}
	return resp.toToken(prev), nil
}

func postToken(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(httpResp.Body)

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("parse token response (status %d): %w", httpResp.StatusCode, err)
	}
	if tr.Error != "" {
		return nil, fmt.Errorf("token endpoint error: %s: %s", tr.Error, tr.ErrorDesc)
	}
	if httpResp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		return nil, fmt.Errorf("token request failed (status %d): %s", httpResp.StatusCode, strings.TrimSpace(string(body)))
	}
	return &tr, nil
}

// tokenSourceFromStore returns an AccessTokenProvider that loads the token from
// the store, refreshes it when near expiry, and writes refreshed tokens back.
func tokenSourceFromStore(store TokenStore) accessTokenProvider {
	return func(ctx context.Context) (string, error) {
		tok, err := store.Load(ctx)
		if err != nil {
			return "", err
		}
		if tok == nil {
			return "", fmt.Errorf("not authenticated. Run: lflow auth colab")
		}
		if tok.Valid() {
			return tok.AccessToken, nil
		}
		fresh, err := refreshToken(ctx, tok)
		if err != nil {
			return "", err
		}
		if err := store.Save(ctx, fresh); err != nil {
			return "", fmt.Errorf("persist refreshed token: %w", err)
		}
		return fresh.AccessToken, nil
	}
}

func generatePKCE() (verifier, challenge string, err error) {
	verifier, err = randomURLString(32)
	if err != nil {
		return "", "", fmt.Errorf("generate PKCE verifier: %w", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomURLString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// openBrowser best-effort opens url in the user's default browser.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch goruntime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}
