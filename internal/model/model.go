// Package model holds the types shared by the client, the message parser and
// the command line.
package model

import (
	"errors"
	"time"
)

// Errors the command line maps to exit codes. Wrap them with fmt.Errorf("...: %w", ErrX).
var (
	ErrNotFound = errors.New("not found")    // exit 2: no match, or --wait timed out
	ErrAuth     = errors.New("login failed") // exit 3: bad credentials, MFA needed, session refused
	ErrConfig   = errors.New("not configured")
)

// MessageRef is one row as gofer's search lists it. When Thread is set the
// row stands for a conversation and ID is its newest message; the other
// members of the conversation are not listed separately.
type MessageRef struct {
	ID       string `json:"id"`
	Account  string `json:"account"`
	FolderID string `json:"folder_id,omitempty"`
	Thread   bool   `json:"thread,omitempty"`
}

type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email"`
}

type Attachment struct {
	Index       int    `json:"index"` // 1-based, in the order Parse finds them
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	Inline      bool   `json:"inline,omitempty"`
}

// Message is a message parsed from its raw source.
type Message struct {
	ID          string       `json:"id"` // gofer's id, set by the caller
	Account     string       `json:"account,omitempty"`
	FolderID    string       `json:"folder_id,omitempty"`
	Folder      string       `json:"folder,omitempty"`     // folder name, when the caller could name it
	Thread      bool         `json:"thread,omitempty"`     // the row stands for a conversation
	MessageID   string       `json:"message_id,omitempty"` // Message-ID header
	InReplyTo   string       `json:"in_reply_to,omitempty"`
	Date        time.Time    `json:"date"`
	From        []Address    `json:"from"`
	To          []Address    `json:"to"`
	Cc          []Address    `json:"cc,omitempty"`
	ReplyTo     []Address    `json:"reply_to,omitempty"`
	Subject     string       `json:"subject"`
	Text        string       `json:"text"`           // text/plain, or the HTML part turned into text
	HTML        string       `json:"html,omitempty"` // text/html as sent
	Attachments []Attachment `json:"attachments,omitempty"`
}

type Account struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name,omitempty"`
	Provider string `json:"provider,omitempty"` // imap, gmail, outlook, jmap...
}

type Folder struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id"`
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Role      string `json:"role,omitempty"` // inbox, sent, trash, spam, archive, drafts
	Unread    int    `json:"unread"`
}

// ServerInfo is what doctor reports about the server and the session.
type ServerInfo struct {
	URL     string `json:"url"`
	Version string `json:"version,omitempty"`
	User    string `json:"user,omitempty"`
}
