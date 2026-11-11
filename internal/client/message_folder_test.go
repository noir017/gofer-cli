package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const moveDialogPage = `<!doctype html><html><body>
<div class="h-[50vh]" role="listbox" data-mail-move-list data-mail-move-source-account="acc1">
  <button type="button" data-mail-move-folder="f1" data-mail-move-account="acc1" data-mail-move-name="inbox" disabled>
    <span>Inbox</span>
  </button>
  <button type="button" data-mail-move-folder="f2" data-mail-move-account="acc1" data-mail-move-name="archive">
    <span>Archive</span>
  </button>
  <button type="button" data-mail-move-folder="f9" data-mail-move-account="acc2" data-mail-move-name="sent" disabled>
    <span>Sent</span>
  </button>
</div>
</body></html>`

func TestMessageFolder(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(moveDialogPage))
	}))
	defer srv.Close()
	c := newBareClient(t, srv)

	got, err := c.MessageFolder(context.Background(), "42")
	if err != nil {
		t.Fatalf("MessageFolder: %v", err)
	}
	if got != "f1" {
		t.Errorf("folder = %q, want f1 (the disabled row of the message's own account)", got)
	}
	if path != "/email/42/move" {
		t.Errorf("path = %q, want /email/42/move", path)
	}
}

func TestMessageFolderRejectsAnotherPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><p>not the dialog</p></body></html>`))
	}))
	defer srv.Close()
	c := newBareClient(t, srv)

	_, err := c.MessageFolder(context.Background(), "42")
	if err == nil || !strings.Contains(err.Error(), "data-mail-move-list") {
		t.Fatalf("error = %v, want a marker complaint", err)
	}
}
