package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/config"
	"github.com/noir017/gofer-cli/internal/model"
)

func TestListTextOutput(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "1", Account: "acc-1"}},
		sources: map[string][]byte{"1": []byte("1")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{
			Date:    time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC),
			From:    []model.Address{{Name: "Ana", Email: "ana@example.com"}},
			To:      []model.Address{{Email: "me@example.com"}},
			Subject: "Hello",
			Text:    "body",
		}, nil
	}
	if code := e.run("list"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	want := "1\tacc-1\t2026-11-11 09:30\tAna <ana@example.com>\tme@example.com\tHello\n"
	if got := e.out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestListJSONOmitsBody(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "1"}},
		sources: map[string][]byte{"1": []byte("1")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{
			Date:    time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC),
			From:    []model.Address{{Email: "ana@example.com"}},
			To:      []model.Address{{Email: "me@example.com"}},
			Subject: "Hello",
			Text:    "the body",
			HTML:    "<p>the body</p>",
		}, nil
	}
	if code := e.run("list", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var rows []map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, e.out)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	for _, key := range []string{"text", "html"} {
		if _, ok := rows[0][key]; ok {
			t.Errorf("list JSON has %q, want it dropped", key)
		}
	}
	if rows[0]["id"] != "1" || rows[0]["subject"] != "Hello" {
		t.Errorf("row = %v", rows[0])
	}
}

func TestListShowsDatesInLocalZone(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "1"}},
		sources: map[string][]byte{"1": []byte("1")},
	}
	e := newEnv(t, api)
	e.app.Location = time.FixedZone("CST", 8*3600)
	// Gofer stamps its own outgoing mail in UTC; the listing shows the
	// reader's zone, not the zone of the header.
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Date: time.Date(2026, 11, 11, 7, 38, 0, 0, time.UTC)}, nil
	}
	if code := e.run("list"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); !strings.Contains(got, "2026-11-11 15:38") {
		t.Fatalf("stdout = %q, want the date in the local zone", got)
	}
}

func TestListJSONMarksConversationRows(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "1", Thread: true}},
		sources: map[string][]byte{"1": []byte("1")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Subject: "Hello"}, nil
	}
	if code := e.run("list", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var rows []map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if len(rows) != 1 || rows[0]["thread"] != true {
		t.Fatalf("rows = %v, want the conversation row marked thread", rows)
	}
}

func TestListSearchQuery(t *testing.T) {
	api := &fakeAPI{accounts: []model.Account{{ID: "acc-1", Email: "me@example.com"}}}
	e := newEnv(t, api)
	code := e.run("list",
		"--to", "to@example.com",
		"--from", "from@example.com",
		"--subject", "hello world",
		"--account", "me@example.com",
		"--unread",
		"--limit", "7",
		"-q", "invoice",
	)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.searches) != 1 {
		t.Fatalf("searches = %d, want 1", len(api.searches))
	}
	opts := api.searches[0]
	if opts.Query != "invoice" {
		t.Errorf("query = %q, want %q", opts.Query, "invoice")
	}
	if opts.To != "to@example.com" {
		t.Errorf("to = %q", opts.To)
	}
	if opts.From != "from@example.com" {
		t.Errorf("from = %q", opts.From)
	}
	if opts.Subject != "hello world" {
		t.Errorf("subject = %q", opts.Subject)
	}
	if opts.AccountID != "acc-1" {
		t.Errorf("account id = %q, want acc-1", opts.AccountID)
	}
	if !opts.Unread {
		t.Error("unread = false, want true")
	}
	if opts.Limit != 7 {
		t.Errorf("limit = %d, want 7", opts.Limit)
	}
	if opts.After != "" || opts.Before != "" {
		t.Errorf("after = %q, before = %q, want both empty", opts.After, opts.Before)
	}
}

