package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noir017/gofer-cli/internal/config"
)

func TestSearchAttachmentKeywords(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")
	ctx := context.Background()

	if _, err := c.Search(ctx, SearchOptions{Query: `attachment:invoice.pdf filetype:pdf has:attachment`}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	q := f.query()
	if q.Get("attachment") != "invoice.pdf" {
		t.Errorf("attachment = %q, want invoice.pdf", q.Get("attachment"))
	}
	if q.Get("attachment_type") != "pdf" || q.Get("attachment_extension") != "" {
		t.Errorf("attachment_type = %q, attachment_extension = %q, want the pdf preset", q.Get("attachment_type"), q.Get("attachment_extension"))
	}
	if q.Get("attachments") != "1" {
		t.Errorf("attachments = %q, want 1", q.Get("attachments"))
	}
	if q.Get("q") != "" {
		t.Errorf("q = %q, want the keywords taken out of the text", q.Get("q"))
	}

	// A file extension is gofer's custom attachment type.
	if _, err := c.Search(ctx, SearchOptions{Query: `filetype:.eml`}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	q = f.query()
	if q.Get("attachment_type") != "custom" || q.Get("attachment_extension") != "eml" {
		t.Errorf("filetype:.eml = %q/%q, want custom/eml", q.Get("attachment_type"), q.Get("attachment_extension"))
	}

	// has: only knows attachments; anything else would be searched as text.
	if _, err := c.Search(ctx, SearchOptions{Query: `has:tag`}); err == nil || !strings.Contains(err.Error(), "has:attachment") {
		t.Errorf("has:tag error = %v, want a hint about has:attachment", err)
	}
	if _, err := c.Search(ctx, SearchOptions{Query: `filetype:!!!`}); err == nil || !strings.Contains(err.Error(), "filetype") {
		t.Errorf("filetype:!!! error = %v, want a filetype hint", err)
	}
}

func TestSearchFolderScope(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")
	// gofer refuses an account and a folder scope in one request, so the
	// folders win and the account is not sent. A repeated scope_folder_id
	// searches several folders at once.
	if _, err := c.Search(context.Background(), SearchOptions{FolderIDs: []string{"f1", "f2"}, AccountID: "acc1"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	q := f.query()
	if q.Get("scope") != "folders" {
		t.Errorf("scope = %q, want folders", q.Get("scope"))
	}
	if got := q["scope_folder_id"]; len(got) != 2 || got[0] != "f1" || got[1] != "f2" {
		t.Errorf("scope_folder_id = %v, want f1 and f2", got)
	}
	if q.Has("scope_account_id") {
		t.Errorf("scope_account_id = %q, want it omitted", q.Get("scope_account_id"))
	}
	if q.Get("include_descendants") != "1" {
		t.Errorf("include_descendants = %q, want 1, as the web UI searches subfolders", q.Get("include_descendants"))
	}
}

func TestSearchNotFoundNamesTheScope(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")
	ctx := context.Background()

	if _, err := c.Search(ctx, SearchOptions{FolderIDs: []string{"gone"}}); err == nil ||
		!strings.Contains(err.Error(), "folder not found") {
		t.Errorf("folder 404 = %v, want it to name the folder", err)
	}
	if _, err := c.Search(ctx, SearchOptions{AccountID: "gone"}); err == nil ||
		!strings.Contains(err.Error(), "account not found") {
		t.Errorf("account 404 = %v, want it to name the account", err)
	}
}

func TestSearchHasAttachment(t *testing.T) {
	f := newFakeGofer()
	c := clientFor(t, newFakeServer(t, f), f, "")
	if _, err := c.Search(context.Background(), SearchOptions{HasAttachment: true}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := f.query().Get("attachments"); got != "1" {
		t.Errorf("attachments = %q, want 1", got)
	}
}

// newBareClient returns a client for srv with a session already set, so no
// test needs a login round trip.
func newBareClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(config.Config{URL: srv.URL, User: "me@example.com", Password: "secret", CacheDir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.setSession("test-session")
	return c
}

// dropConnection closes the connection without writing an answer, which the
// client sees as EOF.
func dropConnection(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hijacker.Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func TestGetRetriesOnceOnDroppedConnection(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			dropConnection(w)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	c := newBareClient(t, srv)
	var sleeps int
	c.sleep = func(d time.Duration) {
		sleeps++
		if d != time.Second {
			t.Errorf("retry waited %s, want 1s", d)
		}
	}

	resp, err := c.do(context.Background(), http.MethodGet, "/thing", nil, nil, "")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
	if sleeps != 1 {
		t.Errorf("sleeps = %d, want 1", sleeps)
	}
}

func TestPostIsNotRetried(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		dropConnection(w)
	}))
	defer srv.Close()
	c := newBareClient(t, srv)
	c.sleep = func(time.Duration) { t.Error("a POST must not be retried") }

	if _, err := c.do(context.Background(), http.MethodPost, "/thing", nil, strings.NewReader("x"), ""); err == nil {
		t.Fatal("POST returned no error")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

func TestLoginRetriesOnce(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			dropConnection(w)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "fresh"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := newBareClient(t, srv)
	c.sleep = func(time.Duration) {}

	if err := c.Login(context.Background()); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

func TestMFAStepIsNotRetried(t *testing.T) {
	// A TOTP code is spent by the attempt that used it, so the two-factor
	// step must fail at once instead of trying the same code again.
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		dropConnection(w)
	}))
	defer srv.Close()
	c := newBareClient(t, srv)
	c.cfg.TOTPSecret = testTOTPSecret
	c.sleep = func(time.Duration) { t.Error("the two-factor step must not be retried") }

	if err := c.loginMFA(context.Background(), "pre-auth"); err == nil {
		t.Fatal("loginMFA returned no error")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

func TestTransient(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{io.EOF, true},
		{io.ErrUnexpectedEOF, true},
		{context.Canceled, false},
		{context.DeadlineExceeded, false},
		{&timeoutError{}, true},
		{http.ErrHandlerTimeout, false},
	}
	for _, tc := range cases {
		if got := transient(tc.err); got != tc.want {
			t.Errorf("transient(%v) = %t, want %t", tc.err, got, tc.want)
		}
	}
}

type timeoutError struct{}

func (*timeoutError) Error() string   { return "i/o timeout" }
func (*timeoutError) Timeout() bool   { return true }
func (*timeoutError) Temporary() bool { return true }
