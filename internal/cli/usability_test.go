package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/noir017/gofer-cli/internal/config"
	"github.com/noir017/gofer-cli/internal/model"
)

// listAPI is one inbox message and one trash message, with both folders
// named, as a mailbox looks after the caller threw something away.
func listAPI() *fakeAPI {
	return &fakeAPI{
		refs: []model.MessageRef{
			{ID: "t", Account: "acc-1", FolderID: "f_trash"},
			{ID: "1", Account: "acc-1", FolderID: "f_inbox"},
		},
		sources: map[string][]byte{"t": []byte("t"), "1": []byte("1")},
		folders: []model.Folder{
			{ID: "f_inbox", AccountID: "acc-1", Name: "Inbox", Role: "inbox"},
			{ID: "f_trash", AccountID: "acc-1", Name: "Trash", Role: "trash"},
		},
	}
}

func parseSubjects(e *env) func([]byte) (*model.Message, error) {
	return func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
}

func TestListSkipsTrashByDefault(t *testing.T) {
	api := listAPI()
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); !strings.HasPrefix(got, "1\tacc-1\tInbox\t") || strings.Contains(got, "t\tacc-1\tTrash\t") {
		t.Fatalf("stdout = %q, want only the inbox message", got)
	}
	if got := api.sourceCalls.Load(); got != 1 {
		t.Errorf("source reads = %d, want the trash row left unread", got)
	}
}

func TestListFolderTrashShowsTrash(t *testing.T) {
	api := listAPI()
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list", "--folder", "Trash"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := api.searches[0].FolderIDs[0]; got != "f_trash" {
		t.Errorf("folder = %q, want f_trash", got)
	}
	if got := e.out.String(); !strings.Contains(got, "t\tacc-1\tTrash\t") {
		t.Fatalf("stdout = %q, want the message in Trash", got)
	}
}

func TestListFolderSharedByAccountsSearchesAll(t *testing.T) {
	api := listAPI()
	api.folders = append(api.folders, model.Folder{ID: "f_trash2", AccountID: "acc-2", Name: "Trash", Role: "trash"})
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list", "--folder", "Trash"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	got := api.searches[0].FolderIDs
	if len(got) != 2 || got[0] != "f_trash" || got[1] != "f_trash2" {
		t.Errorf("folders = %v, want both Trash folders", got)
	}
}

func TestListFolderAcceptsID(t *testing.T) {
	api := listAPI()
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list", "--folder", "f_trash"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := api.searches[0].FolderIDs[0]; got != "f_trash" {
		t.Errorf("folder = %q, want f_trash", got)
	}
}

func TestListHasAttachment(t *testing.T) {
	api := listAPI()
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list", "--has-attachment"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if !api.searches[0].HasAttachment {
		t.Error("has attachment = false, want the filter passed to the search")
	}
}

func TestListShowsFolderNameAndFallsBackToID(t *testing.T) {
	api := listAPI()
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); !strings.HasPrefix(got, "1\tacc-1\tInbox\t") {
		t.Fatalf("stdout = %q, want the folder name after the account", got)
	}

	// Without a folder list the column still says where the message is.
	api2 := listAPI()
	api2.foldersErr = errors.New("folders unavailable")
	e2 := newEnv(t, api2)
	e2.app.Parse = parseSubjects(e2)
	if code := e2.run("list"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e2.errOut)
	}
	if got := e2.out.String(); !strings.Contains(got, "1\tacc-1\tf_inbox\t") {
		t.Fatalf("stdout = %q, want the folder id", got)
	}
}

func TestListJSONNamesTheFolder(t *testing.T) {
	api := listAPI()
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var rows []map[string]any
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, e.out)
	}
	if len(rows) != 1 || rows[0]["folder"] != "Inbox" || rows[0]["folder_id"] != "f_inbox" {
		t.Fatalf("rows = %v, want the folder name and id", rows)
	}
}

