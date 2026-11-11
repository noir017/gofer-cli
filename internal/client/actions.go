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
	for _, chunk := range chunkIDs(ids, bulkChunkSize) {
		body, err := bulkBody(chunk, state, folderID)
		if err != nil {
			return err
		}
		if err := c.postBulk(ctx, path, body); err != nil {
			return err
		}
	}
	return nil
}

// Move moves messages to folderID (a model.Folder ID).
func (c *Client) Move(ctx context.Context, ids []string, folderID string) error {
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return errors.New("move: no destination folder")
	}
	for _, chunk := range chunkIDs(ids, bulkChunkSize) {
		body, err := bulkBody(chunk, "", folderID)
		if err != nil {
			return err
		}
		if err := c.postBulk(ctx, "/api/messages/move", body); err != nil {
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

// bulkBody builds the JSON body of one bulk request. Empty ids are dropped;
// an id list with nothing left in it is an error, since gofer refuses a
// request without targets.
func bulkBody(ids []string, state, folderID string) ([]byte, error) {
	req := bulkRequest{State: state, FolderID: folderID, Targets: make([]bulkTarget, 0, len(ids))}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			req.Targets = append(req.Targets, bulkTarget{ID: id})
		}
	}
	if len(req.Targets) == 0 {
		return nil, errors.New("no message ids")
	}
	return json.Marshal(req)
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

func (c *Client) postBulk(ctx context.Context, path string, body []byte) error {
	resp, err := c.do(ctx, http.MethodPost, path, nil, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer closeActionBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return actionStatusError(path, resp)
	}
	return nil
}

// closeActionBody drains and closes a response body so the connection can be
// reused.
func closeActionBody(resp *http.Response) {
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
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
