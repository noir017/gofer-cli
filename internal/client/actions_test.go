package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/noir017/gofer-cli/internal/model"
)

func TestActionEndpoint(t *testing.T) {
	tests := []struct {
		action Action
		path   string
		state  string
		folder string
	}{
		{ActRead, "/api/messages/read", "read", ""},
		{ActUnread, "/api/messages/read", "unread", ""},
		{ActStar, "/api/messages/star", "starred", ""},
		{ActUnstar, "/api/messages/star", "unstarred", ""},
		{ActArchive, "/api/messages/archive", "", ""},
		{ActUnarchive, "/api/messages/unarchive", "", ""},
		{ActSpam, "/api/messages/spam", "", ""},
		{ActNotSpam, "/api/messages/not-spam", "", ""},
		// Delete is a move to Trash: /api/messages/delete would delete a
		// message that already sits in Trash for good.
		{ActDelete, "/api/messages/move", "", "trash"},
	}
	for _, tt := range tests {
		path, state, folder, err := actionEndpoint(tt.action)
		if err != nil {
			t.Fatalf("%s: %v", tt.action, err)
		}
		if path != tt.path || state != tt.state || folder != tt.folder {
			t.Errorf("%s = (%q, %q, %q), want (%q, %q, %q)",
				tt.action, path, state, folder, tt.path, tt.state, tt.folder)
		}
	}
	if _, _, _, err := actionEndpoint(Action("frobnicate")); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestBulkBody(t *testing.T) {
	body, err := bulkBody([]string{" 12 ", "", "13"}, "unread", "")
	if err != nil {
		t.Fatal(err)
	}
	var got bulkRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 2 || got.Targets[0].ID != "12" || got.Targets[1].ID != "13" {
		t.Fatalf("targets = %+v, want the two trimmed ids", got.Targets)
	}
	if got.State != "unread" {
		t.Errorf("state = %q, want unread", got.State)
	}
	if strings.Contains(string(body), "folder_id") {
		t.Errorf("body carries an empty folder_id: %s", body)
	}
}

func TestBulkBodyMove(t *testing.T) {
	body, err := bulkBody([]string{"7"}, "", "acc_one_f_2")
	if err != nil {
		t.Fatal(err)
	}
	var got bulkRequest
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.FolderID != "acc_one_f_2" {
		t.Errorf("folder_id = %q, want acc_one_f_2", got.FolderID)
	}
	if got.State != "" || strings.Contains(string(body), "state") {
		t.Errorf("a move carries state %q", got.State)
	}
}

func TestBulkBodyRejectsEmptyIDs(t *testing.T) {
	for _, ids := range [][]string{nil, {}, {""}, {" "}} {
		if _, err := bulkBody(ids, "read", ""); err == nil {
			t.Errorf("bulkBody(%q) accepted no ids", ids)
		}
	}
}

func TestChunkIDs(t *testing.T) {
	if got := chunkIDs(nil, 200); len(got) != 0 {
		t.Errorf("chunkIDs(nil) = %v, want nothing", got)
	}
	ids := make([]string, 450)
	for i := range ids {
		ids[i] = "x"
	}
	chunks := chunkIDs(ids, bulkChunkSize)
	if len(chunks) != 3 || len(chunks[0]) != 200 || len(chunks[1]) != 200 || len(chunks[2]) != 50 {
		t.Fatalf("chunk sizes = %d/%d/%d, want 200/200/50",
			len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

func TestActionStatusErrorKeepsGoferText(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader("message not found\n")),
	}
	err := actionStatusError("/api/messages/read", resp)
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("err = %v, want it to wrap model.ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "message not found") {
		t.Errorf("err = %v, want gofer's own text", err)
	}

	resp = &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader("folder_id required")),
	}
	err = actionStatusError("/api/messages/move", resp)
	if errors.Is(err, model.ErrNotFound) {
		t.Errorf("err = %v, want a plain error", err)
	}
	if !strings.Contains(err.Error(), "folder_id required") || !strings.Contains(err.Error(), "messages/move") {
		t.Errorf("err = %v, want the endpoint and gofer's text", err)
	}
}