func TestListUnknownAccountExitsTwo(t *testing.T) {
	api := &fakeAPI{accounts: []model.Account{{ID: "acc-1", Email: "me@example.com"}}}
	e := newEnv(t, api)
	if code := e.run("list", "--account", "other@example.com"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestListKeepsServerOrder(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{}}
	var want strings.Builder
	for _, id := range []string{"1", "2", "3", "4", "5", "6"} {
		api.refs = append(api.refs, model.MessageRef{ID: id})
		api.sources[id] = []byte(id)
		want.WriteString(id + "\t\t0001-01-01 00:00\t\t\t\n")
	}
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{}, nil
	}
	if code := e.run("list"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != want.String() {
		t.Fatalf("stdout = %q, want %q", got, want.String())
	}
}

func TestReadRaw(t *testing.T) {
	raw := "Subject: x\r\n\r\nbody\r\n"
	api := &fakeAPI{sources: map[string][]byte{"9": []byte(raw)}}
	e := newEnv(t, api)
	if code := e.run("read", "9", "--raw"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != raw {
		t.Fatalf("stdout = %q, want %q", got, raw)
	}
}

func TestReadTextAndJSON(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{
			Date:        time.Date(2026, 11, 11, 9, 30, 0, 0, time.UTC),
			From:        []model.Address{{Name: "Ana", Email: "ana@example.com"}},
			To:          []model.Address{{Email: "me@example.com"}},
			Subject:     "Hello",
			Text:        "the body\n",
			Attachments: []model.Attachment{{Index: 1, Filename: "a.pdf", ContentType: "application/pdf", Size: 12}},
		}, nil
	}
	if code := e.run("read", "9"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	got := e.out.String()
	for _, want := range []string{"ID: 9\n", "From: Ana <ana@example.com>\n", "Subject: Hello\n", "\nthe body\n", "Attachments:\n", "  1\ta.pdf\tapplication/pdf\t12\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q:\n%s", want, got)
		}
	}

	e.out.Reset()
	if code := e.run("read", "9", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var m model.Message
	if err := json.Unmarshal(e.out.Bytes(), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if m.ID != "9" || m.Text != "the body\n" {
		t.Fatalf("message = %+v", m)
	}
}

func TestLatestUsesLimitOne(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "2"}},
		sources: map[string][]byte{"2": []byte("2")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Subject: "newest"}, nil
	}
	if code := e.run("latest"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := api.searches[0].Limit; got != 1 {
		t.Fatalf("limit = %d, want 1", got)
	}
	if !strings.Contains(e.out.String(), "Subject: newest") {
		t.Fatalf("stdout = %q", e.out.String())
	}
}

func TestLatestNoMatchExitsTwo(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("latest"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestAttachments(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Attachments: []model.Attachment{{Index: 1, Filename: "a.pdf", ContentType: "application/pdf", Size: 12}}}, nil
	}
	if code := e.run("attachments", "9"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	want := "1\ta.pdf\tapplication/pdf\t12\n"
	if got := e.out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestSaveCleansFilename(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Attachments: []model.Attachment{{Index: 1, Filename: "../../etc/passwd"}}}, nil
	}
	e.app.AttachmentData = func(_ []byte, index int) (model.Attachment, []byte, error) {
		return model.Attachment{Index: index, Filename: "../../etc/passwd"}, []byte("data"), nil
	}
	dir := t.TempDir()
	if code := e.run("save", "9", "-o", dir); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	target := filepath.Join(dir, "passwd")
	if got := e.out.String(); got != target+"\n" {
		t.Fatalf("stdout = %q, want %q", got, target+"\n")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "data" {
		t.Fatalf("file = %q, want data", data)
	}
}

func TestSaveIndexOutOfRangeExitsTwo(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Attachments: []model.Attachment{{Index: 1, Filename: "a.pdf"}}}, nil
	}
	if code := e.run("save", "9", "--index", "2", "-o", t.TempDir()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestMarkReadsIDsFromStdin(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	e.app.Stdin = strings.NewReader("42\tsome subject\n43 another\n\n")
	e.app.StdinIsTerminal = func() bool { return false }
	if code := e.run("mark", "read", "-"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.acts) != 1 {
		t.Fatalf("acts = %d, want 1", len(api.acts))
	}
	if got := api.acts[0]; got.action != client.ActRead || strings.Join(got.ids, ",") != "42,43" {
		t.Fatalf("act = %+v", got)
	}
}

func TestArchiveAndDeleteMapToActions(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	if code := e.run("archive", "7"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if code := e.run("delete", "8"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if api.acts[0].action != client.ActArchive || api.acts[1].action != client.ActDelete {
		t.Fatalf("acts = %+v", api.acts)
	}
}

func TestMoveResolvesFolderByName(t *testing.T) {
	api := &fakeAPI{folders: []model.Folder{{ID: "f-archive", Name: "Archive"}}}
	e := newEnv(t, api)
	if code := e.run("move", "5", "--folder", "archive"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.moves) != 1 || api.moves[0].folder != "f-archive" {
		t.Fatalf("moves = %+v", api.moves)
	}
	if strings.Join(api.moves[0].ids, ",") != "5" {
		t.Fatalf("ids = %v", api.moves[0].ids)
	}
}

func TestMoveUnknownFolderExitsTwo(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("move", "5", "--folder", "Nope"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestSendBodyFromStdin(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	e.app.Stdin = strings.NewReader("hello body")
	e.app.StdinIsTerminal = func() bool { return false }
	if code := e.run("send", "--to", "ana@example.com", "--subject", "hi"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.sends) != 1 {
		t.Fatalf("sends = %d, want 1", len(api.sends))
	}
	req := api.sends[0]
	if req.Text != "hello body" {
		t.Errorf("body = %q, want %q", req.Text, "hello body")
	}
	if strings.Join(req.To, ",") != "ana@example.com" {
		t.Errorf("to = %v", req.To)
	}
	if e.out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", e.out.String())
	}
	if got := e.errOut.String(); got != "sent\n" {
		t.Errorf("stderr = %q, want sent", got)
	}
}

func TestSendJSON(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	if code := e.run("send", "--to", "ana@example.com", "--body", "hi", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var got map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("stdout = %v", got)
	}
}

func TestReplyAll(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("9")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{
			Subject: "Re: hello",
			From:    []model.Address{{Email: "alice@example.com"}},
			To:      []model.Address{{Email: "me@example.com"}},
			Cc:      []model.Address{{Email: "carol@example.com"}},
		}, nil
	}
	if code := e.run("reply", "9", "--all", "--body", "thanks"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	req := api.sends[0]
	if req.Subject != "Re: hello" {
		t.Errorf("subject = %q, want %q", req.Subject, "Re: hello")
	}
	if strings.Join(req.To, ",") != "alice@example.com" {
		t.Errorf("to = %v", req.To)
	}
	if strings.Join(req.Cc, ",") != "me@example.com,carol@example.com" {
		t.Errorf("cc = %v", req.Cc)
	}
	if req.ReplyTo != "9" {
		t.Errorf("reply to = %q, want 9", req.ReplyTo)
	}
}

func TestReplyAddsSubjectPrefix(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("9")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Subject: "hello", From: []model.Address{{Email: "alice@example.com"}}}, nil
	}
	if code := e.run("reply", "9", "--body", "thanks"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := api.sends[0].Subject; got != "Re: hello" {
		t.Fatalf("subject = %q", got)
	}
	if len(api.sends[0].Cc) != 0 {
		t.Fatalf("cc = %v, want empty without --all", api.sends[0].Cc)
	}
}

func TestOTP(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "7"}},
		sources: map[string][]byte{"7": []byte("7")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{
			Date:    e.clock.t.Add(-time.Minute),
			Subject: "Your code",
			From:    []model.Address{{Email: "no-reply@example.com"}},
		}, nil
	}
	e.app.OTP = func(*model.Message, string) (string, bool) { return "123456", true }
	if code := e.run("otp"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "123456\n" {
		t.Fatalf("stdout = %q, want the code only", got)
	}
}

func TestOTPJSON(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "7"}},
		sources: map[string][]byte{"7": []byte("7")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{
			Date:    e.clock.t.Add(-time.Minute),
			Subject: "Your code",
			From:    []model.Address{{Email: "no-reply@example.com"}},
		}, nil
	}
	e.app.OTP = func(*model.Message, string) (string, bool) { return "123456", true }
	if code := e.run("otp", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var got map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if got["code"] != "123456" || got["id"] != "7" || got["subject"] != "Your code" || got["from"] != "no-reply@example.com" {
		t.Fatalf("stdout = %v", got)
	}
	if _, ok := got["date"]; !ok {
		t.Errorf("stdout has no date: %v", got)
	}
	if _, ok := got["link"]; ok {
		t.Errorf("stdout has a link: %v", got)
	}
}

func TestOTPAndLinkRejectInvalidRegexp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		flag string
	}{
		{"otp pattern", []string{"otp", "--pattern", "("}, "--pattern"},
		{"link match", []string{"link", "--match", "["}, "--match"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api := &fakeAPI{}
			e := newEnv(t, api)
			if code := e.run(tc.args...); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if got := e.errOut.String(); !strings.Contains(got, tc.flag) {
				t.Fatalf("stderr = %q, want it to name %s", got, tc.flag)
			}
			if len(api.searches) != 0 {
				t.Errorf("searches = %d, want none before the regexp is valid", len(api.searches))
			}
		})
	}
}

