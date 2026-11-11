package client

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html"

	"github.com/noir017/gofer-cli/internal/model"
)

// Gofer has no JSON endpoint for accounts or folders, so they are read from
// the two pages that carry them in data attributes:
//
//   - /api/sidebar/mail (and /api/sidebar/accounts/{id}) names each account
//     with data-sidebar-account and data-sidebar-account-provider, and each
//     folder with data-mail-drop-folder, data-folder-role and
//     data-mail-drop-account.
//   - /compose/pane names the From choices of the composer with
//     data-account-id, data-account-email and data-identity-email, which is
//     the only compact page that says an account's address.
//
// Everything below reads those attributes only. The visible text is used for
// the one thing gofer gives no attribute for: the label of a folder.

// maxPageBytes caps how much of a page is read. The sidebar of an account with
// many folders is a few hundred kilobytes; this is far above that.
const maxPageBytes = 8 << 20

// Markers that tell a page this parser knows from one that changed shape. They
// sit on the page itself, not on the rows, so a page that still has them and
// lists nothing is a user without accounts or without folders.
const (
	sidebarBodyMarker    = "data-sidebar-app-body"
	sidebarAccountMarker = "data-sidebar-account-toggle"
	composePaneMarker    = "data-compose-pane"
)

// sidebarPage is what the sidebar says about the accounts and their folders.
type sidebarPage struct {
	Accounts []model.Account
	Folders  []model.Folder
}

func parseSidebarPage(r io.Reader) (sidebarPage, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return sidebarPage{}, err
	}
	var page sidebarPage
	var parseErr error
	walkHTML(doc, func(n *html.Node) bool {
		if parseErr != nil {
			return false
		}
		accountID := attr(n, "data-sidebar-account")
		if accountID == "" {
			return true
		}
		// The unified inbox is not an account: it has no provider and lists
		// virtual folders such as "inbox" and "starred".
		provider := attr(n, "data-sidebar-account-provider")
		if provider == "" {
			return false
		}
		name, err := accountLabel(n)
		if err != nil {
			parseErr = fmt.Errorf("account %s: %w", accountID, err)
			return false
		}
		page.Accounts = append(page.Accounts, model.Account{
			ID:       accountID,
			Name:     name,
			Provider: provider,
		})
		walkHTML(n, func(m *html.Node) bool {
			if parseErr != nil {
				return false
			}
			folderID := attr(m, "data-mail-drop-folder")
			if folderID == "" {
				return true
			}
			if attr(m, "data-mail-drop-account") != accountID {
				return false
			}
			label, err := folderLabel(m)
			if err != nil {
				parseErr = fmt.Errorf("folder %s of account %s: %w", folderID, accountID, err)
				return false
			}
			page.Folders = append(page.Folders, model.Folder{
				ID:        folderID,
				AccountID: accountID,
				Name:      label,
				Role:      normalizeFolderRole(attr(m, "data-folder-role")),
			})
			return false
		})
		return false
	})
	if parseErr != nil {
		return sidebarPage{}, parseErr
	}
	// A page that still has its containers but no account is a user without
	// accounts, which is an empty list and not a page this parser lost track
	// of.
	if len(page.Accounts) == 0 && !hasElementWithAttr(doc, sidebarBodyMarker) &&
		!hasElementWithAttr(doc, sidebarAccountMarker) {
		return sidebarPage{}, fmt.Errorf("not a mail sidebar: no element carries %s or %s",
			sidebarBodyMarker, sidebarAccountMarker)
	}
	return page, nil
}

// parseMessageFolder reads the folder a message sits in from its Move dialog.
// Every folder of the message's account is listed; the message's own folder
// is the one row the dialog disables. Other accounts' rows may be disabled
// for their own reasons, so only the source account's rows count.
func parseMessageFolder(r io.Reader) (string, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return "", err
	}
	list := findHTML(doc, func(n *html.Node) bool { return hasAttr(n, "data-mail-move-list") })
	if list == nil {
		return "", errors.New("not the move dialog: no element carries data-mail-move-list")
	}
	account := attr(list, "data-mail-move-source-account")
	if account == "" {
		return "", errors.New("not the move dialog: the list names no source account")
	}
	current := findHTML(list, func(n *html.Node) bool {
		return attr(n, "data-mail-move-folder") != "" &&
			attr(n, "data-mail-move-account") == account &&
			hasAttr(n, "disabled")
	})
	if current == nil {
		return "", errors.New("the move dialog marks no current folder")
	}
	return attr(current, "data-mail-move-folder"), nil
}

// composeAccount is one entry of the composer's From menu: an account, with
// the identities it may also send as.
type composeAccount struct {
	ID         string
	Email      string
	Name       string
	Identities []composeIdentity
}

type composeIdentity struct {
	ID        string
	Email     string
	Name      string
	AccountID string
}