func TestListEmptyWithToHintsAtAccount(t *testing.T) {
	api := listAPI()
	api.refs = nil
	e := newEnv(t, api)
	if code := e.run("list", "--to", "someone@example.com"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.errOut.String(); !strings.Contains(got, "--to matches the To header; to pick a mailbox use --account <email>") {
		t.Fatalf("stderr = %q, want the --to hint", got)
	}
}

func TestReadShowsFolder(t *testing.T) {
	api := &fakeAPI{
		sources:  map[string][]byte{"7": []byte("7")},
		folderOf: map[string]model.Folder{"7": {ID: "f_inbox", Name: "Inbox"}},
	}
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("read", "7"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); !strings.Contains(got, "Folder: Inbox\n") {
		t.Fatalf("stdout = %q, want the folder line", got)
	}
	if len(api.folderCalls) != 1 || api.folderCalls[0] != "7" {
		t.Errorf("folder lookups = %v, want one for 7", api.folderCalls)
	}
	// The dialog carries the name, so read must not fetch the folder list.
	if got := api.folderLists.Load(); got != 0 {
		t.Errorf("folder list requests = %d, want none", got)
	}

	e2 := newEnv(t, api)
	e2.app.Parse = parseSubjects(e2)
	if code := e2.run("read", "7", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e2.errOut)
	}
	var m map[string]any
	if err := json.Unmarshal(e2.out.Bytes(), &m); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if m["folder"] != "Inbox" {
		t.Errorf("folder = %v, want Inbox", m["folder"])
	}
}

