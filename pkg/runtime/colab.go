package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Colab API overview (reverse-engineered from the official colab-vscode extension):
//  1. OAuth2 auth -> Google access token
//  2. GET  /tun/m/assign -> XSRF token (or existing assignment)
//  3. POST /tun/m/assign with X-Goog-Colab-Token -> runtime endpoint + proxy token
//  4. Open Jupyter session via the runtime proxy URL (POST /api/sessions)
//  5. Execute code over a Jupyter WebSocket (wire protocol v5.3)
//  6. Keep-alive every 60s to prevent the ~90min idle timeout
//  7. Release via GET+POST /tun/m/unassign (same XSRF pattern)

const (
	colabBackendURL             = "https://colab.research.google.com"
	colabGAPIURL                = "https://colab.pa.googleapis.com"
	keepAliveInterval           = 60 * time.Second
	proxyTokenRefreshBuffer     = 5 * time.Minute
	proxyTokenRefreshRetry      = 30 * time.Second
	defaultProxyTokenTTLSeconds = 3600

	// clientAgent must be "vscode" — Colab's backend validates this header and
	// rejects unknown user-agents.
	clientAgent = "vscode"
)

// Runtime holds Colab runtime assignment info plus its keep-alive lifecycle.
type Runtime struct {
	Endpoint    string `json:"endpoint"`
	ProxyToken  string `json:"proxyToken"`
	ProxyURL    string `json:"proxyUrl"`
	Accelerator string `json:"accelerator"`

	ProxyTokenExpiresAt time.Time `json:"proxyTokenExpiresAt,omitempty"`

	mu     sync.RWMutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (rt *Runtime) stopKeepAlive() {
	if rt.cancel != nil {
		rt.cancel()
	}
}

// ConnectionInfo returns the current proxy URL and proxy token.
func (rt *Runtime) ConnectionInfo() (string, string) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.ProxyURL, rt.ProxyToken
}

func (rt *Runtime) proxyTokenRefreshDelay(buffer time.Duration) time.Duration {
	rt.mu.RLock()
	expiresAt := rt.ProxyTokenExpiresAt
	rt.mu.RUnlock()
	if expiresAt.IsZero() {
		return 100 * time.Millisecond
	}
	delay := time.Until(expiresAt) - buffer
	if delay < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	return delay
}

func (rt *Runtime) updateProxyInfo(token, proxyURL string, expiresInSecs int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if token != "" {
		rt.ProxyToken = token
	}
	if proxyURL != "" {
		rt.ProxyURL = proxyURL
	}
	if expiresInSecs <= 0 {
		expiresInSecs = defaultProxyTokenTTLSeconds
	}
	rt.ProxyTokenExpiresAt = time.Now().Add(time.Duration(expiresInSecs) * time.Second)
}

// accessTokenProvider returns a fresh Google OAuth access token.
type accessTokenProvider func(context.Context) (string, error)

// ColabClient interacts with the Colab backend API.
type ColabClient struct {
	tokenProvider accessTokenProvider
	authUser      string
	httpClient    *http.Client
}

// NewClient creates a Colab client that sources (and refreshes) its OAuth token
// from store.
func NewClient(store TokenStore) *ColabClient {
	return &ColabClient{
		tokenProvider: tokenSourceFromStore(store),
		authUser:      "0",
		httpClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *ColabClient) withAuthUser(rawURL string) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "authuser=" + url.QueryEscape(c.authUser)
}

