package client

import (
	"context"

	"github.com/noir017/gofer-cli/internal/model"
)

func (c *Client) Login(ctx context.Context) error  { return errNotImplemented }
func (c *Client) Logout(ctx context.Context) error { return errNotImplemented }

// Info reports the server version and the signed-in user, logging in if needed.
func (c *Client) Info(ctx context.Context) (model.ServerInfo, error) {
	return model.ServerInfo{}, errNotImplemented
}

type SearchOptions struct {
	Query     string // gofer search syntax: to: from: subject: account: after: ...
	AccountID string // "" = all accounts
	Unread    bool
	Limit     int // 0 = 20
}

// Search lists messages, newest first, one row per message (never a thread).
func (c *Client) Search(ctx context.Context, opts SearchOptions) ([]model.MessageRef, error) {
	return nil, errNotImplemented
}

// Source returns the message exactly as received (RFC 822).
func (c *Client) Source(ctx context.Context, id string) ([]byte, error) {
	return nil, errNotImplemented
}
