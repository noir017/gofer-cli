package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/noir017/gofer-cli/internal/model"
)

// Accounts lists the accounts of the signed-in user. Gofer only tells an
// account's address on the composer's From menu, so that page is read as well
// as the sidebar.
func (c *Client) Accounts(ctx context.Context) ([]model.Account, error) {
	page, err := c.loadSidebar(ctx, "/api/sidebar/mail")
	if err != nil {
		return nil, err
	}
	compose, err := c.loadComposeAccounts(ctx)
	if err != nil {
		return nil, err
	}
	return mergeAccounts(page.Accounts, compose), nil
}

// Folders lists the folders of one account, or of all when accountID is "".
// Unread counts come from /api/folders/unread, which is JSON and also counts
// the folders whose badge the sidebar leaves out.
func (c *Client) Folders(ctx context.Context, accountID string) ([]model.Folder, error) {
	accountID = strings.TrimSpace(accountID)
	path := "/api/sidebar/mail"
	if accountID != "" {
		path = "/api/sidebar/accounts/" + url.PathEscape(accountID)
	}
	page, err := c.loadSidebar(ctx, path)
	if err != nil {
		return nil, err
	}
	folders := make([]model.Folder, 0, len(page.Folders))
	for _, folder := range page.Folders {
		if accountID != "" && folder.AccountID != accountID {
			continue
		}
		folders = append(folders, folder)
	}
	unread, err := c.folderUnread(ctx)
	if err != nil {
		return nil, err
	}
	for i := range folders {
		folders[i].Unread = unread[folders[i].ID]
	}
	return folders, nil
}

func (c *Client) loadSidebar(ctx context.Context, path string) (sidebarPage, error) {
	body, err := c.getPage(ctx, path)
	if err != nil {
		return sidebarPage{}, err
	}
	return parseSidebarPage(strings.NewReader(string(body)))
}

func (c *Client) loadComposeAccounts(ctx context.Context) ([]composeAccount, error) {
	body, err := c.getPage(ctx, "/compose/pane")
	if err != nil {
		return nil, err
	}
	return parseComposeAccounts(strings.NewReader(string(body)))
}

func (c *Client) folderUnread(ctx context.Context) (map[string]int, error) {
	body, err := c.getPage(ctx, "/api/folders/unread")
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	if err := json.Unmarshal(body, &counts); err != nil {
		return nil, err
	}
	return counts, nil
}

func (c *Client) getPage(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil, nil, "")
	if err != nil {
		return nil, err
	}
	defer closeActionBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, actionStatusError(path, resp)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
}
