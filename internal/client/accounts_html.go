package client

import (
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
	walkHTML(doc, func(n *html.Node) bool {
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
		page.Accounts = append(page.Accounts, model.Account{
			ID:       accountID,
			Name:     accountLabel(n),
			Provider: provider,
		})
		walkHTML(n, func(m *html.Node) bool {
			folderID := attr(m, "data-mail-drop-folder")
			if folderID == "" {
				return true
			}
			if attr(m, "data-mail-drop-account") != accountID {
				return false
			}
			page.Folders = append(page.Folders, model.Folder{
				ID:        folderID,
				AccountID: accountID,
				Name:      folderLabel(m),
				Role:      normalizeFolderRole(attr(m, "data-folder-role")),
			})
			return false
		})
		return false
	})
	return page, nil
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
func accountLabel(n *html.Node) string {
	toggle := findHTML(n, func(m *html.Node) bool {
		return attr(m, "data-sidebar-account-toggle") != ""
	})
	if toggle == nil {
		return ""
	}
	return labelText(toggle)
}

// folderLabel is the folder name in its sidebar link.
func folderLabel(n *html.Node) string {
	return labelText(n)
}

// labelText is the text of the first span a sidebar row truncates, which is
// the label next to its icon.
func labelText(n *html.Node) string {
	label := findHTML(n, func(m *html.Node) bool {
		return m.Data == "span" && hasClass(m, "truncate")
	})
	if label == nil {
		return collapseText(nodeText(n))
	}
	return collapseText(nodeText(label))
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

func hasClass(n *html.Node, class string) bool {
	for _, field := range strings.Fields(attr(n, "class")) {
		if field == class {
			return true
		}
	}
	return false
}

func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c))
	}
	return b.String()
}

func collapseText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