func TestReadWarnsWhenFolderLookupFails(t *testing.T) {
	api := &fakeAPI{
		sources:   map[string][]byte{"7": []byte("7")},
		folders:   []model.Folder{{ID: "f_inbox", Name: "Inbox"}},
		folderErr: errors.New("no move dialog"),
	}
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("read", "7"); code != 0 {
		t.Fatalf("exit = %d, want the message printed anyway", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "cannot tell the folder of message 7") {
		t.Fatalf("stderr = %q, want a warning", got)
	}
	if strings.Contains(e.out.String(), "Folder:") {
		t.Fatalf("stdout = %q, want no folder line", e.out.String())
	}
}

func TestBulkCommandsReportEveryMessage(t *testing.T) {
	api := &fakeAPI{folders: []model.Folder{{ID: "f_arch", Name: "Archive", Role: "archive"}}}
	e := newEnv(t, api)

	cases := []struct {
		args   []string
		result string
	}{
		{[]string{"delete", "1", "2"}, "trash"},
		{[]string{"archive", "1", "2"}, "archive"},
		{[]string{"spam", "1", "2"}, "spam"},
		{[]string{"move", "1", "2", "--folder", "Archive"}, "Archive"},
		{[]string{"mark", "read", "1", "2"}, "read"},
		{[]string{"mark", "unread", "1", "2"}, "unread"},
		{[]string{"mark", "star", "1", "2"}, "starred"},
		{[]string{"mark", "unstar", "1", "2"}, "unstarred"},
	}
	for _, tc := range cases {
		e.out.Reset()
		if code := e.run(tc.args...); code != 0 {
			t.Fatalf("%v: exit = %d, stderr = %s", tc.args, code, e.errOut)
		}
		want := "1\t" + tc.result + "\n2\t" + tc.result + "\n"
		if got := e.out.String(); got != want {
			t.Errorf("%v: stdout = %q, want %q", tc.args, got, want)
		}
	}

	e.out.Reset()
	if code := e.run("delete", "1", "2", "--json"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	var rows []actionResult
	if err := json.Unmarshal(e.out.Bytes(), &rows); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "1" || rows[0].Result != "trash" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestMoveUnknownFolderListsChoices(t *testing.T) {
	api := &fakeAPI{
		refs: []model.MessageRef{{ID: "1", Account: "acc-1", FolderID: "f_inbox"}},
		folders: []model.Folder{
			{ID: "f_inbox", AccountID: "acc-1", Name: "Inbox", Role: "inbox"},
			{ID: "f_arch", AccountID: "acc-1", Name: "Archive", Role: "archive"},
			{ID: "f_other", AccountID: "acc-2", Name: "Inbox", Role: "inbox"},
		},
	}
	e := newEnv(t, api)
	if code := e.run("move", "1", "--folder", "NoSuchFolder"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	got := e.errOut.String()
	if !strings.Contains(got, "available folders: Inbox (f_inbox), Archive (f_arch)") {
		t.Fatalf("stderr = %q, want the account's folders with ids", got)
	}
	if strings.Contains(got, "f_other") {
		t.Errorf("stderr = %q, want another account's folder left out", got)
	}
	if len(api.moves) != 0 {
		t.Errorf("moves = %v, want no request sent", api.moves)
	}
}

func TestOTPTimeoutAndNoMatchReadDifferently(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("otp", "--wait", "5"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "timed out after 5s; no match in the last 10m") {
		t.Fatalf("stderr = %q, want the timeout wording", got)
	}

	e2 := newEnv(t, &fakeAPI{})
	if code := e2.run("otp"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := e2.errOut.String(); !strings.Contains(got, "no match since 11:50 (last 10m); widen with --since 24h") {
		t.Fatalf("stderr = %q, want the no-match wording", got)
	}
}

func TestWaitTimeoutWordingAndAlias(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("wait", "--wait", "3"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "timed out after 3s; no new message") {
		t.Fatalf("stderr = %q, want the timeout wording", got)
	}

	e2 := newEnv(t, &fakeAPI{})
	if code := e2.run("wait", "--timeout", "3"); code != 2 {
		t.Fatalf("exit = %d, want 2 through the hidden alias", code)
	}
	if got := e2.errOut.String(); !strings.Contains(got, "timed out after 3s") {
		t.Fatalf("stderr = %q, want --timeout to mean --wait", got)
	}
}

func TestOTPAcceptsTimeoutAlias(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("otp", "--timeout", "3"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if e.clock.sleeps == 0 {
		t.Fatal("sleeps = 0, want the alias to start the wait loop")
	}
}

func TestAttachmentsWithoutAny(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"7": []byte("7")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) { return &model.Message{ID: "7"}, nil }
	if code := e.run("attachments", "7"); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "message 7 has no attachments") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestSaveWithoutAnyAttachments(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{"7": []byte("7")}}
	e := newEnv(t, api)
	e.app.Parse = func([]byte) (*model.Message, error) { return &model.Message{ID: "7"}, nil }
	if code := e.run("save", "7", "-o", t.TempDir()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "message 7 has no attachments") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestConfigErrorIsNotDoubled(t *testing.T) {
	e := newEnv(t, nil)
	e.app.loadConfig = func() (config.Config, error) {
		return config.Config{}, fmt.Errorf("%w: read /nonexistent: no such file", model.ErrConfig)
	}
	if code := e.run("list"); code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
	if got := e.errOut.String(); strings.Count(got, "not configured") != 1 {
		t.Fatalf("stderr = %q, want one \"not configured\"", got)
	}
}

func TestSendHelpSaysSubjectIsOptional(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("send", "--help"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	got := e.out.String()
	for _, want := range []string{"[--subject S]", "subject (optional)", "stdin"} {
		if !strings.Contains(got, want) {
			t.Errorf("help = %q, want %q", got, want)
		}
	}
}

func TestHelpDocumentsTextColumns(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	for _, tc := range []struct{ command, want string }{
		{"list", "id, account, folder, date, from, to, subject"},
		{"accounts", "id, email, name, provider"},
		{"folders", "id, account, name, role, unread"},
	} {
		e.out.Reset()
		if code := e.run(tc.command, "--help"); code != 0 {
			t.Fatalf("%s --help: exit = %d", tc.command, code)
		}
		if got := e.out.String(); !strings.Contains(got, tc.want) {
			t.Errorf("%s help = %q, want the columns %q", tc.command, got, tc.want)
		}
	}
}

func TestWaitPrintsTheFolderOfANewMessage(t *testing.T) {
	api := &seqAPI{
		fakeAPI: &fakeAPI{
			sources: map[string][]byte{"9": []byte("9")},
			folders: []model.Folder{
				{ID: "f_inbox", Name: "Inbox", Role: "inbox"},
				{ID: "f_sent", Name: "Sent", Role: "sent"},
			},
		},
		seq: [][]model.MessageRef{{}, {{ID: "9", FolderID: "f_inbox"}}},
	}
	e := newEnv(t, api)
	e.app.Parse = func(raw []byte) (*model.Message, error) {
		return &model.Message{Date: e.clock.t, Subject: string(raw)}, nil
	}
	if code := e.run("wait", "--wait", "10"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); !strings.Contains(got, "Folder: Inbox\n") {
		t.Fatalf("stdout = %q, want the folder line", got)
	}
}

func TestSearchCommandsShareFolderAndAttachmentFlags(t *testing.T) {
	for _, command := range []string{"list", "latest", "otp", "link", "wait"} {
		e := newEnv(t, &fakeAPI{})
		e.out.Reset()
		if code := e.run(command, "--help"); code != 0 {
			t.Fatalf("%s --help: exit = %d", command, code)
		}
		help := e.out.String()
		for _, flag := range []string{"--folder", "--has-attachment"} {
			if !strings.Contains(help, flag) {
				t.Errorf("%s help = %q, want %s", command, help, flag)
			}
		}
	}
}

func TestOTPWithFolderSearchesTrash(t *testing.T) {
	api := arrivalAPI()
	api.refs = []model.MessageRef{{ID: "trash", FolderID: "f_trash"}}
	api.sources["trash"] = []byte("trash")
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	e.app.OTP = func(*model.Message, string) (string, bool) { return "482913", true }
	if code := e.run("otp", "--folder", "Trash"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); got != "482913\n" {
		t.Fatalf("stdout = %q, want the code from the folder the caller named", got)
	}
	if got := api.searches[0].FolderIDs; len(got) != 1 || got[0] != "f_trash" {
		t.Errorf("folders = %v, want f_trash", got)
	}
}

func TestWaitWithFolderIncludesSent(t *testing.T) {
	api := &seqAPI{
		fakeAPI: &fakeAPI{
			sources: map[string][]byte{"sent": []byte("sent")},
			folders: []model.Folder{{ID: "f_sent", Name: "Sent", Role: "sent"}},
		},
		seq: [][]model.MessageRef{{}, {{ID: "sent", FolderID: "f_sent"}}},
	}
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("wait", "--folder", "Sent", "--wait", "10"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := e.out.String(); !strings.Contains(got, "Subject: sent") {
		t.Fatalf("stdout = %q, want the sent copy the caller asked for", got)
	}
}

func TestHasAttachmentOnEverySearchCommand(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"list", nil},
		{"latest", nil},
		{"otp", nil},
		{"link", nil},
		{"wait", []string{"--wait", "1"}},
	} {
		api := &fakeAPI{}
		e := newEnv(t, api)
		args := append([]string{tc.command, "--has-attachment"}, tc.args...)
		_ = e.run(args...)
		if len(api.searches) == 0 || !api.searches[0].HasAttachment {
			t.Errorf("%s: has attachment = false, want the filter passed to the search", tc.command)
		}
	}
}

func TestListFetchesMoreAndTruncatesAfterTrash(t *testing.T) {
	api := &fakeAPI{
		refs: []model.MessageRef{
			{ID: "t1", FolderID: "f_trash"},
			{ID: "t2", FolderID: "f_trash"},
			{ID: "a", FolderID: "f_inbox"},
			{ID: "b", FolderID: "f_inbox"},
			{ID: "c", FolderID: "f_inbox"},
		},
		sources: map[string][]byte{
			"t1": []byte("t1"), "t2": []byte("t2"),
			"a": []byte("a"), "b": []byte("b"), "c": []byte("c"),
		},
		folders: []model.Folder{
			{ID: "f_inbox", Name: "Inbox", Role: "inbox"},
			{ID: "f_trash", Name: "Trash", Role: "trash"},
		},
	}
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list", "--limit", "2"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if got := api.searches[0].Limit; got != fetchLimit(2) {
		t.Errorf("search limit = %d, want %d so trash does not use up the listing", got, fetchLimit(2))
	}
	lines := strings.Split(strings.TrimSpace(e.out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "a\t") || !strings.HasPrefix(lines[1], "b\t") {
		t.Fatalf("stdout = %q, want the two inbox messages", e.out.String())
	}
	if got := api.sourceCalls.Load(); got != 2 {
		t.Errorf("source reads = %d, want only the two listed messages", got)
	}
}

func TestListToHintOnlyWhenTheSearchItselfIsEmpty(t *testing.T) {
	// The search found rows and every one was trash: the filter is right,
	// only the listing is empty, so no hint.
	api := &fakeAPI{
		refs:    []model.MessageRef{{ID: "t", FolderID: "f_trash"}},
		sources: map[string][]byte{"t": []byte("t")},
		folders: []model.Folder{{ID: "f_trash", Role: "trash"}},
	}
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	if code := e.run("list", "--to", "someone@example.com"); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, e.errOut)
	}
	if strings.Contains(e.errOut.String(), "--to matches") {
		t.Fatalf("stderr = %q, want no hint when the search did return rows", e.errOut.String())
	}

	// An empty search with --to does hint.
	e2 := newEnv(t, &fakeAPI{})
	if code := e2.run("list", "--to", "someone@example.com"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(e2.errOut.String(), "--to matches the To header") {
		t.Fatalf("stderr = %q, want the hint", e2.errOut.String())
	}
}

func TestSearchFolderNameTooBroadFails(t *testing.T) {
	api := &fakeAPI{}
	for i := 0; i <= maxSearchFolders; i++ {
		api.folders = append(api.folders, model.Folder{
			ID: fmt.Sprintf("f-%d", i), AccountID: fmt.Sprintf("acc-%d", i), Name: "Shared",
		})
	}
	e := newEnv(t, api)
	if code := e.run("list", "--folder", "Shared"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "at most 200 folders") {
		t.Fatalf("stderr = %q, want the folder cap", got)
	}
}

func TestOTPLimitCapsDownloads(t *testing.T) {
	api := &fakeAPI{sources: map[string][]byte{}}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("%d", i)
		api.refs = append(api.refs, model.MessageRef{ID: id})
		api.sources[id] = []byte(id)
	}
	e := newEnv(t, api)
	e.app.Parse = parseSubjects(e)
	// The match sits beyond the limit, so finding it would mean reading more
	// messages than --limit allows.
	e.app.OTP = func(m *model.Message, _ string) (string, bool) {
		return "123456", m.Subject == "3"
	}
	if code := e.run("otp", "--limit", "3"); code != 2 {
		t.Fatalf("exit = %d, want 2: the match is past the limit", code)
	}
	if got := api.sourceCalls.Load(); got != 3 {
		t.Fatalf("source reads = %d, want the limit of 3", got)
	}
	if got := api.searches[0].Limit; got != fetchLimit(3) {
		t.Errorf("search limit = %d, want %d, so skipping does not empty the window", got, fetchLimit(3))
	}
}

func TestWidenHintDoublesBeyondADay(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("otp", "--since", "48h"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := e.errOut.String(); !strings.Contains(got, "widen with --since 96h") {
		t.Fatalf("stderr = %q, want twice the window", got)
	}
}

// The alias must not be advertised: --wait is the name to teach.
func TestTimeoutAliasIsHidden(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("otp", "--help"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(e.out.String(), "--timeout") {
		t.Fatalf("help = %q, want --timeout hidden", e.out.String())
	}
}
