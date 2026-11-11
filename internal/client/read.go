package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/noir017/gofer-cli/internal/model"
)

// infoProbeQuery is a search that matches nothing; it is only there to make
// gofer check the session.
const infoProbeQuery = "goferclidoctorprobe"

var versionPattern = regexp.MustCompile(`data-public-version[^>]*>\s*([^<]+?)\s*<`)

// Info reports the server version and the signed-in user, logging in if needed.
func (c *Client) Info(ctx context.Context) (model.ServerInfo, error) {
	info := model.ServerInfo{URL: c.cfg.URL, User: c.cfg.User}

	// Searching for an impossible term is a cheap authenticated request.
	probe, err := c.do(ctx, http.MethodGet, "/mail/search/ids", url.Values{
		"q":          {infoProbeQuery},
		"no_threads": {"1"},
	}, nil, "")
	if err != nil {
		return info, err
	}
	if probe.StatusCode != http.StatusOK {
		return info, responseError("server check", probe)
	}
	discard(probe)

	version, err := c.serverVersion(ctx)
	if err != nil {
		return info, err
	}
	info.Version = version
	return info, nil
}

// serverVersion reads the version label every page shown before sign-in
// carries (internal/views/public_corners.templ in gofer).
func (c *Client) serverVersion(ctx context.Context) (string, error) {
	resp, err := c.request(ctx, http.MethodGet, "/login", nil, nil, "", "", nil)
	if err != nil {
		return "", fmt.Errorf("read server version: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read server version: %w", err)
	}
	match := versionPattern.FindSubmatch(body)
	if match == nil {
		return "", nil
	}
	return strings.TrimPrefix(strings.TrimSpace(string(match[1])), "Gofer "), nil
}

type SearchOptions struct {
	Query     string // gofer search syntax: to: from: subject: account: after: ...
	AccountID string // "" = all accounts
	Unread    bool
	Limit     int // 0 = 20
}

// Search lists messages, newest first, one row per message (never a thread).
func (c *Client) Search(ctx context.Context, opts SearchOptions) ([]model.MessageRef, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	query := url.Values{
		"q":          {opts.Query},
		"no_threads": {"1"},
		// Pin the order: the web UI otherwise uses the user's saved sort.
		"sort_by":    {"date"},
		"sort_order": {"desc"},
	}
	if opts.Unread {
		query.Set("unread", "1")
	}
	if opts.AccountID != "" {
		query.Set("scope", "account")
		query.Set("scope_account_id", opts.AccountID)
	}

	resp, err := c.do(ctx, http.MethodGet, "/mail/search/ids", query, nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("search", resp)
	}
	defer resp.Body.Close()

	var payload struct {
		IDs []struct {
			ID       string `json:"id"`
			Account  string `json:"account"`
			FolderID string `json:"folder_id"`
		} `json:"ids"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("search: decode response: %w", err)
	}

	refs := make([]model.MessageRef, 0, min(limit, len(payload.IDs)))
	for _, item := range payload.IDs {
		if len(refs) == limit {
			break
		}
		refs = append(refs, model.MessageRef{ID: item.ID, Account: item.Account, FolderID: item.FolderID})
	}
	return refs, nil
}

// Source returns the message exactly as received (RFC 822).
func (c *Client) Source(ctx context.Context, id string) ([]byte, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("read message source: empty message id")
	}
	resp, err := c.do(ctx, http.MethodGet, "/api/messages/"+url.PathEscape(id)+"/source", nil, nil, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		discard(resp)
		return nil, fmt.Errorf("message %s: %w", id, model.ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, responseError("read message source", resp)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read message source: %w", err)
	}
	return data, nil
}
