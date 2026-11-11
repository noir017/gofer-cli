package client

import (
	"bytes"
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

type Action string

const (
	ActRead      Action = "read"
	ActUnread    Action = "unread"
	ActStar      Action = "star"
	ActUnstar    Action = "unstar"
	ActArchive   Action = "archive"
	ActUnarchive Action = "unarchive"
	ActSpam      Action = "spam"
	ActNotSpam   Action = "not-spam"
	ActDelete    Action = "delete" // to Trash, never permanent
)

type SendRequest struct {
	From        string // account email or identity address; "" = the only account
	To, Cc, Bcc []string
	Subject     string
	Text        string
	Attachments []string // file paths
	ReplyTo     string   // gofer id of the message answered, for reply
}

// bulkChunkSize is how many messages one bulk request carries. The web UI
// chunks at 200 as well, and gofer refuses more than 256 targets at once for
// accounts whose changes are queued.
const bulkChunkSize = 200

// Act applies action to the messages with the given gofer ids.
//
// ActDelete only ever moves messages to Trash. gofer's /api/messages/delete
// deletes a message that already sits in Trash for good, so delete is sent as
// a move to the combined "trash" folder instead, which never deletes anything.
func (c *Client) Act(ctx context.Context, action Action, ids []string) error {
	path, state, folderID, err := actionEndpoint(action)
	if err != nil {
		return err
	}
	return c.runBulk(ctx, string(action), path, ids, state, folderID)
}

// Move moves messages to folderID (a model.Folder ID).
func (c *Client) Move(ctx context.Context, ids []string, folderID string) error {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return errors.New("move: no destination folder")
	}
	return c.runBulk(ctx, "move", "/api/messages/move", ids, "", folderID)
}

// runBulk sends ids in batches and adds up what gofer reports.
func (c *Client) runBulk(ctx context.Context, label, path string, ids []string, state, folderID string) error {
	ids, err := cleanIDs(ids)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return runBulkChunks(label, ids, bulkChunkSize, func(chunk []string) (bulkResult, error) {
		body, err := bulkBody(chunk, state, folderID)
		if err != nil {
			return bulkResult{}, err
		}
		return c.postBulk(ctx, path, body)
	})
}

// runBulkChunks sends the ids batch by batch and stops at the first failure.
// The error then says how many messages had already been changed, counting the
// ones the failing batch changed before it reported the failure, since those
// changes stay.
func runBulkChunks(label string, ids []string, size int, send func([]string) (bulkResult, error)) error {
	applied := 0
	for _, chunk := range chunkIDs(ids, size) {
		result, err := send(chunk)
		applied += result.Updated
		if err == nil {
			err = bulkResultError(label, result)
		}
		if err != nil {
			if applied > 0 {
				return fmt.Errorf("%w; %d message(s) had already been changed", err, applied)
			}
			return err
		}
	}
	return nil
}

// Sync asks gofer to check for new mail now; accountID "" = all accounts.
// Gofer syncs in the background, so this returns once the sync has started,
// not when it has finished.
func (c *Client) Sync(ctx context.Context, accountID string) error {
	path := "/api/mail/sync"
	if accountID = strings.TrimSpace(accountID); accountID != "" {
		path = "/api/mail/sync/accounts/" + url.PathEscape(accountID)
	}
	resp, err := c.do(ctx, http.MethodPost, path, nil, nil, "")
	if err != nil {
		return err
	}
	defer closeActionBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return actionStatusError(path, resp)
	}
	return nil
}

// actionEndpoint maps an action to its bulk endpoint, the state field that
// endpoint reads and the destination folder, when the action is a move.
func actionEndpoint(action Action) (path, state, folderID string, err error) {
	switch action {
	case ActRead:
		return "/api/messages/read", "read", "", nil
	case ActUnread:
		return "/api/messages/read", "unread", "", nil
	case ActStar:
		return "/api/messages/star", "starred", "", nil
	case ActUnstar:
		return "/api/messages/star", "unstarred", "", nil
	case ActArchive:
		return "/api/messages/archive", "", "", nil
	case ActUnarchive:
		return "/api/messages/unarchive", "", "", nil
	case ActSpam:
		return "/api/messages/spam", "", "", nil
	case ActNotSpam:
		return "/api/messages/not-spam", "", "", nil
	case ActDelete:
		return "/api/messages/move", "", "trash", nil
	default:
		return "", "", "", fmt.Errorf("unknown action %q", action)
	}
}

// bulkRequest is the body the message bulk endpoints decode.
type bulkRequest struct {
	Targets  []bulkTarget `json:"targets"`
	State    string       `json:"state,omitempty"`
	FolderID string       `json:"folder_id,omitempty"`
}

type bulkTarget struct {
	ID string `json:"id"`
}

