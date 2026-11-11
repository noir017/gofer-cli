// Package client talks to a gofer server over HTTP, the way its web UI does.
//
// Gofer has no public API. The client keeps to endpoints that return JSON or
// raw mail, and sends X-Gofer-Request: 1 on writes, which gofer accepts from
// clients that are not browsers (internal/httpguard in gofer).
package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/noir017/gofer-cli/internal/config"
)

var errNotImplemented = errors.New("not implemented")

type Client struct {
	cfg  config.Config
	base *url.URL
	http *http.Client
}

// New returns a client for cfg. It does not touch the network.
func New(cfg config.Config) (*Client, error) {
	return nil, errNotImplemented
}

// do sends one request with the session cookie, logging in first when there
// is no session and once more when the server answers 401. Writes carry
// X-Gofer-Request: 1. The caller closes the body.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	return nil, errNotImplemented
}
