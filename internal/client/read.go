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
		"q": {infoProbeQuery},
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
	resp, err := c.request(ctx, http.MethodGet, c.urlFor("/login", nil), nil, "", "", nil)
	if err != nil {
		return "", fmt.Errorf("read server version: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
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
	Query         string // free text plus key:value tokens, as in gofer's search box
	From          string // sender name or address
	To            string // To recipients only; cc: and bcc: come from Query
	Subject       string
	Body          string
	After         string   // YYYY-MM-DD
	Before        string   // YYYY-MM-DD
	AccountID     string   // "" = all accounts
	FolderIDs     []string // nil = every folder; wins over AccountID
	Unread        bool
	HasAttachment bool
	Limit         int // 0 = 20
}

// Search lists gofer's mail rows, newest first. Conversation grouping is the
// user's setting and is left alone: when it is on, a row can stand for a whole
// conversation, Thread is set and ID is that conversation's newest message.
func (c *Client) Search(ctx context.Context, opts SearchOptions) ([]model.MessageRef, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

	// The search box's key:value tokens become request parameters; explicit
	// fields win over tokens for the same filter.
	filters, err := parseSearchQuery(opts.Query)
	if err != nil {
		return nil, err
	}
	if value := strings.TrimSpace(opts.From); value != "" {
		filters.from = value
	}
	if value := strings.TrimSpace(opts.To); value != "" {
		filters.to = value
		filters.recipientType = "to"
	}
	if value := strings.TrimSpace(opts.Subject); value != "" {
		filters.subject = value
	}
	if value := strings.TrimSpace(opts.Body); value != "" {
		filters.body = value
	}
	if value := strings.TrimSpace(opts.After); value != "" {
		filters.after = value
	}
	if value := strings.TrimSpace(opts.Before); value != "" {
		filters.before = value
	}
	if opts.Unread {
		filters.unread = true
	}
	if opts.HasAttachment {
		filters.attachments = true
	}
	query := filters.params()
	// gofer accepts one scope at a time, and a folder pins the account.
	switch {
	case len(opts.FolderIDs) > 0:
		query.Set("scope", "folders")
		// The web UI searches a folder and what is inside it; do the same.
		query.Set("include_descendants", "1")
		for _, id := range opts.FolderIDs {
			query.Add("scope_folder_id", id)
		}
	case opts.AccountID != "":
		query.Set("scope", "account")
		query.Set("scope_account_id", opts.AccountID)
	}

	resp, err := c.do(ctx, http.MethodGet, "/mail/search/ids", query, nil, "")
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		discard(resp)
		switch {
		case len(opts.FolderIDs) > 0:
			return nil, fmt.Errorf("search: folder not found: %w", model.ErrNotFound)
		case opts.AccountID != "":
			return nil, fmt.Errorf("search: account not found: %w", model.ErrNotFound)
		default:
			return nil, fmt.Errorf("search: no such mailbox: %w", model.ErrNotFound)
		}
	default:
		return nil, responseError("search", resp)
	}
	defer func() { _ = resp.Body.Close() }()

	var payload struct {
		IDs []struct {
			ID       string `json:"id"`
			Account  string `json:"account"`
			FolderID string `json:"folder_id"`
			Thread   bool   `json:"thread"`
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
		refs = append(refs, model.MessageRef{
			ID:       item.ID,
			Account:  item.Account,
			FolderID: item.FolderID,
			Thread:   item.Thread,
		})
	}
	return refs, nil
}

// Source returns the message exactly as received (RFC 822).
func (c *Client) Source(ctx context.Context, id string) ([]byte, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("read message source: empty message id")
	}
	// JoinPath escapes the id as one path segment, so a slash or a question
	// mark in it cannot change the route.
	target, err := url.JoinPath(c.base.String(), "api", "messages", id, "source")
	if err != nil {
		return nil, fmt.Errorf("read message source: %w", err)
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("read message source: %w", err)
	}
	resp, err := c.doURL(ctx, http.MethodGet, u, nil, "")
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
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read message source: %w", err)
	}
	return data, nil
}