func TestOTPSinceWindow(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "7"}},
		sources: map[string][]byte{"7": []byte("7")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t.Add(-time.Hour)}, nil
	}
	e.app.OTP = func(*model.Message, string) (string, bool) { return "123456", true }
	if code := e.run("otp"); code != 2 {
		t.Fatalf("exit = %d, want 2 for a message outside --since", code)
	}
}

func TestOTPWaitTimesOut(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	if code := e.run("otp", "--wait", "7"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if len(api.syncs) != 1 {
		t.Fatalf("syncs = %v, want one at the start", api.syncs)
	}
	if e.clock.sleeps < 2 {
		t.Fatalf("sleeps = %d, want the poll loop to run", e.clock.sleeps)
	}
	if !strings.HasPrefix(e.errOut.String(), "gofer-cli: ") {
		t.Fatalf("stderr = %q", e.errOut.String())
	}
}

func TestOTPWaitResyncs(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	if code := e.run("otp", "--wait", "20"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if len(api.syncs) != 2 {
		t.Fatalf("syncs = %d, want 2 (start and 15s)", len(api.syncs))
	}
}

// arrivalAPI is a search result where the sent and draft copies are newest,
// as they are right after the caller sends a message.
func arrivalAPI() *fakeAPI {
	return &fakeAPI{
		refs: []model.MessageRef{
			{ID: "sent", FolderID: "f_sent"},
			{ID: "draft", FolderID: "f_drafts"},
			{ID: "inbox", FolderID: "f_inbox"},
		},
		sources: map[string][]byte{
			"sent": []byte("sent"), "draft": []byte("draft"), "inbox": []byte("inbox"),
		},
		folders: []model.Folder{
			{ID: "f_sent", Role: "sent"},
			{ID: "f_drafts", Role: "drafts"},
			{ID: "f_inbox", Role: "inbox"},
			{ID: "f_trash", Role: "trash"},
		},
	}
}

func TestOTPSkipsSentAndDraftCopies(t *testing.T) {
	api := arrivalAPI()
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
	e.app.OTP = func(m *model.Message, _ string) (string, bool) {
		return m.Subject, true
	}
	if code := e.run("otp"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "inbox\n" {
		t.Fatalf("stdout = %q, want the code from the received message", got)
	}
	if got := api.sourceCalls.Load(); got != 1 {
		t.Errorf("source reads = %d, want the sent and draft copies skipped", got)
	}
}

func TestLinkSkipsSentCopies(t *testing.T) {
	api := arrivalAPI()
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
	e.app.Link = func(m *model.Message, _ string) (string, bool) {
		return "https://example.com/" + m.Subject, true
	}
	if code := e.run("link"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "https://example.com/inbox\n" {
		t.Fatalf("stdout = %q, want the link from the received message", got)
	}
}

func TestOTPKeepsTrashCopies(t *testing.T) {
	api := arrivalAPI()
	api.refs = []model.MessageRef{{ID: "trash", FolderID: "f_trash"}}
	api.sources["trash"] = []byte("trash")
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
	e.app.OTP = func(m *model.Message, _ string) (string, bool) { return "482913", true }
	if code := e.run("otp"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "482913\n" {
		t.Fatalf("stdout = %q, want the message in Trash to match", got)
	}
}

func TestOTPWarnsWhenFoldersFail(t *testing.T) {
	api := arrivalAPI()
	api.foldersErr = errors.New("folders unavailable")
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
	e.app.OTP = func(m *model.Message, _ string) (string, bool) { return m.Subject, true }
	if code := e.run("otp"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	// Without the folder list nothing is filtered, so the sent copy wins.
	if got := e.out.String(); got != "sent\n" {
		t.Fatalf("stdout = %q, want the unfiltered result", got)
	}
	if got := e.errOut.String(); !strings.Contains(got, "cannot list folders") {
		t.Fatalf("stderr = %q, want a warning about the folder list", got)
	}
}

func TestOTPKeepsConversationRows(t *testing.T) {
	// With conversation grouping on, a row's folder is where its newest
	// message sits. A conversation whose newest message is a sent copy must
	// still be read: the received mail is inside it.
	api := arrivalAPI()
	api.refs = []model.MessageRef{{ID: "thread", FolderID: "f_sent", Thread: true}}
	api.sources["thread"] = []byte("thread")
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
	e.app.OTP = func(*model.Message, string) (string, bool) { return "482913", true }
	if code := e.run("otp"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "482913\n" {
		t.Fatalf("stdout = %q, want the conversation row read", got)
	}
}

func TestWaitKeepsConversationRows(t *testing.T) {
	api := &seqAPI{
		fakeAPI: &fakeAPI{
			sources: map[string][]byte{"thread": []byte("thread")},
			folders: []model.Folder{{ID: "f_sent", Role: "sent"}},
		},
		seq: [][]model.MessageRef{
			{},
			{{ID: "thread", FolderID: "f_sent", Thread: true}},
		},
	}
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
	if code := e.run("wait", "--timeout", "10"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); !strings.Contains(got, "Subject: thread") {
		t.Fatalf("stdout = %q, want the conversation row printed", got)
	}
}

func TestWaitSkipsSentCopy(t *testing.T) {
	api := &seqAPI{
		fakeAPI: &fakeAPI{
			sources: map[string][]byte{"sent": []byte("sent"), "inbox": []byte("inbox")},
			folders: []model.Folder{
				{ID: "f_sent", Role: "sent"},
				{ID: "f_inbox", Role: "inbox"},
			},
		},
		seq: [][]model.MessageRef{
			{},
			{{ID: "sent", FolderID: "f_sent"}, {ID: "inbox", FolderID: "f_inbox"}},
		},
	}
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
	if code := e.run("wait", "--timeout", "10"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); !strings.Contains(got, "Subject: inbox") {
		t.Fatalf("stdout = %q, want the received message, not the sent copy", got)
	}
}

func TestLink(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "3"}},
		sources: map[string][]byte{"3": []byte("3")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, From: []model.Address{{Email: "no-reply@example.com"}}}, nil
	}
	e.app.Link = func(*model.Message, string) (string, bool) { return "https://example.com/login", true }
	if code := e.run("link"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "https://example.com/login\n" {
		t.Fatalf("stdout = %q", got)
	}
}

// seqAPI returns a different set of search results on each call.
type seqAPI struct {
	*fakeAPI
	seq   [][]model.MessageRef
	calls int
}

func (s *seqAPI) Search(ctx context.Context, opts client.SearchOptions) ([]model.MessageRef, error) {
	s.searches = append(s.searches, opts)
	refs := s.seq[len(s.seq)-1]
	if s.calls < len(s.seq) {
		refs = s.seq[s.calls]
	}
	s.calls++
	return refs, nil
}

func TestWaitReturnsFirstNewMessage(t *testing.T) {
	api := &seqAPI{
		fakeAPI: &fakeAPI{sources: map[string][]byte{"a": []byte("a"), "b": []byte("b")}},
		seq: [][]model.MessageRef{
			{{ID: "a"}},
			{{ID: "b"}, {ID: "a"}},
		},
	}
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Subject: string(raw)}, nil
	}
	if code := e.run("wait", "--timeout", "10"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if !strings.Contains(e.out.String(), "Subject: b") {
		t.Fatalf("stdout = %q, want the new message", e.out.String())
	}
	if got := api.searches[0].Limit; got != 200 {
		t.Fatalf("wait limit = %d, want 200", got)
	}
}

func TestWaitTimesOut(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	if code := e.run("wait", "--timeout", "6"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if len(api.syncs) < 1 {
		t.Fatal("wait never synced")
	}
}

func TestDoctor(t *testing.T) {
	api := &fakeAPI{info: model.ServerInfo{URL: "https://gofer.example.com", Version: "1.2.3", User: "me@example.com"}}
	e := newEnv(t, api)
	e.app.loadConfig = func() (config.Config, error) {
		return config.Config{URL: "https://gofer.example.com", User: "me@example.com", Password: "secret"}, nil
	}
	if code := e.run("doctor"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	got := e.out.String()
	for _, want := range []string{"config", "server", "login", "version", "1.2.3", "ok"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "secret") {
		t.Error("doctor printed the password")
	}
}

func TestDoctorConfigFailureExitsThree(t *testing.T) {
	e := newEnv(t, nil)
	if code := e.run("doctor"); code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	if got := e.out.String(); !strings.Contains(got, "FAIL") {
		t.Fatalf("stdout = %q, want a FAIL line", got)
	}
}

func TestDoctorReportsRefusedLoginAsLogin(t *testing.T) {
	api := &fakeAPI{infoErr: fmt.Errorf("login: invalid credentials: %w", model.ErrAuth)}
	e := newEnv(t, api)
	e.app.loadConfig = func() (config.Config, error) {
		return config.Config{URL: "https://gofer.example.com", User: "me@example.com", Password: "wrong"}, nil
	}
	if code := e.run("doctor"); code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	var serverOK, loginFail bool
	for _, line := range strings.Split(e.out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "server") && strings.Contains(line, "ok"):
			serverOK = true
		case strings.HasPrefix(line, "login") && strings.Contains(line, "FAIL"):
			loginFail = true
		}
	}
	if !serverOK || !loginFail {
		t.Fatalf("stdout = %q, want a passing server check and a failed login check", e.out.String())
	}
}

func TestAccounts(t *testing.T) {
	api := &fakeAPI{accounts: []model.Account{{ID: "acc-1", Email: "me@example.com", Name: "Me", Provider: "imap"}}}
	e := newEnv(t, api)
	if code := e.run("accounts"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	want := "acc-1\tme@example.com\tMe\timap\n"
	if got := e.out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	e.out.Reset()
	if code := e.run("accounts", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var accounts []model.Account
	if err := json.Unmarshal(e.out.Bytes(), &accounts); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if len(accounts) != 1 || accounts[0].ID != "acc-1" {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestFoldersResolvesAccount(t *testing.T) {
	api := &fakeAPI{
		accounts: []model.Account{{ID: "acc-1", Email: "me@example.com"}},
		folders:  []model.Folder{{ID: "f-1", AccountID: "acc-1", Name: "Inbox", Role: "inbox", Unread: 3}},
	}
	e := newEnv(t, api)
	if code := e.run("folders", "--account", "me@example.com"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	want := "f-1\tacc-1\tInbox\tinbox\t3\n"
	if got := e.out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestSyncResolvesAccount(t *testing.T) {
	api := &fakeAPI{accounts: []model.Account{{ID: "acc-1", Email: "me@example.com"}}}
	e := newEnv(t, api)
	if code := e.run("sync", "--account", "me@example.com"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.syncs) != 1 || api.syncs[0] != "acc-1" {
		t.Fatalf("syncs = %v, want acc-1", api.syncs)
	}
}

func TestLoginAndLogout(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	if code := e.run("login"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if api.logins != 1 {
		t.Fatalf("logins = %d, want 1", api.logins)
	}
	if got := e.errOut.String(); got != "logged in\n" {
		t.Fatalf("stderr = %q", got)
	}
	if e.out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", e.out.String())
	}

	e.errOut.Reset()
	if code := e.run("logout"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := e.errOut.String(); got != "logged out\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestSaveOneIndex(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Attachments: []model.Attachment{{Index: 1, Filename: "a.txt"}, {Index: 2, Filename: "b.txt"}}}, nil
	}
	e.app.AttachmentData = func(_ []byte, index int) (model.Attachment, []byte, error) {
		return model.Attachment{Index: index, Filename: "b.txt"}, []byte("data"), nil
	}
	dir := t.TempDir()
	if code := e.run("save", "9", "--index", "2", "-o", dir); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	want := filepath.Join(dir, "b.txt") + "\n"
	if got := e.out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("a.txt should not be written, stat err = %v", err)
	}
}

func TestListSkipsUnreadableMessage(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "1"}, {ID: "2"}},
		sources: map[string][]byte{"1": []byte("1")},
	}
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Subject: string(raw)}, nil
	}
	if code := e.run("list"); code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, e.errOut)
	}
	if got := e.out.String(); got != "1\t\t0001-01-01 00:00\t\t\t1\n" {
		t.Fatalf("stdout = %q, want only the readable message", got)
	}
	if !strings.Contains(e.errOut.String(), "skipping message 2") {
		t.Fatalf("stderr = %q, want a skip line", e.errOut.String())
	}
}

func TestListFiltersControlCharacters(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "1"}},
		sources: map[string][]byte{"1": []byte("1")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Subject: "bad\x1b[31m\x07 text"}, nil
	}
	if code := e.run("list"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if strings.ContainsAny(e.out.String(), "\x1b\x07") {
		t.Fatalf("stdout still has control characters: %q", e.out.String())
	}
	if !strings.Contains(e.out.String(), "bad[31m text") {
		t.Fatalf("stdout = %q", e.out.String())
	}
}

