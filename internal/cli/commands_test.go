package cli

import (
	"context"
	"encoding/json"
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
	want := "1\t2026-11-11 09:30\tAna <ana@example.com>\tme@example.com\tHello\n"
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
		"-q", "is:unread",
	)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if len(api.searches) != 1 {
		t.Fatalf("searches = %d, want 1", len(api.searches))
	}
	want := `to:to@example.com from:from@example.com subject:"hello world" account:me@example.com is:unread`
	if got := api.searches[0].Query; got != want {
		t.Fatalf("query = %q, want %q", got, want)
	}
	opts := api.searches[0]
	if opts.AccountID != "acc-1" {
		t.Errorf("account id = %q, want acc-1", opts.AccountID)
	}
	if !opts.Unread {
		t.Error("unread = false, want true")
	}
	if opts.Limit != 7 {
		t.Errorf("limit = %d, want 7", opts.Limit)
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
		want.WriteString(id + "\t0001-01-01 00:00\t\t\t\n")
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
