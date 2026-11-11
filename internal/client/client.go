// Package client talks to a gofer server over HTTP, the way its web UI does.
//
// Gofer has no public API. The client keeps to endpoints that return JSON or
// raw mail, and sends X-Gofer-Request: 1 on writes, which gofer accepts from
// clients that are not browsers (internal/httpguard in gofer).
package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/noir017/gofer-cli/internal/config"
	"github.com/noir017/gofer-cli/internal/model"
)

const (
	userAgent         = "gofer-cli"
	automationHeader  = "X-Gofer-Request"
	sessionCookieName = "gofer_session"
	preAuthCookieName = "gofer_pre_auth"
	formContentType   = "application/x-www-form-urlencoded"
)

type Client struct {
	cfg  config.Config
	base *url.URL
	http *http.Client

	mu       sync.Mutex
	token    string
	password string
	loginMu  sync.Mutex

	// now is the clock used for TOTP codes; tests replace it.
	now func() time.Time
}

// New returns a client for cfg. It does not touch the network.
func New(cfg config.Config) (*Client, error) {
	raw := strings.TrimSpace(cfg.URL)
	if raw == "" {
		return nil, fmt.Errorf("%w: set %s", model.ErrConfig, config.EnvURL)
	}
	base, err := url.Parse(raw)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, fmt.Errorf("%w: %s must be an http(s) URL", model.ErrConfig, config.EnvURL)
	}
	base.Path = strings.TrimSuffix(base.Path, "/")
	base.RawQuery, base.Fragment = "", ""
	cfg.URL = strings.TrimSuffix(raw, "/")

	c := &Client{
		cfg:  cfg,
		base: base,
		http: &http.Client{
			// A sign-in redirect or a 303 to /login is an answer, not a
			// detour: the caller decides whether to sign in again.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Timeout: 2 * time.Minute,
		},
		now: time.Now,
	}
	if cfg.CacheDir != "" {
		if saved, err := loadSession(cfg.CacheDir, cfg.URL, cfg.User); err == nil {
			c.token = saved.Token
		}
	}
	return c, nil
}

// do sends one request with the session cookie, logging in first when there
// is no session and once more when the server answers 401. Writes carry
// X-Gofer-Request: 1. The caller closes the body.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	payload, err := readRequestBody(body)
	if err != nil {
		return nil, err
	}
	return c.doURL(ctx, method, c.urlFor(path, query), payload, contentType)
}

// doURL is do for a target that needs path segments escaped one by one.
func (c *Client) doURL(ctx context.Context, method string, target *url.URL, payload []byte, contentType string) (*http.Response, error) {
	token := c.sessionToken()
	if token == "" {
		if err := c.login(ctx); err != nil {
			return nil, err
		}
		token = c.sessionToken()
	}

	resp, err := c.request(ctx, method, target, payload, contentType, cookieHeader(sessionCookieName, token), nil)
	if err != nil {
		return nil, err
	}
	if !needsAuth(resp) {
		return resp, nil
	}
	discard(resp)

	// The session expired or was revoked. Sign in again and replay the
	// request once; the body was buffered for exactly this case.
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	resp, err = c.request(ctx, method, target, payload, contentType, cookieHeader(sessionCookieName, c.sessionToken()), nil)
	if err != nil {
		return nil, err
	}
	if needsAuth(resp) {
		discard(resp)
		return nil, fmt.Errorf("gofer refused the session right after signing in: %w", model.ErrAuth)
	}
	return resp, nil
}

// urlFor builds a target under the configured base path.
func (c *Client) urlFor(path string, query url.Values) *url.URL {
	u := *c.base
	u.Path = c.base.Path + path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	return &u
}

// request sends one request without any sign-in logic. extra headers are
// added on top of the defaults.
func (c *Client) request(ctx context.Context, method string, target *url.URL, body []byte, contentType, cookie string, extra http.Header) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set(automationHeader, "1")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	for name, values := range extra {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	return c.http.Do(req)
}

func (c *Client) sessionToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token
}

func (c *Client) setSession(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
	// The session lives in memory even when the cache cannot be written.
	_ = saveSession(c.cfg.CacheDir, sessionFile{
		URL:     c.cfg.URL,
		User:    c.cfg.User,
		Token:   token,
		SavedAt: c.now().UTC(),
	})
}

func (c *Client) clearSession() {
	c.mu.Lock()
	c.token = ""
	c.mu.Unlock()
	_ = removeSession(c.cfg.CacheDir, c.cfg.URL, c.cfg.User)
}

func readRequestBody(body io.Reader) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	return payload, nil
}

// needsAuth reports whether resp says the request needs a signed-in session.
func needsAuth(resp *http.Response) bool {
	if resp.StatusCode == http.StatusUnauthorized {
		return true
	}
	if resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusFound {
		switch redirectPath(resp) {
		case "/login", "/admin/login":
			return true
		}
	}
	return false
}

func redirectURL(resp *http.Response) *url.URL {
	location := resp.Header.Get("Location")
	if location == "" {
		return nil
	}
	if resp.Request != nil && resp.Request.URL != nil {
		if u, err := resp.Request.URL.Parse(location); err == nil {
			return u
		}
	}
	u, err := url.Parse(location)
	if err != nil {
		return nil
	}
	return u
}

func redirectPath(resp *http.Response) string {
	if u := redirectURL(resp); u != nil {
		return u.Path
	}
	return ""
}

func cookieHeader(name, value string) string {
	if value == "" {
		return ""
	}
	return (&http.Cookie{Name: name, Value: value}).String()
}

func responseCookie(resp *http.Response, name string) string {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == name && cookie.Value != "" {
			return cookie.Value
		}
	}
	return ""
}

// discard reads a little of the body and closes it, so the connection can be
// reused without holding a large response in memory.
func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
}

// responseError turns an unexpected response into an error carrying a short
// excerpt of the body. It closes the body.
func responseError(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
	_ = resp.Body.Close()
	message := strings.TrimSpace(string(body))
	if message == "" {
		return fmt.Errorf("%s: %s", what, resp.Status)
	}
	return fmt.Errorf("%s: %s: %s", what, resp.Status, message)
}
