package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noir017/gofer-cli/internal/config"
	"github.com/noir017/gofer-cli/internal/model"
)

const testTOTPSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

type fakeGofer struct {
	mu sync.Mutex

	user     string
	password string
	version  string
	mfa      bool
	mfaFail  string // expired, enroll or throttle
	secret   string
	now      func() time.Time

	nextToken int
	sessions  map[string]bool
	preAuth   string
	logins    int
	mfaLogins int
	lastQuery url.Values
	logout    bool
	csrfSeen  string
	sources   map[string]string
}

func newFakeGofer() *fakeGofer {
	return &fakeGofer{
		user:     "agent@example.com",
		password: "secret",
		version:  "v9.9.9",
		now:      time.Now,
		sessions: make(map[string]bool),
		sources: map[string]string{
			"42":  "From: sender@example.com\r\nSubject: hello\r\n\r\nbody text\r\n",
			"4 2": "From: sender@example.com\r\nSubject: escaped id\r\n\r\nbody text\r\n",
		},
	}
}

func (f *fakeGofer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", f.handleLogin)
	mux.HandleFunc("POST /login/mfa", f.handleMFA)
	mux.HandleFunc("GET /login", f.handleLoginPage)
	mux.HandleFunc("GET /mail/search/ids", f.handleSearch)
	mux.HandleFunc("GET /api/messages/{id}/source", f.handleSource)
	mux.HandleFunc("POST /api/echo", f.handleEcho)
	mux.HandleFunc("POST /auth/logout", f.handleLogout)
	return mux
}

func (f *fakeGofer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !f.checkAutomation(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.logins++
	user, password, mfa := f.user, f.password, f.mfa
	f.mu.Unlock()
	if r.PostFormValue("identifier") != user || r.PostFormValue("password") != password {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if mfa {
		f.mu.Lock()
		f.preAuth = "pre-1"
		failure := f.mfaFail
		f.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "gofer_pre_auth", Value: "pre-1", Path: "/"})
		if failure == "enroll" {
			http.Redirect(w, r, "/login/mfa/enroll", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/login/mfa", http.StatusSeeOther)
		return
	}
	f.issueSession(w, r)
}

func (f *fakeGofer) handleMFA(w http.ResponseWriter, r *http.Request) {
	if !f.checkAutomation(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.mfaLogins++
	preAuth, secret, now, failure := f.preAuth, f.secret, f.now, f.mfaFail
	f.mu.Unlock()
	switch failure {
	case "expired":
		http.Redirect(w, r, "/login?error=mfa", http.StatusSeeOther)
		return
	case "throttle":
		http.Error(w, "slow down", http.StatusTooManyRequests)
		return
	}
	cookie, err := r.Cookie("gofer_pre_auth")
	if err != nil || cookie.Value != preAuth {
		http.Error(w, "missing pre-auth challenge", http.StatusUnauthorized)
		return
	}
	want, err := totpCode(secret, now())
	if err != nil || r.PostFormValue("code") != want {
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	}
	f.issueSession(w, r)
}

func (f *fakeGofer) issueSession(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.nextToken++
	token := fmt.Sprintf("token-%d", f.nextToken)
	f.sessions[token] = true
	f.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "gofer_session", Value: token, Path: "/"})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (f *fakeGofer) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !f.checkAutomation(w, r) {
		return
	}
	f.mu.Lock()
	version := f.version
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<html><body><a class="fixed" data-public-version>Gofer %s</a></body></html>`, version)
}

func (f *fakeGofer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		f.reject(w, r)
		return
	}
	f.mu.Lock()
	f.lastQuery = r.URL.Query()
	f.mu.Unlock()
	if r.URL.Query().Get("scope_account_id") == "gone" {
		http.NotFound(w, r)
		return
	}

	ids := []map[string]any{
		{"id": "42", "thread": false, "account": "acc1", "folder_id": "f1"},
		{"id": "43", "thread": true, "account": "acc1", "folder_id": "f1"},
		{"id": "41", "thread": false, "account": "acc2", "folder_id": "f2"},
	}
	if r.URL.Query().Get("unread") == "1" {
		ids = ids[:1]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ids": ids, "total": len(ids), "truncated": false,
	})
}

func (f *fakeGofer) handleSource(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		f.reject(w, r)
		return
	}
	f.mu.Lock()
	data, ok := f.sources[r.PathValue("id")]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, data)
}

func (f *fakeGofer) handleEcho(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		f.reject(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	_, _ = w.Write(body)
}

func (f *fakeGofer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !f.checkAutomation(w, r) {
		return
	}
	cookie, err := r.Cookie("gofer_session")
	if err != nil {
		f.reject(w, r)
		return
	}
	f.mu.Lock()
	valid := f.sessions[cookie.Value]
	f.csrfSeen = r.Header.Get("X-CSRF-Token")
	f.mu.Unlock()
	if !valid {
		f.reject(w, r)
		return
	}
	if r.Header.Get("X-CSRF-Token") != csrfToken(cookie.Value, http.MethodPost, "/auth/logout") {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	f.mu.Lock()
	delete(f.sessions, cookie.Value)
	f.logout = true
	f.mu.Unlock()
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (f *fakeGofer) checkAutomation(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Gofer-Request") != "1" {
		http.Error(w, "missing X-Gofer-Request", http.StatusBadRequest)
		return false
	}
	return true
}

func (f *fakeGofer) authorized(r *http.Request) bool {
	cookie, err := r.Cookie("gofer_session")
	if err != nil {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions[cookie.Value]
}

func (f *fakeGofer) reject(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"authentication_required"}`)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (f *fakeGofer) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins
}

