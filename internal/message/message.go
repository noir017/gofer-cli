// Package message parses raw mail into model.Message.
package message

import (
	"errors"

	"github.com/noir017/gofer-cli/internal/model"
)

// Parse reads an RFC 822 message: headers decoded (RFC 2047), any charset to
// UTF-8, Text from text/plain or else from text/html, attachments listed.
func Parse(raw []byte) (*model.Message, error) {
	return nil, errors.New("message.Parse: not implemented")
}

// AttachmentData returns the decoded bytes of attachment index (1-based, as
// Parse numbers them).
func AttachmentData(raw []byte, index int) (model.Attachment, []byte, error) {
	return model.Attachment{}, nil, errors.New("message.AttachmentData: not implemented")
}