func parseComposeAccounts(r io.Reader) ([]composeAccount, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	var accounts []composeAccount
	index := make(map[string]int)
	ensure := func(accountID string) int {
		if i, ok := index[accountID]; ok {
			return i
		}
		index[accountID] = len(accounts)
		accounts = append(accounts, composeAccount{ID: accountID})
		return len(accounts) - 1
	}
	walkHTML(doc, func(n *html.Node) bool {
		// The composer marks its From choices with an attribute that has no
		// value, so presence is what counts.
		if !hasAttr(n, "data-compose-account-item") {
			return true
		}
		accountID := attr(n, "data-account-id")
		if accountID == "" {
			return false
		}
		if email := attr(n, "data-account-email"); email != "" {
			i := ensure(accountID)
			accounts[i].Email = email
			accounts[i].Name = attr(n, "data-account-name")
			return false
		}
		if email := attr(n, "data-identity-email"); email != "" {
			i := ensure(accountID)
			accounts[i].Identities = append(accounts[i].Identities, composeIdentity{
				ID:        attr(n, "data-identity-id"),
				Email:     email,
				Name:      attr(n, "data-identity-name"),
				AccountID: accountID,
			})
		}
		return false
	})
	// The pane is there but lists no From choice when the user has no account
	// with mail, which is an empty list rather than a changed page.
	if !hasElementWithAttr(doc, composePaneMarker) {
		return nil, fmt.Errorf("not the compose pane: no element carries %s", composePaneMarker)
	}
	return accounts, nil
}

// mergeAccounts puts the addresses from the composer menu onto the account
// list of the sidebar, and keeps any account only the composer knows.
func mergeAccounts(sidebar []model.Account, compose []composeAccount) []model.Account {
	byID := make(map[string]composeAccount, len(compose))
	for _, account := range compose {
		byID[account.ID] = account
	}
	merged := make([]model.Account, 0, len(sidebar)+len(compose))
	seen := make(map[string]bool, len(sidebar))
	for _, account := range sidebar {
		seen[account.ID] = true
		if c, ok := byID[account.ID]; ok {
			account.Email = c.Email
			if strings.TrimSpace(account.Name) == "" {
				account.Name = c.Name
			}
		}
		merged = append(merged, account)
	}
	for _, c := range compose {
		if seen[c.ID] {
			continue
		}
		merged = append(merged, model.Account{ID: c.ID, Email: c.Email, Name: c.Name})
	}
	return merged
}

// resolveSender maps a From address to the account it belongs to and, when the
// address is an identity of that account, to the identity.
func resolveSender(accounts []composeAccount, from string) (accountID, identityID string, ok bool) {
	from = strings.TrimSpace(from)
	if from == "" {
		return "", "", true
	}
	for _, account := range accounts {
		if strings.EqualFold(strings.TrimSpace(account.Email), from) {
			return account.ID, "", true
		}
	}
	for _, account := range accounts {
		for _, identity := range account.Identities {
			if strings.EqualFold(strings.TrimSpace(identity.Email), from) {
				return account.ID, identity.ID, true
			}
		}
	}
	return "", "", false
}

// normalizeFolderRole maps gofer's folder roles to the names the model uses.
// IMAP servers call the spam folder junk, and gofer's combined folders call it
// spam; everything else is passed through, custom folders included.
func normalizeFolderRole(role string) string {
	if role == "junk" {
		return "spam"
	}
	return role
}

// accountLabel is the account name in its sidebar heading.
func accountLabel(n *html.Node) (string, error) {
	toggle := findHTML(n, func(m *html.Node) bool {
		return attr(m, "data-sidebar-account-toggle") != ""
	})
	if toggle == nil {
		return "", errors.New("no heading: no element carries data-sidebar-account-toggle")
	}
	return labelText(toggle)
}

// folderLabel is the folder name in its sidebar link.
func folderLabel(n *html.Node) (string, error) {
	return labelText(n)
}

// labelText is the text of the first span a sidebar row truncates, which is
// the label next to its icon. That span holds the label and nothing else, so
// anything besides text inside it means the page changed: the unread badge is
// another span of the row, and its count must never end up in the name.
func labelText(n *html.Node) (string, error) {
	label := findHTML(n, func(m *html.Node) bool {
		return m.Data == "span" && hasClass(m, "truncate")
	})
	if label == nil {
		return "", errors.New("no label: no span has class truncate")
	}
	var text strings.Builder
	for c := label.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.TextNode {
			return "", errors.New("no label: the span with class truncate holds more than text")
		}
		text.WriteString(c.Data)
	}
	if collapsed := collapseText(text.String()); collapsed != "" {
		return collapsed, nil
	}
	return "", errors.New("empty label: the span with class truncate holds no text")
}

func findHTML(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n.Type == html.ElementNode && match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findHTML(c, match); found != nil {
			return found
		}
	}
	return nil
}

// walkHTML calls visit for every element below n. visit returns whether to
// descend into that element.
func walkHTML(n *html.Node, visit func(*html.Node) bool) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && !visit(c) {
			continue
		}
		walkHTML(c, visit)
	}
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, name string) bool {
	for _, a := range n.Attr {
		if a.Key == name {
			return true
		}
	}
	return false
}

func hasElementWithAttr(n *html.Node, name string) bool {
	return findHTML(n, func(m *html.Node) bool { return hasAttr(m, name) }) != nil
}

func hasClass(n *html.Node, class string) bool {
	for _, field := range strings.Fields(attr(n, "class")) {
		if field == class {
			return true
		}
	}
	return false
}

func collapseText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
