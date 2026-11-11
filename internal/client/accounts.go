package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/noir017/gofer-cli/internal/model"
)

// Accounts lists the accounts of the signed-in user. Gofer only tells an
// account's address on the composer's From menu, so that page is read as well
// as the sidebar.
//
// The account of the local folders, whose provider is "local", never appears
// on that menu: it has no mail server and no address to send from, so its
// Email stays empty.
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
		if accountID != "" && errors.Is(err, model.ErrNotFound) {
			return nil, c.foldersNotFound(ctx, accountID, err)
		}
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

// foldersNotFound explains a 404 on one account's sidebar section. Gofer
// answers 404 for an account it cannot show, which is the account of a mail
// sync that is turned off as well as one that does not exist, so the two are
// told apart by asking which accounts exist.
func (c *Client) foldersNotFound(ctx context.Context, accountID string, cause error) error {
	accounts, err := c.Accounts(ctx)
	if err == nil {
		for _, account := range accounts {
			if account.ID == accountID {
				return fmt.Errorf("folders of account %s: mail sync is off for this account, or it is being deleted, "+
					"so gofer shows no folders for it: %w", accountID, cause)
			}
		}
	}
	return fmt.Errorf("folders of account %s: no such account: %w", accountID, cause)
}

func (c *Client) loadSidebar(ctx context.Context, path string) (sidebarPage, error) {
	body, err := c.getPage(ctx, path)
	if err != nil {
		return sidebarPage{}, err
	}
	page, err := parseSidebarPage(strings.NewReader(string(body)))
	if err != nil {
		return sidebarPage{}, fmt.Errorf("%s: %w", path, err)
	}
	return page, nil
}

func (c *Client) loadComposeAccounts(ctx context.Context) ([]composeAccount, error) {
	body, err := c.getPage(ctx, "/compose/pane")
	if err != nil {
		return nil, err
	}
	accounts, err := parseComposeAccounts(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("/compose/pane: %w", err)
	}
	return accounts, nil
}

func (c *Client) folderUnread(ctx context.Context) (map[string]int, error) {
	body, err := c.getPage(ctx, "/api/folders/unread")
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	if err := json.Unmarshal(body, &counts); err != nil {
		return nil, fmt.Errorf("/api/folders/unread: %w", err)
	}
	return counts, nil
}

func (c *Client) getPage(ctx context.Context, path string) ([]byte, error) {
	return c.getPageQuery(ctx, path, nil)
}

// getPageQuery is getPage with request parameters. The parameters must go
// through here rather than be pasted into path: urlFor sets Path, which
// escapes a "?" in it, so the server would see one long path and answer 404.
func (c *Client) getPageQuery(ctx context.Context, path string, query url.Values) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, path, query, nil, "")
	if err != nil {
		return nil, err
	}
	defer closeActionBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, actionStatusError(path, resp)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
}
