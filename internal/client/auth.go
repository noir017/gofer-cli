package client

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/noir017/gofer-cli/internal/config"
	"github.com/noir017/gofer-cli/internal/model"
)

const (
	csrfHeaderName    = "X-CSRF-Token"
	csrfKeyContext    = "gofer-csrf-session-key-v1"
	csrfActionContext = "gofer-csrf-action-v1"
	totpPeriod        = 30 * time.Second
)

// Login signs in and replaces the cached session.
func (c *Client) Login(ctx context.Context) error { return c.login(ctx) }

func (c *Client) login(ctx context.Context) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()

	if c.cfg.User == "" {
		return fmt.Errorf("%w: set %s", model.ErrConfig, config.EnvUser)
	}
	password, err := c.passwordValue()
	if err != nil {
		return err
	}

	form := url.Values{
		"identifier": {c.cfg.User},
		"password":   {password},
	}
	resp, err := c.requestAttempt(ctx, http.MethodPost, c.urlFor("/login", nil), []byte(form.Encode()), formContentType, "", nil, true)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if token := responseCookie(resp, sessionCookieName); token != "" {
		c.setSession(token)
		return nil
	}
	if preAuth := responseCookie(resp, preAuthCookieName); preAuth != "" && redirectPath(resp) == "/login/mfa" {
		return c.loginMFA(ctx, preAuth)
	}
	if err := secondFactorRedirectError(resp); err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("login: invalid credentials: %w", model.ErrAuth)
	case http.StatusTooManyRequests:
		return fmt.Errorf("login: too many attempts, try again later: %w", model.ErrAuth)
	default:
		return fmt.Errorf("login: unexpected status %s", resp.Status)
	}
}

// loginMFA completes the TOTP step after the password was accepted.
func (c *Client) loginMFA(ctx context.Context, preAuth string) error {
	if c.cfg.TOTPSecret == "" {
		return fmt.Errorf("%w: two-factor authentication is required, set %s", model.ErrAuth, config.EnvTOTPSecret)
	}
	code, err := totpCode(c.cfg.TOTPSecret, c.now())
	if err != nil {
		return fmt.Errorf("login: %v: %w", err, model.ErrAuth)
	}
	form := url.Values{"code": {code}}
	resp, err := c.requestAttempt(ctx, http.MethodPost, c.urlFor("/login/mfa", nil), []byte(form.Encode()), formContentType,
		cookieHeader(preAuthCookieName, preAuth), nil, true)
	if err != nil {
		return fmt.Errorf("login: two-factor step: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if token := responseCookie(resp, sessionCookieName); token != "" {
		c.setSession(token)
		return nil
	}
	if err := secondFactorRedirectError(resp); err != nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("login: two-factor code rejected: %w", model.ErrAuth)
	case http.StatusTooManyRequests:
		return fmt.Errorf("login: too many two-factor attempts, try again later: %w", model.ErrAuth)
	default:
		return fmt.Errorf("login: two-factor step: unexpected status %s", resp.Status)
	}
}

// secondFactorRedirectError names the redirects gofer uses when the second
// factor cannot continue (internal/handler/local_login.go in gofer).
func secondFactorRedirectError(resp *http.Response) error {
	target := redirectURL(resp)
	if target == nil {
		return nil
	}
	switch target.Path {
	case "/login/mfa/enroll":
		return fmt.Errorf("%w: gofer requires two-factor enrollment; complete it in the web UI first", model.ErrAuth)
	case "/login":
		if target.Query().Get("error") == "mfa" {
			return fmt.Errorf("%w: the two-factor challenge expired or was already used; sign in again", model.ErrAuth)
		}
	}
	return nil
}

// Logout revokes the server session and removes the cached one. A session the
// server already forgot counts as logged out; a transport failure leaves the
// cached session alone, since the server may not have seen the request.
func (c *Client) Logout(ctx context.Context) error {
	token := c.sessionToken()
	if token == "" {
		c.clearSession()
		return nil
	}
	header := http.Header{
		csrfHeaderName: {csrfToken(token, http.MethodPost, "/auth/logout")},
	}
	resp, err := c.request(ctx, http.MethodPost, c.urlFor("/auth/logout", nil), nil, "", cookieHeader(sessionCookieName, token), header)
	if err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	status := resp.StatusCode
	discard(resp)
	c.clearSession()
	if status == http.StatusUnauthorized || status == http.StatusSeeOther || status == http.StatusFound || status == http.StatusOK {
		return nil
	}
	return fmt.Errorf("logout: unexpected status %d", status)
}

func (c *Client) passwordValue() (string, error) {
	c.mu.Lock()
	if c.password != "" {
		password := c.password
		c.mu.Unlock()
		return password, nil
	}
	c.mu.Unlock()

	password, err := c.cfg.ResolvePassword()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.password = password
	c.mu.Unlock()
	return password, nil
}

// csrfToken derives the proof gofer requires on /auth/logout, bound to the
// session token, the method and the exact path (internal/auth/csrf.go in
// gofer).
func csrfToken(sessionToken, method, path string) string {
	sessionKey := hmacSHA256([]byte(sessionToken), []byte(csrfKeyContext))
	action := method + "\x00" + path
	return hex.EncodeToString(hmacSHA256(sessionKey, []byte(csrfActionContext+"\x00"+action)))
}

func hmacSHA256(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

// totpCode returns the current six-digit TOTP code for a base32 secret, as
// gofer expects: SHA-1, 30-second steps, six digits.
func totpCode(secret string, at time.Time) (string, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	normalized = strings.TrimRight(normalized, "=")
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	if err != nil || len(key) == 0 {
		return "", fmt.Errorf("invalid TOTP secret")
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.UTC().Unix()/int64(totpPeriod/time.Second)))

	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000), nil
}