// bulkBody builds the JSON body of one bulk request.
func bulkBody(ids []string, state, folderID string) ([]byte, error) {
	ids, err := cleanIDs(ids)
	if err != nil {
		return nil, err
	}
	req := bulkRequest{State: state, FolderID: folderID, Targets: make([]bulkTarget, 0, len(ids))}
	for _, id := range ids {
		req.Targets = append(req.Targets, bulkTarget{ID: id})
	}
	return json.Marshal(req)
}

// cleanIDs trims the ids and drops the empty ones. A list with nothing left in
// it is an error: gofer refuses a request without targets, so an empty
// selection must not look like a success.
func cleanIDs(ids []string) ([]string, error) {
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			cleaned = append(cleaned, id)
		}
	}
	if len(cleaned) == 0 {
		return nil, errors.New("no message ids")
	}
	return cleaned, nil
}

func chunkIDs(ids []string, size int) [][]string {
	var chunks [][]string
	for start := 0; start < len(ids); start += size {
		end := start + size
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[start:end])
	}
	return chunks
}

// bulkResult is what a bulk endpoint answers with. Not every endpoint sends
// every count: archive reports no_archive, spam reports failed, and the rest
// only updated.
type bulkResult struct {
	Updated      int `json:"updated"`
	Messages     int `json:"messages"`
	NoArchive    int `json:"no_archive"`
	NoTrash      int `json:"no_trash"`
	Failed       int `json:"failed"`
	RemoteFailed int `json:"remote_failed"`
	// MissingFolder is the X-Gofer-Missing-Folder header, "<account id> <role>",
	// which names the folder an account lacks.
	MissingFolder string `json:"-"`
}

// missingFolderHeader names the first folder gofer found missing while it ran
// a bulk action.
const missingFolderHeader = "X-Gofer-Missing-Folder"

func (c *Client) postBulk(ctx context.Context, path string, body []byte) (bulkResult, error) {
	resp, err := c.do(ctx, http.MethodPost, path, nil, bytes.NewReader(body), "application/json")
	if err != nil {
		return bulkResult{}, err
	}
	defer closeActionBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return bulkResult{}, actionStatusError(path, resp)
	}
	var result bulkResult
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return bulkResult{}, err
	}
	result, err = parseBulkAnswer(data, resp.Header.Get(missingFolderHeader))
	if err != nil {
		return bulkResult{}, fmt.Errorf("%s: %w", strings.TrimPrefix(path, "/api/"), err)
	}
	return result, nil
}

// parseBulkAnswer reads the counts a bulk endpoint answered with. An empty
// body means the endpoint reported nothing, which is not an error; anything
// else must be the JSON the endpoint documents.
func parseBulkAnswer(data []byte, missingFolder string) (bulkResult, error) {
	var result bulkResult
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &result); err != nil {
			return bulkResult{}, fmt.Errorf("unexpected answer %q: %w", strings.TrimSpace(string(data)), err)
		}
	}
	result.MissingFolder = missingFolder
	return result, nil
}

// bulkResultError turns the counts of a successful answer into an error when
// gofer changed nothing: a message it could not change, or an account without
// the folder the action needs. Gofer reports both with HTTP 200.
func bulkResultError(label string, result bulkResult) error {
	if result.Failed > 0 {
		return fmt.Errorf("%s: %d message(s) could not be changed", label, result.Failed)
	}
	if result.Updated > 0 {
		return nil
	}
	switch {
	case result.NoArchive > 0:
		return fmt.Errorf("%s: nothing archived: %s", label,
			missingFolderReason(result.MissingFolder, "archive", result.NoArchive))
	case result.NoTrash > 0:
		return fmt.Errorf("%s: nothing deleted: %s", label,
			missingFolderReason(result.MissingFolder, "trash", result.NoTrash))
	case result.MissingFolder != "":
		return fmt.Errorf("%s: nothing changed: %s", label,
			missingFolderReason(result.MissingFolder, "", 1))
	}
	return nil
}

// missingFolderReason names the folder gofer missed: the account it named in
// the header when it named one, the count otherwise.
func missingFolderReason(header, role string, count int) string {
	if fields := strings.Fields(header); len(fields) == 2 {
		return fmt.Sprintf("account %s has no %s folder", fields[0], fields[1])
	}
	if role == "" {
		role = "matching"
	}
	if count == 1 {
		return fmt.Sprintf("an account has no %s folder", role)
	}
	return fmt.Sprintf("%d accounts have no %s folder", count, role)
}

// closeActionBody drains and closes a response body so the connection can be
// reused.
func closeActionBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
}

// actionStatusError turns a refused request into an error, keeping gofer's own
// explanation, which is the only detail it gives about a bulk action.
func actionStatusError(path string, resp *http.Response) error {
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	text := strings.TrimSpace(string(detail))
	if text == "" {
		text = http.StatusText(resp.StatusCode)
	}
	err := fmt.Errorf("%s: %s (HTTP %d)", strings.TrimPrefix(path, "/api/"), text, resp.StatusCode)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %v", model.ErrNotFound, err)
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %v", model.ErrAuth, err)
	}
	return err
}