func TestReadRejectsConflictingOutput(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("read", "9", "--raw", "--json"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(e.errOut.String(), "only one of") {
		t.Fatalf("stderr = %q", e.errOut.String())
	}
}

func TestReadHTMLMissingExitsOne(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Subject: "plain"}, nil
	}
	if code := e.run("read", "9", "--html"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(e.errOut.String(), "no HTML part") {
		t.Fatalf("stderr = %q", e.errOut.String())
	}
}

func TestLatestRawSkipsParse(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "2"}},
		sources: map[string][]byte{"2": []byte("the raw message")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		t.Fatal("latest --raw must not parse")
		return nil, nil
	}
	if code := e.run("latest", "--raw"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "the raw message" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestSaveDuplicateNamesGetSuffix(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Attachments: []model.Attachment{{Index: 1, Filename: "a.txt"}, {Index: 2, Filename: "a.txt"}}}, nil
	}
	e.app.AttachmentData = func(_ []byte, index int) (model.Attachment, []byte, error) {
		return model.Attachment{Index: index, Filename: "a.txt"}, []byte("data"), nil
	}
	dir := t.TempDir()
	if code := e.run("save", "9", "-o", dir); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	want := filepath.Join(dir, "a.txt") + "\n" + filepath.Join(dir, "a-1.txt") + "\n"
	if got := e.out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestSaveNegativeIndexFails(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Attachments: []model.Attachment{{Index: 1, Filename: "a.txt"}}}, nil
	}
	if code := e.run("save", "9", "--index=-1", "-o", t.TempDir()); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestOTPDoesNotDownloadTwice(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "7"}},
		sources: map[string][]byte{"7": []byte("7")},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t.Add(-time.Minute)}, nil
	}
	if code := e.run("otp", "--wait", "5"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := api.sourceCalls.Load(); got != 1 {
		t.Fatalf("source calls = %d, want 1: a checked message must not be read again", got)
	}
}

