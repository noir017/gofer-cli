package client

import (
	"context"
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

func TestActRejectsEmptyIDs(t *testing.T) {
	// Nothing to act on is a mistake at the call site, not a success: gofer
	// refuses a request without targets, so an empty selection must not look
	// like work that was done.
	c := &Client{}
	for _, ids := range [][]string{nil, {}, {""}, {"  ", "\t"}} {
		err := c.Act(context.Background(), ActRead, ids)
		if err == nil {
			t.Fatalf("Act(%q) succeeded with no ids", ids)
		}
		if !strings.Contains(err.Error(), "no message ids") {
			t.Fatalf("Act(%q) = %v, want the empty selection error", ids, err)
		}
	}
	if err := c.Move(context.Background(), nil, "acc_one_f_1"); err == nil || !strings.Contains(err.Error(), "no message ids") {
		t.Fatalf("Move(nil) = %v, want the empty selection error", err)
	}
}

func TestActRejectsUnknownAction(t *testing.T) {
	c := &Client{}
	if err := c.Act(context.Background(), Action("frobnicate"), []string{"1"}); err == nil {
		t.Fatal("unknown action accepted")
	}
}

func TestParseBulkAnswer(t *testing.T) {
	result, err := parseBulkAnswer([]byte(`{"updated":2,"no_archive":1}`), "acc_one archive")
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 2 || result.NoArchive != 1 || result.MissingFolder != "acc_one archive" {
		t.Fatalf("result = %+v, want the counts and the header", result)
	}

	result, err = parseBulkAnswer([]byte(`{"updated":1,"messages":2,"failed":1,"remote_failed":1}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || result.Failed != 1 || result.RemoteFailed != 1 {
		t.Fatalf("result = %+v, want the spam counts", result)
	}

	if result, err := parseBulkAnswer(nil, ""); err != nil || result.Updated != 0 {
		t.Fatalf("empty answer = %+v, %v; want no counts and no error", result, err)
	}
	if _, err := parseBulkAnswer([]byte("<html>login</html>"), ""); err == nil {
		t.Fatal("a page parsed as counts")
	}
}

func TestBulkResultError(t *testing.T) {
	tests := []struct {
		name   string
		result bulkResult
		want   string
	}{
		{"changed", bulkResult{Updated: 3}, ""},
		{"nothing to do", bulkResult{}, ""},
		{"some messages failed", bulkResult{Updated: 2, Failed: 1}, "1 message(s) could not be changed"},
		{"all messages failed", bulkResult{Failed: 2}, "2 message(s) could not be changed"},
		{"no archive folder", bulkResult{NoArchive: 1, MissingFolder: "acc_one archive"},
			"account acc_one has no archive folder"},
		{"no archive folder, no header", bulkResult{NoArchive: 2}, "2 accounts have no archive folder"},
		{"some archived", bulkResult{Updated: 1, NoArchive: 1}, ""},
		{"missing folder named without a count", bulkResult{MissingFolder: "acc_one trash"},
			"account acc_one has no trash folder"},
	}
	for _, tt := range tests {
		err := bulkResultError("archive", tt.result)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: err = %v, want none", tt.name, err)
		case tt.want != "" && err == nil:
			t.Errorf("%s: no error, want one about %q", tt.name, tt.want)
		case tt.want != "" && !strings.Contains(err.Error(), tt.want):
			t.Errorf("%s: err = %v, want it to say %q", tt.name, err, tt.want)
		}
	}
}

func TestRunBulkChunksReportsWhatAlreadyChanged(t *testing.T) {
	ids := make([]string, 0, bulkChunkSize+1)
	for i := 0; i <= bulkChunkSize; i++ {
		ids = append(ids, "1")
	}
	calls := 0
	err := runBulkChunks("read", ids, bulkChunkSize, func(chunk []string) (bulkResult, error) {
		calls++
		if calls == 1 {
			return bulkResult{Updated: len(chunk)}, nil
		}
		return bulkResult{}, errors.New("server said no")
	})
	if err == nil {
		t.Fatal("second batch failed but the run reported success")
	}
	if !strings.Contains(err.Error(), "200 message(s) had already been changed") {
		t.Fatalf("err = %v, want it to say how many messages the first batch changed", err)
	}
	if !strings.Contains(err.Error(), "server said no") {
		t.Fatalf("err = %v, want the cause kept", err)
	}

	// A failure in the first batch has nothing to report about earlier ones.
	err = runBulkChunks("read", []string{"1"}, bulkChunkSize, func([]string) (bulkResult, error) {
		return bulkResult{}, errors.New("server said no")
	})
	if err == nil || strings.Contains(err.Error(), "already been changed") {
		t.Fatalf("err = %v, want the cause alone", err)
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