func (c *ColabClient) setAuthHeaders(ctx context.Context, req *http.Request) error {
	token, err := c.tokenProvider(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Colab-Client-Agent", clientAgent)
	req.Header.Set("Accept", "application/json")
	return nil
}

func (c *ColabClient) colabRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if err := c.setAuthHeaders(ctx, req); err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

// outcomeError maps Colab assignment outcome codes to user-friendly errors.
func outcomeError(code int) error {
	switch code {
	case 1:
		return fmt.Errorf("GPU quota denied — try again later or upgrade to Colab Pro")
	case 2:
		return fmt.Errorf("GPU quota exceeded — you've used too much GPU time recently")
	case 5:
		return fmt.Errorf("account denylisted from GPU access")
	}
	return nil
}

// stripXSSI removes the XSSI prefix ")]}'\n" that Colab prepends to JSON responses.
func stripXSSI(data []byte) []byte {
	prefix := []byte(")]}'\n")
	if len(data) > len(prefix) && string(data[:len(prefix)]) == string(prefix) {
		return data[len(prefix):]
	}
	return data
}

// uuidToNbHash converts a UUID to Colab's notebook hash (nbh) format.
func uuidToNbHash(u string) string {
	s := strings.ReplaceAll(u, "-", "_")
	for len(s) < 44 {
		s += "."
	}
	return s
}

type runtimeReusePolicy int

const (
	reuseMatchingRuntime runtimeReusePolicy = iota
	replaceMismatchedRuntime
	alwaysAssignNewRuntime
)

// EnsureRuntime requests a runtime using an explicit reuse/replacement policy.
func (c *ColabClient) EnsureRuntime(ctx context.Context, gpu string, cpu bool, policy runtimeReusePolicy) (*Runtime, error) {
	if policy == alwaysAssignNewRuntime {
		return c.assignNewRuntime(ctx, gpu, cpu)
	}

	if assignments, err := c.ListAssignments(ctx); err == nil && len(assignments) > 0 {
		a := assignments[0]
		if a.Endpoint != "" && a.RuntimeProxyInfo.Token != "" {
			requestedAccelerator := requestedAccelerator(gpu, cpu)
			existingAccelerator := strings.ToUpper(a.Accelerator)
			if requestedAccelerator == existingAccelerator {
				return c.runtimeFromAssignment(ctx, a)
			}
			if policy == replaceMismatchedRuntime {
				oldRT := &Runtime{Endpoint: a.Endpoint}
				_ = c.UnassignRuntime(ctx, oldRT)
			}
		}
	}

	return c.assignNewRuntime(ctx, gpu, cpu)
}

// runtimeFromAssignment builds a Runtime from an existing assignment and starts
// keep-alive + proxy-token refresh loops bound to ctx.
func (c *ColabClient) runtimeFromAssignment(ctx context.Context, a assignPostResponse) (*Runtime, error) {
	proxyURL := a.RuntimeProxyInfo.URL
	if proxyURL != "" {
		validatedURL, err := validateRuntimeProxyURL(proxyURL)
		if err != nil {
			logRuntimeProxyValidationFailure(proxyURL, err)
			return nil, fmt.Errorf("invalid runtime proxy URL: %w", err)
		}
		proxyURL = validatedURL
	}

	rt := &Runtime{
		Endpoint:    a.Endpoint,
		ProxyToken:  a.RuntimeProxyInfo.Token,
		ProxyURL:    proxyURL,
		Accelerator: a.Accelerator,
	}
	rt.updateProxyInfo(a.RuntimeProxyInfo.Token, proxyURL, a.RuntimeProxyInfo.ExpiresInSeconds())

	kaCtx, cancel := context.WithCancel(ctx)
	rt.cancel = cancel
	rt.wg.Add(2)
	go c.keepAlive(kaCtx, rt)
	go c.refreshProxyTokenLoop(kaCtx, rt)
	return rt, nil
}

func requestedAccelerator(gpu string, cpu bool) string {
	if cpu {
		return "NONE"
	}
	return strings.ToUpper(gpu)
}

func assignRuntimeParams(nbHash, gpu string, cpu bool) string {
	if cpu {
		return fmt.Sprintf("?nbh=%s", nbHash)
	}
	return fmt.Sprintf("?nbh=%s&variant=GPU&accelerator=%s", nbHash, strings.ToUpper(gpu))
}

// assignNewRuntime creates a fresh Colab runtime via the assign API.
func (c *ColabClient) assignNewRuntime(ctx context.Context, gpu string, cpu bool) (*Runtime, error) {
	nbHash := uuidToNbHash(uuid.New().String())

	params := assignRuntimeParams(nbHash, gpu, cpu)
	assignURL := c.withAuthUser(colabBackendURL + "/tun/m/assign" + params)

	// Step 1: GET to obtain XSRF token (or existing assignment)
	resp, err := c.colabRequest(ctx, "GET", assignURL, nil)
	if err != nil {
		return nil, fmt.Errorf("XSRF GET: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("XSRF GET failed (status %d): %s", resp.StatusCode, stripXSSI(body))
	}

	cleaned := stripXSSI(body)

	// Try parsing as an existing assignment first.
	var existingAssignment assignPostResponse
	if err := json.Unmarshal(cleaned, &existingAssignment); err == nil && existingAssignment.RuntimeProxyInfo.Token != "" {
		return c.runtimeFromAssignment(ctx, existingAssignment)
	}

	var xsrfResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(cleaned, &xsrfResp); err != nil {
		return nil, fmt.Errorf("parse XSRF response: %w (body: %s)", err, cleaned)
	}
	if xsrfResp.Token == "" {
		return nil, fmt.Errorf("no XSRF token in response: %s", cleaned)
	}

	// Step 2: POST with XSRF token
	req, err := http.NewRequestWithContext(ctx, "POST", assignURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create assign request: %w", err)
	}
	if err := c.setAuthHeaders(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("X-Goog-Colab-Token", xsrfResp.Token)

	resp, err = c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("assign POST: %w", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		cleaned = stripXSSI(body)
		var errResp struct {
			Outcome int `json:"outcome"`
		}
		if json.Unmarshal(cleaned, &errResp) == nil {
			if err := outcomeError(errResp.Outcome); err != nil {
				return nil, err
			}
		}
		return nil, fmt.Errorf("assign failed (status %d): %s", resp.StatusCode, cleaned)
	}

	var assignment assignPostResponse
	if err := json.Unmarshal(stripXSSI(body), &assignment); err != nil {
		return nil, fmt.Errorf("parse assignment: %w", err)
	}
	if err := outcomeError(assignment.Outcome); err != nil {
		return nil, err
	}
	if assignment.Endpoint == "" {
		return nil, fmt.Errorf("no endpoint in assignment response: %s", stripXSSI(body))
	}

	return c.runtimeFromAssignment(ctx, assignment)
}

type assignPostResponse struct {
	Endpoint         string           `json:"endpoint"`
	Accelerator      string           `json:"accelerator"`
	Outcome          int              `json:"outcome"`
	RuntimeProxyInfo runtimeProxyInfo `json:"runtimeProxyInfo"`
}

type runtimeProxyInfo struct {
	Token              string `json:"token"`
	TokenExpiresInSecs int    `json:"tokenExpiresInSeconds"`
	TokenTTL           string `json:"tokenTtl"`
	URL                string `json:"url"`
}

func (r runtimeProxyInfo) ExpiresInSeconds() int {
	if r.TokenExpiresInSecs > 0 {
		return r.TokenExpiresInSecs
	}
	secs, err := parseTokenTTLSeconds(r.TokenTTL)
	if err == nil && secs > 0 {
		return secs
	}
	return defaultProxyTokenTTLSeconds
}

// SendKeepAlive sends a single keep-alive request for a runtime.
func (c *ColabClient) SendKeepAlive(ctx context.Context, rt *Runtime) error {
	_, proxyToken := rt.ConnectionInfo()
	kaURL := c.withAuthUser(colabBackendURL + "/tun/m/" + rt.Endpoint + "/keep-alive/")
	req, err := http.NewRequestWithContext(ctx, "GET", kaURL, nil)
	if err != nil {
		return err
	}
	if err := c.setAuthHeaders(ctx, req); err != nil {
		return err
	}
	req.Header.Set("X-Colab-Tunnel", "Google")
	if proxyToken != "" {
		req.Header.Set("X-Colab-Runtime-Proxy-Token", proxyToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("keep-alive failed (status %d): %s", resp.StatusCode, stripXSSI(body))
	}
	return nil
}

func (c *ColabClient) keepAlive(ctx context.Context, rt *Runtime) {
	defer rt.wg.Done()
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = c.SendKeepAlive(ctx, rt)
		}
	}
}

func (c *ColabClient) refreshProxyTokenLoop(ctx context.Context, rt *Runtime) {
	defer rt.wg.Done()
	for {
		timer := time.NewTimer(rt.proxyTokenRefreshDelay(proxyTokenRefreshBuffer))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if err := c.RefreshProxyToken(ctx, rt); err != nil {
			retry := time.NewTimer(proxyTokenRefreshRetry)
			select {
			case <-ctx.Done():
				retry.Stop()
				return
			case <-retry.C:
			}
		}
	}
}

// UnassignRuntime releases the Colab runtime (XSRF GET+POST pattern) and stops
// its keep-alive loops.
func (c *ColabClient) UnassignRuntime(ctx context.Context, rt *Runtime) error {
	if rt.cancel != nil {
		rt.cancel()
		rt.wg.Wait()
	}

	unassignURL := c.withAuthUser(colabBackendURL + "/tun/m/unassign/" + rt.Endpoint)

	resp, err := c.colabRequest(ctx, "GET", unassignURL, nil)
	if err != nil {
		return fmt.Errorf("unassign XSRF GET: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unassign XSRF GET failed (status %d): %s", resp.StatusCode, stripXSSI(body))
	}

	var xsrfResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(stripXSSI(body), &xsrfResp); err != nil {
		return fmt.Errorf("parse unassign XSRF: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", unassignURL, nil)
	if err != nil {
		return fmt.Errorf("create unassign request: %w", err)
	}
	if err := c.setAuthHeaders(ctx, req); err != nil {
		return fmt.Errorf("set unassign auth headers: %w", err)
	}
	if xsrfResp.Token != "" {
		req.Header.Set("X-Goog-Colab-Token", xsrfResp.Token)
	}

	resp, err = c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("unassign POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unassign failed (status %d): %s", resp.StatusCode, stripXSSI(body))
	}

	return nil
}

// ListAssignments returns currently assigned runtimes.
func (c *ColabClient) ListAssignments(ctx context.Context) ([]assignPostResponse, error) {
	u := c.withAuthUser(colabBackendURL + "/tun/m/assignments")
	resp, err := c.colabRequest(ctx, "GET", u, nil)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list assignments failed (status %d): %s", resp.StatusCode, stripXSSI(body))
	}

	cleaned := stripXSSI(body)

	var assignments []assignPostResponse
	if err := json.Unmarshal(cleaned, &assignments); err != nil {
		var wrapper map[string]json.RawMessage
		if err2 := json.Unmarshal(cleaned, &wrapper); err2 != nil {
			return nil, fmt.Errorf("parse assignments: %w (body: %s)", err, cleaned)
		}
		for _, key := range []string{"assignments", "servers", "items"} {
			if raw, ok := wrapper[key]; ok {
				if json.Unmarshal(raw, &assignments) == nil {
					return assignments, nil
				}
			}
		}
		var single assignPostResponse
		if json.Unmarshal(cleaned, &single) == nil && single.Endpoint != "" {
			return []assignPostResponse{single}, nil
		}
		return nil, fmt.Errorf("parse assignments: unexpected format: %s", cleaned)
	}

	return assignments, nil
}

// RefreshProxyToken refreshes the runtime proxy token via the GAPI endpoint.
func (c *ColabClient) RefreshProxyToken(ctx context.Context, rt *Runtime) error {
	proxyTokenURL := fmt.Sprintf("%s/v1/runtime-proxy-token?endpoint=%s&port=8080", colabGAPIURL, url.QueryEscape(rt.Endpoint))
	resp, err := c.colabRequest(ctx, "GET", proxyTokenURL, nil)
	if err != nil {
		return fmt.Errorf("refresh proxy token: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh proxy token failed (status %d): %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		Token    string `json:"token"`
		TokenTTL string `json:"tokenTtl"`
		URL      string `json:"url"`
	}
	if err := json.Unmarshal(stripXSSI(body), &tokenResp); err != nil {
		return fmt.Errorf("parse proxy token: %w", err)
	}

	proxyURL := ""
	if tokenResp.URL != "" {
		validatedURL, err := validateRuntimeProxyURL(tokenResp.URL)
		if err != nil {
			logRuntimeProxyValidationFailure(tokenResp.URL, err)
			return fmt.Errorf("invalid runtime proxy URL: %w", err)
		}
		proxyURL = validatedURL
	}
	expiresIn, err := parseTokenTTLSeconds(tokenResp.TokenTTL)
	if err != nil {
		expiresIn = defaultProxyTokenTTLSeconds
	}
	rt.updateProxyInfo(tokenResp.Token, proxyURL, expiresIn)

	return nil
}

func parseTokenTTLSeconds(tokenTTL string) (int, error) {
	if tokenTTL == "" {
		return 0, fmt.Errorf("empty token TTL")
	}
	trimmed := strings.TrimSuffix(tokenTTL, "s")
	var seconds float64
	if _, err := fmt.Sscanf(trimmed, "%f", &seconds); err != nil {
		return 0, err
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("non-positive token TTL %q", tokenTTL)
	}
	return int(seconds), nil
}
