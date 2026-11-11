package client

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"testing"
)

func TestComposeFormNew(t *testing.T) {
	form := composeForm(SendRequest{
		To:      []string{"alice@example.com", " Bob <bob@example.com> "},
		Cc:      []string{"carol@example.com"},
		Subject: "Lunch",
		Text:    "at noon",
	}, nil, "", "", nil)

	want := url.Values{
		"to":      {"alice@example.com, Bob <bob@example.com>"},
		"cc":      {"carol@example.com"},
		"subject": {"Lunch"},
		"body":    {"at noon"},
	}
	compareForm(t, form, want)
}

func TestComposeFormReply(t *testing.T) {
	reply := &replySource{
		AccountID:  "acc_one",
		IdentityID: "idn_1",
		MessageID:  "<abc@example.com>",
		References: "<root@example.com>",
		Subject:    "Confirm your email",
		FromEmail:  "support@example.com",
	}
	form := composeForm(SendRequest{ReplyTo: "42", Text: "done"}, reply, reply.AccountID, reply.IdentityID, nil)

	want := url.Values{
		"account_id":  {"acc_one"},
		"identity_id": {"idn_1"},
		"to":          {"support@example.com"}, // the sender of the answered message
		"subject":     {"Re: Confirm your email"},
		"body":        {"done"},
		"in_reply_to": {"<abc@example.com>"},
		"references":  {"<root@example.com>"},
	}
	compareForm(t, form, want)
}

func TestComposeFormReplyUsesTheOriginalSubject(t *testing.T) {
	reply := &replySource{
		AccountID: "acc_one",
		MessageID: "<abc@example.com>",
		Subject:   "Report",
		FromEmail: "support@example.com",
	}
	form := composeForm(SendRequest{
		To:      []string{"someone@example.com"},
		Subject: "Something else",
		Text:    "see below",
	}, reply, "acc_two", "", nil)

	if got := form.Get("to"); got != "someone@example.com" {
		t.Errorf("to = %q, want the recipient the caller gave", got)
	}
	// Gofer compares the base subject with the answered message's and drops
	// the threading headers when they differ, so the original wins.
	if got := form.Get("subject"); got != "Re: Report" {
		t.Errorf("subject = %q, want Re: Report", got)
	}
	if got := form.Get("account_id"); got != "acc_two" {
		t.Errorf("account_id = %q, want the account of the From address", got)
	}
}

func TestComposeFormAttachments(t *testing.T) {
	form := composeForm(SendRequest{To: []string{"a@example.com"}, Text: "x"}, nil, "", "", []composeAttachment{
		{ID: "att_1", Filename: "a.pdf", ContentType: "application/pdf", Size: 12},
		{ID: "att_2", Filename: "b.txt", ContentType: "text/plain", Size: 34},
	})
	want := url.Values{
		"to":                      {"a@example.com"},
		"body":                    {"x"},
		"attachment_id":           {"att_1", "att_2"},
		"attachment_filename":     {"a.pdf", "b.txt"},
		"attachment_content_type": {"application/pdf", "text/plain"},
		"attachment_size":         {"12", "34"},
	}
	compareForm(t, form, want)
}

func TestComposeFormNoSubject(t *testing.T) {
	form := composeForm(SendRequest{To: []string{"a@example.com"}, Text: "x"}, nil, "", "", nil)
	if _, ok := form["subject"]; ok {
		t.Errorf("subject sent as %q, want it left out", form.Get("subject"))
	}
}

func TestReplySubject(t *testing.T) {
	tests := []struct{ subject, want string }{
		{"Report", "Re: Report"},
		{"re: Report", "re: Report"},
		{"RE: Report", "RE: Report"},
		{"Fwd: Report", "Fwd: Report"},
		{"答复：报告", "答复：报告"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := replySubject(tt.subject); got != tt.want {
			t.Errorf("replySubject(%q) = %q, want %q", tt.subject, got, tt.want)
		}
	}
}

func TestAttachmentUploadBody(t *testing.T) {
	body, contentType, err := attachmentUploadBody("notes.txt", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q, want multipart/form-data", mediaType)
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	part, err := reader.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if part.FormName() != "attachment" || part.FileName() != "notes.txt" {
		t.Errorf("part = %q named %q, want attachment named notes.txt", part.FormName(), part.FileName())
	}
	if got := part.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("part content type = %q, want the type of a .txt file", got)
	}
	data, err := io.ReadAll(part)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("part body = %q, want hello", data)
	}
}

func TestAttachmentUploadBodyUnknownType(t *testing.T) {
	body, contentType, err := attachmentUploadBody("blob.zzz", []byte{0, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	part, err := multipart.NewReader(bytes.NewReader(body), params["boundary"]).NextPart()
	if err != nil {
		t.Fatal(err)
	}
	// An empty type lets gofer sniff the bytes instead of believing a wrong one.
	if got := part.Header.Get("Content-Type"); got != "" {
		t.Errorf("part content type = %q, want none", got)
	}
}

func compareForm(t *testing.T, got, want url.Values) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("form = %v, want %v", got, want)
	}
	for key, values := range want {
		actual, ok := got[key]
		if !ok {
			t.Errorf("form has no %q", key)
			continue
		}
		if len(actual) != len(values) {
			t.Errorf("%s = %v, want %v", key, actual, values)
			continue
		}
		for i := range values {
			if actual[i] != values[i] {
				t.Errorf("%s[%d] = %q, want %q", key, i, actual[i], values[i])
			}
		}
	}
}
