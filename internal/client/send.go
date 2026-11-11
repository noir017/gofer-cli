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
// threaded with it, and the reply leaves from the account and identity the
// answered message arrived at unless From says otherwise. The reply takes its
// subject from that message, with "Re: " added when it has none, because gofer
// matches the base subject to keep the thread; SendRequest.Subject is used for
// a new message only. The reply text is the caller's; nothing is quoted here.
func (c *Client) Send(ctx context.Context, req SendRequest) error {
	if len(req.To) == 0 && strings.TrimSpace(req.ReplyTo) == "" {
		return errors.New("send: no recipients")
	}
	reply, err := c.replySource(ctx, req.ReplyTo)
	if err != nil {
		return err
	}

	var accounts []composeAccount
	if strings.TrimSpace(req.From) != "" {
		accounts, err = c.loadComposeAccounts(ctx)
		if err != nil {
			return err
		}
	}
	accountID, identityID, err := senderFor(req.From, reply, accounts)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}

	uploads, err := c.uploadAttachments(ctx, req.Attachments)
	if err != nil {
		return err
	}

	form := composeForm(req, reply, accountID, identityID, uploads)
	resp, err := c.do(ctx, http.MethodPost, "/compose", nil,
		strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		c.discardAttachments(ctx, uploads)
		return err
	}
	defer closeActionBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		err := actionStatusError("/compose", resp)
		c.discardAttachments(ctx, uploads)
		return err
	}
	return nil
}

// discardAttachments deletes the attachments staged for a send that failed.
// Nothing depends on it: gofer drops unreferenced uploads when the next one
// arrives, so an attachment that stays behind is only wasted space for a
// while.
func (c *Client) discardAttachments(ctx context.Context, uploads []composeAttachment) {
	if len(uploads) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	for _, upload := range uploads {
		resp, err := c.do(ctx, http.MethodDelete, "/compose/attachments/"+url.PathEscape(upload.ID), nil, nil, "")
		if err != nil {
			continue
		}
		closeActionBody(resp)
	}
}

// senderFor picks who the message is sent as. From decides when it is set; a
// reply without one leaves from the account and identity the answered message
// arrived at, which is where its thread lives. With neither, gofer sends from
// its first account.
func senderFor(from string, reply *replySource, accounts []composeAccount) (accountID, identityID string, err error) {
	if from = strings.TrimSpace(from); from != "" {
		accountID, identityID, ok := resolveSender(accounts, from)
		if !ok {
			return "", "", fmt.Errorf("%q is not an address of any account", from)
		}
		return accountID, identityID, nil
	}
	if reply != nil {
		return reply.AccountID, reply.IdentityID, nil
	}
	return "", "", nil
}

// replySource reads the message a reply answers, or nil when replyTo is empty.
func (c *Client) replySource(ctx context.Context, replyTo string) (*replySource, error) {
	replyTo = strings.TrimSpace(replyTo)
	if replyTo == "" {
		return nil, nil
	}
	query := url.Values{"email_id": {replyTo}}
	body, err := c.getPageQuery(ctx, "/api/compose/source", query)
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
		// Gofer keeps the threading headers only when the base subject is the
		// answered message's, so a reply takes that subject and no other.
		subject = replySubject(reply.Subject)
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

// uploadAttachments stages the files on the server and returns what the
// composer form must name them by. A file that fails takes the ones already
// staged with it: the send will not happen, so nothing would reference them.
func (c *Client) uploadAttachments(ctx context.Context, paths []string) ([]composeAttachment, error) {
	return stageAttachments(paths,
		func(path string) (composeAttachment, error) { return c.uploadAttachment(ctx, path) },
		func(staged []composeAttachment) { c.discardAttachments(ctx, staged) })
}

// stageAttachments uploads the files one by one, and hands what the earlier
// ones staged to discard when one fails.
func stageAttachments(paths []string, upload func(string) (composeAttachment, error), discard func([]composeAttachment)) ([]composeAttachment, error) {
	staged := make([]composeAttachment, 0, len(paths))
	for _, path := range paths {
		attachment, err := upload(path)
		if err != nil {
			discard(staged)
			return nil, err
		}
		staged = append(staged, attachment)
	}
	return staged, nil
}

func (c *Client) uploadAttachment(ctx context.Context, path string) (composeAttachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		return composeAttachment{}, err
	}
	if info.IsDir() {
		return composeAttachment{}, fmt.Errorf("attachment %s is a directory", path)
	}
	if info.Size() > maxAttachmentBytes {
		return composeAttachment{}, fmt.Errorf("attachment %s is %d bytes, more than gofer accepts (%d)",
			path, info.Size(), int64(maxAttachmentBytes))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return composeAttachment{}, err
	}
	body, contentType, err := attachmentUploadBody(filepath.Base(path), data)
	if err != nil {
		return composeAttachment{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, "/compose/attachments", nil, bytes.NewReader(body), contentType)
	if err != nil {
		return composeAttachment{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		err := actionStatusError("/compose/attachments", resp)
		closeActionBody(resp)
		return composeAttachment{}, err
	}
	var upload composeAttachment
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&upload)
	closeActionBody(resp)
	if decodeErr != nil {
		return composeAttachment{}, fmt.Errorf("attachment %s: %w", path, decodeErr)
	}
	if upload.ID == "" {
		return composeAttachment{}, fmt.Errorf("attachment %s: gofer returned no id", path)
	}
	return upload, nil
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
