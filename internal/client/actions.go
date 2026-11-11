package client

import (
	"context"

	"github.com/noir017/gofer-cli/internal/model"
)

type Action string

const (
	ActRead    Action = "read"
	ActUnread  Action = "unread"
	ActStar    Action = "star"
	ActUnstar  Action = "unstar"
	ActArchive Action = "archive"
	ActSpam    Action = "spam"
	ActDelete  Action = "delete" // to Trash, never permanent
)

func (c *Client) Act(ctx context.Context, action Action, ids []string) error {
	return errNotImplemented
}

// Move moves messages to folderID (a model.Folder ID).
func (c *Client) Move(ctx context.Context, ids []string, folderID string) error {
	return errNotImplemented
}

// Sync asks gofer to check for new mail now; accountID "" = all accounts.
func (c *Client) Sync(ctx context.Context, accountID string) error { return errNotImplemented }

func (c *Client) Accounts(ctx context.Context) ([]model.Account, error) {
	return nil, errNotImplemented
}

// Folders lists the folders of one account, or of all when accountID is "".
func (c *Client) Folders(ctx context.Context, accountID string) ([]model.Folder, error) {
	return nil, errNotImplemented
}

type SendRequest struct {
	From        string // account email or identity address; "" = the only account
	To, Cc, Bcc []string
	Subject     string
	Text        string
	Attachments []string // file paths
	ReplyTo     string   // gofer id of the message answered, for reply
}

func (c *Client) Send(ctx context.Context, req SendRequest) error { return errNotImplemented }