func TestOTPSinceKeepsLooking(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "old"}, {ID: "new"}},
		sources: map[string][]byte{"old": []byte("old"), "new": []byte("new")},
	}
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		date := e.clock.t.Add(-time.Hour)
		if string(raw) == "new" {
			date = e.clock.t.Add(-time.Minute)
		}
		return &model.Message{Date: date}, nil
	}
	e.app.OTP = func(m *model.Message, _ string) (string, bool) {
		if m.Date.After(e.clock.t.Add(-time.Hour)) {
			return "424242", true
		}
		return "", false
	}
	if code := e.run("otp"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "424242\n" {
		t.Fatalf("stdout = %q, want the code from the newer message", got)
	}
}

func TestDoctorJSON(t *testing.T) {
	api := &fakeAPI{info: model.ServerInfo{URL: "https://gofer.example.com", Version: "1.2.3", User: "me@example.com"}}
	e := newEnv(t, api)
	e.app.loadConfig = func() (config.Config, error) {
		return config.Config{URL: "https://gofer.example.com", User: "me@example.com", Password: "secret"}, nil
	}
	if code := e.run("doctor", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var got struct {
		Checks []struct {
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, e.out)
	}
	names := make([]string, 0, len(got.Checks))
	for _, c := range got.Checks {
		names = append(names, c.Name)
		if !c.OK {
			t.Errorf("check %s failed: %s", c.Name, c.Detail)
		}
	}
	if strings.Join(names, ",") != "config,server,login,version" {
		t.Fatalf("checks = %v", names)
	}
}

func TestDoctorNetworkErrorExitsOne(t *testing.T) {
	api := &fakeAPI{infoErr: errors.New("dial tcp 203.0.113.1:443: connect: connection refused")}
	e := newEnv(t, api)
	e.app.loadConfig = func() (config.Config, error) {
		return config.Config{URL: "https://gofer.example.com", User: "me@example.com", Password: "secret"}, nil
	}
	if code := e.run("doctor"); code != 1 {
		t.Fatalf("exit = %d, want 1 for a network error", code)
	}
	if !strings.Contains(e.out.String(), "FAIL") {
		t.Fatalf("stdout = %q", e.out.String())
	}
}

func TestDoctorHidesUserinfo(t *testing.T) {
	api := &fakeAPI{info: model.ServerInfo{URL: "https://user:secret@gofer.example.com", Version: "1.2.3"}}
	e := newEnv(t, api)
	e.app.loadConfig = func() (config.Config, error) {
		return config.Config{URL: "https://user:secret@gofer.example.com", User: "me@example.com", Password: "secret"}, nil
	}
	if code := e.run("doctor"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	got := e.out.String()
	if strings.Contains(got, "user:secret") || strings.Contains(got, "secret@") {
		t.Fatalf("stdout leaks userinfo: %q", got)
	}
	if !strings.Contains(got, "gofer.example.com") {
		t.Fatalf("stdout = %q", got)
	}
}

func TestReplyAllSkipsOwnAddresses(t *testing.T) {
	api := &fakeAPI{
		sources:  map[string][]byte{"9": []byte("9")},
		accounts: []model.Account{{ID: "acc-1", Email: "me@example.com"}},
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{
			Subject: "hello",
			From:    []model.Address{{Email: "alice@example.com"}},
			To:      []model.Address{{Email: "me@example.com"}, {Email: "bob@example.com"}},
			Cc:      []model.Address{{Email: "me@example.com"}, {Email: "carol@example.com"}},
		}, nil
	}
	if code := e.run("reply", "9", "--all", "--body", "thanks"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := strings.Join(api.sends[0].Cc, ","); got != "bob@example.com,carol@example.com" {
		t.Fatalf("cc = %q", got)
	}
}

func TestMoveScopesFolderToMessageAccount(t *testing.T) {
	api := &fakeAPI{
		folders: []model.Folder{
			{ID: "f-a", AccountID: "acc-1", Name: "Archive"},
			{ID: "f-b", AccountID: "acc-2", Name: "Archive"},
		},
		refs: []model.MessageRef{{ID: "5", Account: "acc-2"}},
	}
	e := newEnv(t, api)
	if code := e.run("move", "5", "--folder", "Archive"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.moves) != 1 || api.moves[0].folder != "f-b" {
		t.Fatalf("moves = %+v, want f-b", api.moves)
	}
}

func TestMoveAmbiguousFolderName(t *testing.T) {
	api := &fakeAPI{
		folders: []model.Folder{
			{ID: "f-a", AccountID: "acc-1", Name: "Archive"},
			{ID: "f-b", AccountID: "acc-2", Name: "Archive"},
		},
	}
	e := newEnv(t, api)
	if code := e.run("move", "5", "--folder", "Archive"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "f-a") || !strings.Contains(got, "f-b") || !strings.Contains(got, "--account") {
		t.Fatalf("stderr = %q, want the candidate ids and the --account hint", got)
	}
}

func TestStdinIDsFromTerminalFails(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("mark", "read", "-"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(e.errOut.String(), "stdin is a terminal") {
		t.Fatalf("stderr = %q", e.errOut.String())
	}
}

func TestSendRejectsBodyAndFile(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("send", "--to", "ana@example.com", "--body", "x", "--body-file", "/tmp/y"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(e.errOut.String(), "mutually exclusive") {
		t.Fatalf("stderr = %q", e.errOut.String())
	}
}

func TestOTPSearchWindow(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	if code := e.run("otp"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	opts := api.searches[0]
	if opts.Limit != 50 {
		t.Errorf("limit = %d, want 50", opts.Limit)
	}
	want := e.clock.t.Add(-10*time.Minute).AddDate(0, 0, -1).Format("2006-01-02")
	if opts.After != want {
		t.Errorf("after = %q, want %q", opts.After, want)
	}
}

func TestOTPSearchWindowUsesTheLocalDate(t *testing.T) {
	api := &fakeAPI{}
	e := newEnv(t, api)
	e.app.Location = time.FixedZone("CST", 8*3600)
	// 23:30 UTC is already the next day in the reader's zone.
	e.clock.t = time.Date(2026, 11, 10, 23, 30, 0, 0, time.UTC)
	if code := e.run("otp"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	// Local date 2026-11-11, minus ten minutes, minus the day of slack.
	if got := api.searches[0].After; got != "2026-11-10" {
		t.Errorf("after = %q, want 2026-11-10", got)
	}
}

func TestMoveUniqueNameSkipsAccountSearch(t *testing.T) {
	api := &fakeAPI{folders: []model.Folder{{ID: "f-a", AccountID: "acc-1", Name: "Archive"}}}
	e := newEnv(t, api)
	if code := e.run("move", "5", "--folder", "Archive"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.moves) != 1 || api.moves[0].folder != "f-a" {
		t.Fatalf("moves = %+v", api.moves)
	}
	if len(api.searches) != 0 {
		t.Fatalf("searches = %d, want none for a unique folder name", len(api.searches))
	}
}

func TestMoveAccountFlagScopesName(t *testing.T) {
	api := &fakeAPI{
		folders: []model.Folder{
			{ID: "f-a", AccountID: "acc-1", Name: "Archive"},
			{ID: "f-b", AccountID: "acc-2", Name: "Archive"},
		},
		accounts: []model.Account{{ID: "acc-1", Email: "me@example.com"}},
	}
	e := newEnv(t, api)
	if code := e.run("move", "5", "--folder", "Archive", "--account", "me@example.com"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.moves) != 1 || api.moves[0].folder != "f-a" {
		t.Fatalf("moves = %+v, want f-a", api.moves)
	}
	if len(api.searches) != 0 {
		t.Fatalf("searches = %d, want none when --account is given", len(api.searches))
	}
}

func TestListFailsWhenNothingOutput(t *testing.T) {
	api := &fakeAPI{
		refs:      []model.MessageRef{{ID: "1"}},
		sources:   map[string][]byte{},
		sourceErr: errors.New("connection reset"),
	}
	e := newEnv(t, api)
	if code := e.run("list"); code != 1 {
		t.Fatalf("exit = %d, want 1 when no message could be read", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "skipping message 1") || !strings.Contains(got, "gofer-cli: ") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestOTPRetriesUnparseableMessage(t *testing.T) {
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "7"}},
		sources: map[string][]byte{"7": []byte("7")},
	}
	e := newEnv(t, api)
	parses := 0
	e.app.Parse = func([]byte) (*model.Message, error) {
		parses++
		if parses == 1 {
			return nil, errors.New("malformed")
		}
		return &model.Message{Date: e.clock.t}, nil
	}
	e.app.OTP = func(*model.Message, string) (string, bool) { return "5150", true }
	if code := e.run("otp", "--wait", "5"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "5150\n" {
		t.Fatalf("stdout = %q", got)
	}
	if api.sourceCalls.Load() != 2 {
		t.Fatalf("source calls = %d, want 2: a message that failed to parse is retried", api.sourceCalls.Load())
	}
}

// flakySourceAPI fails the first Source calls with a temporary error.
type flakySourceAPI struct {
	*seqAPI
	fails int
}

func (f *flakySourceAPI) Source(ctx context.Context, id string) ([]byte, error) {
	if f.fails > 0 {
		f.fails--
		return nil, errors.New("temporary read error")
	}
	return f.fakeAPI.Source(ctx, id)
}

func TestWaitRetriesSourceError(t *testing.T) {
	api := &flakySourceAPI{
		seqAPI: &seqAPI{
			fakeAPI: &fakeAPI{sources: map[string][]byte{"b": []byte("b")}},
			seq:     [][]model.MessageRef{{}, {{ID: "b"}}},
		},
		fails: 1,
	}
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Subject: string(raw)}, nil
	}
	if code := e.run("wait", "--timeout", "10"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if !strings.Contains(e.out.String(), "Subject: b") {
		t.Fatalf("stdout = %q", e.out.String())
	}
	if !strings.Contains(e.errOut.String(), "reading message b") {
		t.Fatalf("stderr = %q, want a retry line", e.errOut.String())
	}
}

func TestSaveFallbackNameUsesParsedIndex(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"9": []byte("raw")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{Attachments: []model.Attachment{{Index: 2, Filename: ""}}}, nil
	}
	e.app.AttachmentData = func(_ []byte, _ int) (model.Attachment, []byte, error) {
		return model.Attachment{Index: 99, Filename: ""}, []byte("data"), nil
	}
	dir := t.TempDir()
	if code := e.run("save", "9", "-o", dir); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	want := filepath.Join(dir, "attachment-2") + "\n"
	if got := e.out.String(); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestReplyAllFailsWhenAccountsFail(t *testing.T) {
	api := &fakeAPI{
		sources:     map[string][]byte{"9": []byte("9")},
		accountsErr: errors.New("accounts unavailable"),
	}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) {
		return &model.Message{From: []model.Address{{Email: "alice@example.com"}}}, nil
	}
	if code := e.run("reply", "9", "--all", "--body", "thanks"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if len(api.sends) != 0 {
		t.Fatalf("sends = %d, want none when the account list fails", len(api.sends))
	}
}

func TestSendAndReplyUseShowsOptionalFrom(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	root := e.app.root()
	for _, name := range []string{"send", "reply"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		if !strings.Contains(cmd.Use, "[--from") {
			t.Errorf("%s Use = %q, want [--from ...]", name, cmd.Use)
		}
	}
}
