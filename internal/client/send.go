package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// maxAttachmentBytes is what gofer accepts for one attachment.
const maxAttachmentBytes = 25 << 20

// replySource is what GET /api/compose/source says about the message being
// answered.
type replySource struct {
	AccountID  string `json:"account_id"`
	IdentityID string `json:"identity_id"`
	MessageID  string `json:"message_id"`
	References string `json:"references"`
	Subject    string `json:"subject"`
	FromEmail  string `json:"from_email"`
}

// composeAttachment is what POST /compose/attachments answers with.
type composeAttachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// Send queues a message for delivery: gofer answers as soon as it has queued
// it and sends it in the background, so a delivery failure shows up later, in
// the account's outgoing mail, not here.
//
// With ReplyTo set, gofer is told which message is answered, so the new one is
// threaded with it. The reply text is the caller's; nothing is quoted here.
func (c *Client) Send(ctx context.Context, req SendRequest) error {
	if len(req.To) == 0 && strings.TrimSpace(req.ReplyTo) == "" {
		return errors.New("send: no recipients")
	}
	reply, err := c.replySource(ctx, req.ReplyTo)
	if err != nil {
		return err
	}

	accountID, identityID := "", ""
	switch {
	case strings.TrimSpace(req.From) != "":
		accounts, err := c.loadComposeAccounts(ctx)
		if err != nil {
			return err
		}
		ok := false
		accountID, identityID, ok = resolveSender(accounts, req.From)
		if !ok {
			return fmt.Errorf("send: %q is not an address of any account", strings.TrimSpace(req.From))
		}
	case reply != nil:
		accountID, identityID = reply.AccountID, reply.IdentityID
	}

	uploads, err := c.uploadAttachments(ctx, req.Attachments)
	if err != nil {
		return err
	}

	form := composeForm(req, reply, accountID, identityID, uploads)
	resp, err := c.do(ctx, http.MethodPost, "/compose", nil,
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	defer closeActionBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return actionStatusError("/compose", resp)
	}
	return nil
}

// replySource reads the message a reply answers, or nil when replyTo is empty.
func (c *Client) replySource(ctx context.Context, replyTo string) (*replySource, error) {
	replyTo = strings.TrimSpace(replyTo)
	if replyTo == "" {
		return nil, nil
	}
	query := url.Values{"email_id": {replyTo}}
	body, err := c.getPage(ctx, "/api/compose/source?"+query.Encode())
	if err != nil {
		return nil, err
	}
	var source replySource
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, fmt.Errorf("compose source of message %s: %w", replyTo, err)
	}
	return &source, nil
}

// composeForm builds the form POST /compose reads. accountID "" lets gofer
// send from its first account.
func composeForm(req SendRequest, reply *replySource, accountID, identityID string, uploads []composeAttachment) url.Values {
	form := url.Values{}
	if accountID != "" {
		form.Set("account_id", accountID)
	}
	if identityID != "" {
		form.Set("identity_id", identityID)
	}

	to := joinAddresses(req.To)
	if to == "" && reply != nil {
		to = reply.FromEmail
	}
	form.Set("to", to)
	if cc := joinAddresses(req.Cc); cc != "" {
		form.Set("cc", cc)
	}
	if bcc := joinAddresses(req.Bcc); bcc != "" {
		form.Set("bcc", bcc)
	}

	subject := req.Subject
	if reply != nil {
		subject = firstNonEmpty(subject, reply.Subject)
		subject = replySubject(subject)
	}
	if subject != "" {
		form.Set("subject", subject)
	}
	form.Set("body", req.Text)

	// Gofer only keeps these when the subject is a reply and matches the
	// parent's, which is what makes the message land in its thread.
	if reply != nil && reply.MessageID != "" {
		form.Set("in_reply_to", reply.MessageID)
		if reply.References != "" {
			form.Set("references", reply.References)
		}
	}

	for _, attachment := range uploads {
		form.Add("attachment_id", attachment.ID)
		form.Add("attachment_filename", attachment.Filename)
		form.Add("attachment_content_type", attachment.ContentType)
		form.Add("attachment_size", fmt.Sprintf("%d", attachment.Size))
	}
	return form
}

// replySubject marks the subject as a reply unless it already is one, the way
// gofer's composer does; gofer drops the threading headers of a message whose
// subject is not a reply to the parent's.
func replySubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" || hasReplyPrefix(subject) {
		return subject
	}
	return "Re: " + subject
}

func hasReplyPrefix(subject string) bool {
	lower := strings.ToLower(subject)
	for _, prefix := range []string{"re:", "fw:", "rv:", "fwd:", "re[", "fw[", "[fwd:"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, prefix := range []string{"回复:", "回复：", "答复:", "答复：", "转发:", "转发："} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.HasSuffix(lower, "(fwd)")
}

func joinAddresses(addresses []string) string {
	joined := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if address = strings.TrimSpace(address); address != "" {
			joined = append(joined, address)
		}
	}
	return strings.Join(joined, ", ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// uploadAttachments stages the files on the server and returns what the
// composer form must name them by.
func (c *Client) uploadAttachments(ctx context.Context, paths []string) ([]composeAttachment, error) {
	uploads := make([]composeAttachment, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			return nil, fmt.Errorf("attachment %s is a directory", path)
		}
		if info.Size() > maxAttachmentBytes {
			return nil, fmt.Errorf("attachment %s is %d bytes, more than gofer accepts (%d)",
				path, info.Size(), int64(maxAttachmentBytes))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		body, contentType, err := attachmentUploadBody(filepath.Base(path), data)
		if err != nil {
			return nil, err
		}
		resp, err := c.do(ctx, http.MethodPost, "/compose/attachments", nil, bytes.NewReader(body), contentType)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			err := actionStatusError("/compose/attachments", resp)
			closeActionBody(resp)
			return nil, err
		}
		var upload composeAttachment
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&upload)
		closeActionBody(resp)
		if decodeErr != nil {
			return nil, fmt.Errorf("attachment %s: %w", path, decodeErr)
		}
		if upload.ID == "" {
			return nil, fmt.Errorf("attachment %s: gofer returned no id", path)
		}
		uploads = append(uploads, upload)
	}
	return uploads, nil
}

// attachmentUploadBody is the multipart body POST /compose/attachments reads,
// and the content type that names its boundary. The type of the file is left
// out when the name does not say it, so gofer sniffs the bytes.
func attachmentUploadBody(filename string, data []byte) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="attachment"; filename=%q`, filename))
	if contentType := mime.TypeByExtension(filepath.Ext(filename)); contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}
