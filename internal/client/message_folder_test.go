package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// moveDialogPage is a real /email/{id}/move page, saved from a live instance.
// Account and folder ids, addresses and folder labels were replaced with
// example.com values before saving.
const moveDialogPage = "testdata/move_dialog.html"

func TestMessageFolder(t *testing.T) {
	page, err := os.ReadFile(moveDialogPage)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write(page)
	}))
	defer srv.Close()
	c := newBareClient(t, srv)

	got, err := c.MessageFolder(context.Background(), "42")
	if err != nil {
		t.Fatalf("MessageFolder: %v", err)
	}
	// The message sits in the source account's Trash; other accounts have a
	// disabled row too, and those must not be taken for it. The name comes
	// from the visible label: gofer lowercases data-mail-move-name, so
	// reading the attribute would return "trash".
	if got.ID != "f-15" || got.Name != "Trash" {
		t.Errorf("folder = %+v, want f-15 Trash", got)
	}
	if !strings.Contains(string(page), `data-mail-move-name="trash"`) {
		t.Fatal("fixture lost the lowercased attribute, so the case cannot be checked")
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
