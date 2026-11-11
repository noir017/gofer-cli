package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/noir017/gofer-cli/internal/config"
	"github.com/noir017/gofer-cli/internal/model"
)

// testdata/sidebar_mail.html is a fragment of a real /api/sidebar/mail page,
// cut down to the unified inbox and two accounts, with every address and
// account id replaced by example.com ones.
func openTestdata(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestParseSidebarPageAccounts(t *testing.T) {
	page, err := parseSidebarPage(openTestdata(t, "sidebar_mail.html"))
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Account{
		{ID: "acc_alice_example_com", Email: "", Name: "alice@example.com", Provider: "imap"},
		{ID: "acc_carol_example_com", Email: "", Name: "carol@example.com", Provider: "outlook"},
	}
	if len(page.Accounts) != len(want) {
		t.Fatalf("accounts = %+v, want %+v", page.Accounts, want)
	}
	for i := range want {
		if page.Accounts[i] != want[i] {
			t.Errorf("account %d = %+v, want %+v", i, page.Accounts[i], want[i])
		}
	}
}

func TestParseSidebarPageFolders(t *testing.T) {
	page, err := parseSidebarPage(openTestdata(t, "sidebar_mail.html"))
	if err != nil {
		t.Fatal(err)
	}
	// Seven folders on the IMAP account, eight on the Outlook one. The
	// unified inbox lists seven more, all virtual, and must not appear.
	if len(page.Folders) != 15 {
		t.Fatalf("got %d folders, want 15: %+v", len(page.Folders), page.Folders)
	}
	for _, folder := range page.Folders {
		if folder.AccountID == "" || folder.ID == "" {
			t.Fatalf("folder without an account: %+v", folder)
		}
		if folder.AccountID == "__unified__" || folder.ID == "inbox" {
			t.Fatalf("virtual folder kept: %+v", folder)
		}
	}

	want := []model.Folder{
		{
			ID:        "acc_alice_example_com_f_85d682f58c7881455544f747c58dc64d",
			AccountID: "acc_alice_example_com",
			Name:      "收件箱",
			Role:      "inbox",
		},
		{
			ID:        "acc_alice_example_com_f_4251a520c8b37463ae0c1c1f0d1966be",
			AccountID: "acc_alice_example_com",
			Name:      "垃圾邮件",
			Role:      "spam", // gofer calls an IMAP spam folder junk
		},
		{
			ID:        "acc_alice_example_com_f_1b19fb1c69a1c6f6be74a126e37a10e8",
			AccountID: "acc_alice_example_com",
			Name:      "Projects",
			Role:      "custom",
		},
		{
			ID:        "acc_carol_example_com_f_1cd5b8e01a8f508c22389f9b0a191147",
			AccountID: "acc_carol_example_com",
			Name:      "Receipts",
			Role:      "custom",
		},
	}
	for _, w := range want {
		found := false
		for _, folder := range page.Folders {
			if folder.ID == w.ID {
				found = true
				if folder != w {
					t.Errorf("folder %s = %+v, want %+v", w.ID, folder, w)
				}
			}
		}
		if !found {
			t.Errorf("folder %s missing", w.ID)
		}
	}
}

func TestFoldersSurvivesUnreadCountFailure(t *testing.T) {
	// The unread badge endpoint is separate from the folder list. When it
	// fails the folders are still there, so the listing keeps them with no
	// counts instead of failing as a whole.
	page, err := os.ReadFile("testdata/sidebar_mail.html")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sidebar/mail", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /api/folders/unread", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "counts unavailable", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(config.Config{URL: srv.URL, User: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	c.token = "test"

	folders, err := c.Folders(context.Background(), "")
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}
	if len(folders) != 15 {
		t.Fatalf("got %d folders, want 15", len(folders))
	}
	for _, folder := range folders {
		if folder.Unread != 0 {
			t.Errorf("folder %s unread = %d, want 0 without the count endpoint", folder.ID, folder.Unread)
		}
	}
}

func TestFolderLabelSkipsUnreadBadge(t *testing.T) {
	// The saved page has no unread badges, so the shape of a row that has one
	// is spelled out here: the badge is a span after the label.
	doc, err := html.Parse(strings.NewReader(`<div>
		<a data-folder-role="inbox" data-mail-drop-folder="f1" data-mail-drop-account="a1">
			<span class="flex size-5"><svg></svg></span>
			<span class="flex-1 truncate">Inbox</span>
			<span data-folder-unread="f1" class="min-w-5 h-5 px-1.5">42</span>
		</a></div>`))
	if err != nil {
		t.Fatal(err)
	}
	link := findHTML(doc, func(n *html.Node) bool { return attr(n, "data-mail-drop-folder") != "" })
	label, err := folderLabel(link)
	if err != nil {
		t.Fatal(err)
	}
	if label != "Inbox" {
		t.Fatalf("label = %q, want Inbox", label)
	}
}

func TestFolderLabelRejectsNestedText(t *testing.T) {
	// A label that no longer holds its own text is a page this parser does not
	// know, not a label to guess at.
	doc, err := html.Parse(strings.NewReader(`<a data-mail-drop-folder="f1" data-mail-drop-account="a1">
		<span class="flex-1 truncate"><b>Inbox</b> 42</span></a>`))
	if err != nil {
		t.Fatal(err)
	}
	link := findHTML(doc, func(n *html.Node) bool { return attr(n, "data-mail-drop-folder") != "" })
	if _, err := folderLabel(link); err == nil {
		t.Fatal("nested label accepted")
	}

	doc, err = html.Parse(strings.NewReader(`<a data-mail-drop-folder="f1" data-mail-drop-account="a1">
		<span class="flex size-5">Inbox</span></a>`))
	if err != nil {
		t.Fatal(err)
	}
	link = findHTML(doc, func(n *html.Node) bool { return attr(n, "data-mail-drop-folder") != "" })
	if _, err := folderLabel(link); err == nil {
		t.Fatal("row without a truncate span accepted")
	}
}

func TestParseSidebarPageAllowsAnEmptyAccountList(t *testing.T) {
	// The container is there, so the page is the mail sidebar of a user who
	// has no account yet.
	page, err := parseSidebarPage(strings.NewReader(`<div data-sidebar-app-body="mail"><nav></nav></div>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Accounts) != 0 || len(page.Folders) != 0 {
		t.Fatalf("page = %+v, want no accounts and no folders", page)
	}
}

func TestParseComposeAccountsAllowsAnEmptyList(t *testing.T) {
	// A user with no account that has mail still gets the compose pane, with
	// no From choice in it.
	accounts, err := parseComposeAccounts(strings.NewReader(`<div data-compose-pane><form></form></div>`))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts = %+v, want none", accounts)
	}
}

func TestParseSidebarPageRejectsUnknownPage(t *testing.T) {
	_, err := parseSidebarPage(strings.NewReader(`<html><body><nav><a href="/">Mail</a></nav></body></html>`))
	if err == nil {
		t.Fatal("a page without accounts parsed as empty")
	}
	if !strings.Contains(err.Error(), "data-sidebar-app-body") {
		t.Fatalf("err = %v, want it to name the missing container", err)
	}
}

func TestParseComposeAccountsRejectsUnknownPage(t *testing.T) {
	_, err := parseComposeAccounts(strings.NewReader(`<html><body><form></form></body></html>`))
	if err == nil {
		t.Fatal("a page without From choices parsed as empty")
	}
	if !strings.Contains(err.Error(), "data-compose-pane") {
		t.Fatalf("err = %v, want it to name the missing container", err)
	}
}

func TestParseComposeAccounts(t *testing.T) {
	accounts, err := parseComposeAccounts(openTestdata(t, "compose_pane.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 4 {
		t.Fatalf("got %d accounts, want 4: %+v", len(accounts), accounts)
	}
	want := []composeAccount{
		{ID: "acc_alice_example_com", Email: "alice@example.com", Name: "alice@example.com"},
		{ID: "acc_bob_example_com", Email: "bob@example.com", Name: "bob@example.com", Identities: []composeIdentity{
			{ID: "idn_1f29b89c07a6455fb190b6045233d81e", Email: "bob.alt@example.com", Name: "Alias One", AccountID: "acc_bob_example_com"},
			{ID: "idn_987d1265e35b4a9b846e28f73e5c0556", Email: "bob.news@example.com", Name: "Alias Two", AccountID: "acc_bob_example_com"},
		}},
		{ID: "acc_carol_example_com", Email: "carol@example.com", Name: "carol@example.com"},
		{ID: "acc_dave_example_com", Email: "dave@example.com", Name: "dave@example.com"},
	}
	if len(accounts) != len(want) {
		t.Fatalf("accounts = %+v, want %+v", accounts, want)
	}
	for i := range want {
		got := accounts[i]
		if got.ID != want[i].ID || got.Email != want[i].Email || got.Name != want[i].Name {
			t.Errorf("account %d = %+v, want %+v", i, got, want[i])
		}
		if len(got.Identities) != len(want[i].Identities) {
			t.Errorf("account %s identities = %+v, want %+v", got.ID, got.Identities, want[i].Identities)
			continue
		}
		for j := range want[i].Identities {
			if got.Identities[j] != want[i].Identities[j] {
				t.Errorf("identity %d of %s = %+v, want %+v", j, got.ID, got.Identities[j], want[i].Identities[j])
			}
		}
	}
}

func TestMergeAccounts(t *testing.T) {
	sidebar := []model.Account{
		{ID: "acc_one", Name: "One", Provider: "imap"},
		{ID: "acc_two", Name: "", Provider: "local"},
	}
	compose := []composeAccount{
		{ID: "acc_two", Email: "two@example.com", Name: "Two"},
		{ID: "acc_three", Email: "three@example.com", Name: "Three"},
	}
	got := mergeAccounts(sidebar, compose)
	want := []model.Account{
		{ID: "acc_one", Email: "", Name: "One", Provider: "imap"},
		{ID: "acc_two", Email: "two@example.com", Name: "Two", Provider: "local"},
		{ID: "acc_three", Email: "three@example.com", Name: "Three"},
	}
	if len(got) != len(want) {
		t.Fatalf("merged = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("account %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestResolveSender(t *testing.T) {
	accounts := []composeAccount{
		{ID: "acc_one", Email: "one@example.com", Identities: []composeIdentity{
			{ID: "idn_1", Email: "alias@example.com", AccountID: "acc_one"},
		}},
	}
	tests := []struct {
		from, account, identity string
		ok                      bool
	}{
		{"", "", "", true},
		{"one@example.com", "acc_one", "", true},
		{"One@Example.COM", "acc_one", "", true},
		{"alias@example.com", "acc_one", "idn_1", true},
		{"nobody@example.com", "", "", false},
	}
	for _, tt := range tests {
		account, identity, ok := resolveSender(accounts, tt.from)
		if account != tt.account || identity != tt.identity || ok != tt.ok {
			t.Errorf("resolveSender(%q) = (%q, %q, %t), want (%q, %q, %t)",
				tt.from, account, identity, ok, tt.account, tt.identity, tt.ok)
		}
	}
}

func TestNormalizeFolderRole(t *testing.T) {
	for role, want := range map[string]string{
		"inbox":  "inbox",
		"junk":   "spam",
		"trash":  "trash",
		"custom": "custom",
	} {
		if got := normalizeFolderRole(role); got != want {
			t.Errorf("normalizeFolderRole(%q) = %q, want %q", role, got, want)
		}
	}
}