func (f *fakeGofer) query() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastQuery
}

func (f *fakeGofer) revokeSessions() {
	f.mu.Lock()
	f.sessions = make(map[string]bool)
	f.mu.Unlock()
}

func (f *fakeGofer) didLogout() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logout
}

func (f *fakeGofer) csrfTokenSeen() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.csrfSeen
}

func (f *fakeGofer) sourceBody(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sources[id]
}

func newFakeServer(t *testing.T, f *fakeGofer) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return srv
}

func clientFor(t *testing.T, srv *httptest.Server, f *fakeGofer, cacheDir string) *Client {
	t.Helper()
	if cacheDir == "" {
		cacheDir = t.TempDir()
	}
	c, err := New(config.Config{
		URL:      srv.URL,
		User:     f.user,
		Password: f.password,
		CacheDir: cacheDir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSearch(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")

	refs, err := c.Search(context.Background(), SearchOptions{Query: "hello", Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d, want the limit of 2", len(refs))
	}
	if refs[0].ID != "42" || refs[0].Account != "acc1" || refs[0].FolderID != "f1" {
		t.Errorf("refs[0] = %+v", refs[0])
	}
	if !refs[1].Thread {
		t.Errorf("refs[1] = %+v, want the conversation row marked Thread", refs[1])
	}
	q := f.query()
	for name, want := range map[string]string{
		"q": "hello", "sort_by": "date", "sort_order": "desc",
	} {
		if got := q.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if q.Has("no_threads") {
		t.Errorf("no_threads = %q, want it omitted so conversations are not dropped", q.Get("no_threads"))
	}
	if q.Get("scope") != "" {
		t.Errorf("scope = %q, want it omitted", q.Get("scope"))
	}

	refs, err = c.Search(context.Background(), SearchOptions{Query: "", AccountID: "acc1", Unread: true})
	if err != nil {
		t.Fatalf("Search with filters: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("len(refs) = %d, want 1", len(refs))
	}
	q = f.query()
	if q.Get("scope") != "account" || q.Get("scope_account_id") != "acc1" || q.Get("unread") != "1" {
		t.Errorf("filter query = %v", q)
	}
	if q.Get("q") != "" {
		t.Errorf("q = %q, want an empty query to list everything", q.Get("q"))
	}
	if got := f.loginCount(); got != 1 {
		t.Errorf("logins = %d, want one sign-in reused by both searches", got)
	}
}

func TestSource(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")

	data, err := c.Source(context.Background(), "42")
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if string(data) != f.sourceBody("42") {
		t.Errorf("source = %q", data)
	}

	if _, err := c.Source(context.Background(), "99"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing message error = %v, want ErrNotFound", err)
	}

	// A space must be escaped inside the path segment, not taken as a
	// separator or query character.
	escaped, err := c.Source(context.Background(), "4 2")
	if err != nil {
		t.Fatalf("Source with a space in the id: %v", err)
	}
	if string(escaped) != f.sourceBody("4 2") {
		t.Errorf("source = %q", escaped)
	}
}

func TestSearchAccountNotFound(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")

	_, err := c.Search(context.Background(), SearchOptions{AccountID: "gone"})
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("Search error = %v, want ErrNotFound", err)
	}
}

func TestReloginAfterSessionRevoked(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")

	// Prime a session, then make the server forget it.
	if _, err := c.Search(context.Background(), SearchOptions{Query: "hello"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	f.revokeSessions()

	// /mail/search/ids answers a 303 to /login when the session is gone.
	if _, err := c.Search(context.Background(), SearchOptions{Query: "hello"}); err != nil {
		t.Fatalf("Search after revoke: %v", err)
	}
	// /api/* answers 401 JSON instead.
	if _, err := c.Source(context.Background(), "42"); err != nil {
		t.Fatalf("Source after revoke: %v", err)
	}
	if got := f.loginCount(); got != 2 {
		t.Errorf("logins = %d, want one sign-in per revoked session plus the first", got)
	}
}

func TestDoReplaysBodyAfterReLogin(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")
	if _, err := c.Search(context.Background(), SearchOptions{Query: "hello"}); err != nil {
		t.Fatal(err)
	}
	f.revokeSessions()

	resp, err := c.do(context.Background(), http.MethodPost, "/api/echo", nil, strings.NewReader("payload"), "text/plain")
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "payload" {
		t.Errorf("echoed body = %q, want the request body replayed", body)
	}
}

func TestSessionCacheSharedAcrossClients(t *testing.T) {
	f := newFakeGofer()
	srv := newFakeServer(t, f)
	cacheDir := t.TempDir()

	first := clientFor(t, srv, f, cacheDir)
	if _, err := first.Search(context.Background(), SearchOptions{Query: "hello"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := f.loginCount(); got != 1 {
		t.Fatalf("logins = %d, want 1", got)
	}

	info, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("cache dir mode = %o, want 700", info.Mode().Perm())
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "session-") {
		t.Fatalf("cache entries = %v", entries)
	}
	fileInfo, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Errorf("session file mode = %o, want 600", fileInfo.Mode().Perm())
	}

	second := clientFor(t, srv, f, cacheDir)
	if _, err := second.Search(context.Background(), SearchOptions{Query: "hello"}); err != nil {
		t.Fatalf("Search from second client: %v", err)
	}
	if got := f.loginCount(); got != 1 {
		t.Errorf("logins = %d, want the cached session reused", got)
	}
}

func TestLogoutSendsCSRFAndRemovesSession(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")
	if _, err := c.Search(context.Background(), SearchOptions{Query: "hello"}); err != nil {
		t.Fatal(err)
	}
	sessionFile := sessionPath(c.cfg.CacheDir, c.cfg.URL, c.cfg.User)

	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if !f.didLogout() {
		t.Error("the server did not revoke the session")
	}
	if f.csrfTokenSeen() == "" {
		t.Error("Logout sent no X-CSRF-Token")
	}
	if _, err := os.Stat(sessionFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session file still there: %v", err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Errorf("second Logout: %v", err)
	}

	if _, err := c.Search(context.Background(), SearchOptions{Query: "hello"}); err != nil {
		t.Fatalf("Search after Logout: %v", err)
	}
	if got := f.loginCount(); got != 2 {
		t.Errorf("logins = %d, want a fresh sign-in after Logout", got)
	}
}

func TestLogoutKeepsSessionOnTransportError(t *testing.T) {
	dir := t.TempDir()
	c, err := New(config.Config{URL: "http://127.0.0.1:1", User: "u", Password: "p", CacheDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	c.token = "still-valid"
	if err := saveSession(dir, sessionFile{
		URL: c.cfg.URL, User: c.cfg.User, Token: "still-valid", SavedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.Logout(context.Background()); err == nil {
		t.Fatal("Logout against an unreachable server returned no error")
	}
	if got := c.sessionToken(); got != "still-valid" {
		t.Errorf("session token = %q, want it kept after a transport error", got)
	}
	if _, err := os.Stat(sessionPath(dir, c.cfg.URL, c.cfg.User)); err != nil {
		t.Errorf("cached session was removed: %v", err)
	}
}

func TestExpiredSessionIsIgnored(t *testing.T) {
	f := newFakeGofer()
	srv := newFakeServer(t, f)
	cacheDir := t.TempDir()
	if err := saveSession(cacheDir, sessionFile{
		URL: srv.URL, User: f.user, Token: "stale",
		SavedAt: time.Now().Add(-31 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	c := clientFor(t, srv, f, cacheDir)
	if got := c.sessionToken(); got != "" {
		t.Errorf("New loaded an expired session: %q", got)
	}
	if _, err := c.Search(context.Background(), SearchOptions{Query: "hello"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := f.loginCount(); got != 1 {
		t.Errorf("logins = %d, want a fresh sign-in instead of the expired session", got)
	}
}

func TestMFA(t *testing.T) {
	f := newFakeGofer()
	f.mfa = true
	f.secret = testTOTPSecret
	f.now = func() time.Time { return time.Unix(1111111109, 0) }
	c := clientFor(t, newFakeServer(t, f), f, "")
	c.cfg.TOTPSecret = testTOTPSecret
	c.now = f.now

	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login with TOTP: %v", err)
	}
	f.mu.Lock()
	attempts := f.mfaLogins
	f.mu.Unlock()
	if attempts != 1 {
		t.Errorf("MFA submissions = %d, want 1", attempts)
	}
	if _, err := c.Source(context.Background(), "42"); err != nil {
		t.Errorf("Source after MFA login: %v", err)
	}
}

func TestMFAWithoutSecret(t *testing.T) {
	f := newFakeGofer()
	f.mfa = true
	f.secret = testTOTPSecret
	c := clientFor(t, newFakeServer(t, f), f, "")

	err := c.Login(context.Background())
	if !errors.Is(err, model.ErrAuth) {
		t.Fatalf("Login error = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "GOFER_TOTP_SECRET") {
		t.Errorf("error %q does not name GOFER_TOTP_SECRET", err)
	}
}

func TestMFAExpiredChallenge(t *testing.T) {
	f := newFakeGofer()
	f.mfa = true
	f.mfaFail = "expired"
	f.secret = testTOTPSecret
	c := clientFor(t, newFakeServer(t, f), f, "")
	c.cfg.TOTPSecret = testTOTPSecret

	err := c.Login(context.Background())
	if !errors.Is(err, model.ErrAuth) {
		t.Fatalf("Login error = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error %q does not say the challenge expired", err)
	}
}

func TestMFAEnrollmentRequired(t *testing.T) {
	f := newFakeGofer()
	f.mfa = true
	f.mfaFail = "enroll"
	c := clientFor(t, newFakeServer(t, f), f, "")

	err := c.Login(context.Background())
	if !errors.Is(err, model.ErrAuth) {
		t.Fatalf("Login error = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "enrollment") {
		t.Errorf("error %q does not name the enrollment requirement", err)
	}
}

func TestMFAThrottled(t *testing.T) {
	f := newFakeGofer()
	f.mfa = true
	f.mfaFail = "throttle"
	f.secret = testTOTPSecret
	c := clientFor(t, newFakeServer(t, f), f, "")
	c.cfg.TOTPSecret = testTOTPSecret

	err := c.Login(context.Background())
	if !errors.Is(err, model.ErrAuth) {
		t.Fatalf("Login error = %v, want ErrAuth", err)
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("error %q does not say the attempt was throttled", err)
	}
}

func TestTOTPCode(t *testing.T) {
	// RFC 6238 SHA-1 test vectors, truncated to six digits.
	vectors := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, vector := range vectors {
		code, err := totpCode(testTOTPSecret, time.Unix(vector.unix, 0))
		if err != nil {
			t.Fatalf("totpCode(%d): %v", vector.unix, err)
		}
		if code != vector.want {
			t.Errorf("totpCode(%d) = %s, want %s", vector.unix, code, vector.want)
		}
	}
}

func TestCSRFToken(t *testing.T) {
	// Reference value computed independently with Python's hmac module.
	const want = "f5fb6be7f758032fd8fc48b164209bb087a6882f086bf9a6ae79cf6d460473c7"
	if got := csrfToken("abc123", http.MethodPost, "/auth/logout"); got != want {
		t.Errorf("csrfToken = %s, want %s", got, want)
	}
}

func TestInfo(t *testing.T) {
	f := newFakeGofer()
	f.version = "v9.9.9"
	srv := newFakeServer(t, f)
	c := clientFor(t, srv, f, "")

	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.URL != srv.URL || info.User != f.user || info.Version != "v9.9.9" {
		t.Errorf("Info = %+v", info)
	}
	if got := f.loginCount(); got != 1 {
		t.Errorf("logins = %d, want 1", got)
	}
}

func TestLoginErrors(t *testing.T) {
	f := newFakeGofer()
	srv := newFakeServer(t, f)

	bad := clientFor(t, srv, f, "")
	bad.cfg.Password = "wrong"
	if err := bad.Login(context.Background()); !errors.Is(err, model.ErrAuth) {
		t.Fatalf("wrong password error = %v, want ErrAuth", err)
	}

	missingUser, err := New(config.Config{URL: srv.URL, Password: "secret", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := missingUser.Login(context.Background()); !errors.Is(err, model.ErrConfig) {
		t.Fatalf("missing user error = %v, want ErrConfig", err)
	}

	missingPassword, err := New(config.Config{URL: srv.URL, User: f.user, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := missingPassword.Login(context.Background()); !errors.Is(err, model.ErrConfig) {
		t.Fatalf("missing password error = %v, want ErrConfig", err)
	}
}

func TestNewRejectsBadURL(t *testing.T) {
	for _, raw := range []string{"", "gofer.example.com", "ftp://gofer.example.com"} {
		if _, err := New(config.Config{URL: raw}); !errors.Is(err, model.ErrConfig) {
			t.Errorf("New(%q) error = %v, want ErrConfig", raw, err)
		}
	}
}

func TestConcurrentSessionWrites(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = saveSession(dir, sessionFile{
				URL:     "https://gofer.example.com",
				User:    "agent@example.com",
				Token:   fmt.Sprintf("token-%d", i),
				SavedAt: time.Now(),
			})
		}(i)
	}
	wg.Wait()

	saved, err := loadSession(dir, "https://gofer.example.com", "agent@example.com")
	if err != nil {
		t.Fatalf("loadSession after concurrent writes: %v", err)
	}
	if saved.Token == "" {
		t.Error("saved session has no token")
	}
	path := sessionPath(dir, "https://gofer.example.com", "agent@example.com")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("session file mode = %o, want 600", info.Mode().Perm())
	}
	if dirInfo, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("stat cache dir: %v", err)
	} else if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("cache dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
}
