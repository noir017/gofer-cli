package message

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noir017/gofer-cli/internal/model"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	return raw
}

func TestParse(t *testing.T) {
	cases := []struct {
		file      string
		subject   string
		fromName  string
		fromEmail string
		text      []string
		notText   []string
		html      []string
		att       []model.Attachment
	}{
		{
			file:      "gbk_base64.eml",
			subject:   "示例服务验证码",
			fromName:  "示例服务",
			fromEmail: "no-reply@example.com",
			text:      []string{"您正在登录示例服务", "验证码为 739261", "请勿将验证码告诉他人"},
		},
		{
			file:      "big5_base64.eml",
			subject:   "驗證碼",
			fromName:  "範例服務",
			fromEmail: "no-reply@example.com",
			text:      []string{"您的驗證碼為 573914"},
		},
		{
			file:      "gb18030_base64.eml",
			subject:   "验证码",
			fromName:  "示例服务",
			fromEmail: "no-reply@example.com",
			text:      []string{"您的校验码是 860417"},
		},
		{
			file:      "qp_english.eml",
			subject:   "Your verification code",
			fromName:  "Example Security",
			fromEmail: "security@example.com",
			text:      []string{"Your verification code is 482913", "you can safely ignore this message"},
			notText:   []string{"safely ="},
		},
		{
			file:      "html_only.eml",
			subject:   "Confirm your email address",
			fromName:  "Example Accounts",
			fromEmail: "accounts@example.com",
			text: []string{
				"Please confirm your email address",
				"Confirm email (https://example.com/account/verify?token=AbC123xyz&uid=42)",
			},
			notText: []string{"<p>", "<html>"},
			html: []string{
				`<a href="https://example.com/account/verify?token=AbC123xyz&amp;uid=42"`,
				"Confirm email</a>",
			},
		},
		{
			file:      "multipart_attachment.eml",
			subject:   "Your invoice and account confirmation",
			fromName:  "Example Billing",
			fromEmail: "billing@example.com",
			text:      []string{"Your invoice is attached", "https://example.com/login/confirm?t=7f3a9c", "Thank you,"},
			notText:   []string{"<p>"},
			html:      []string{`<a href="https://example.com/login/confirm?t=7f3a9c">this link</a>`},
			att: []model.Attachment{
				{Index: 1, Filename: "logo.png", ContentType: "image/png", Size: 75, Inline: true},
				{Index: 2, Filename: "invoice.pdf", ContentType: "application/pdf", Size: 142},
			},
		},
		{
			file:      "distractors.eml",
			subject:   "登录提醒",
			fromName:  "Example Alerts",
			fromEmail: "alert@example.com",
			text:      []string{"2026-11-11 10:30", "2026111100123", "138 0013 8000", "验证码：614209"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			m, err := Parse(readTestdata(t, tc.file))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if m.Subject != tc.subject {
				t.Errorf("Subject = %q, want %q", m.Subject, tc.subject)
			}
			if len(m.From) != 1 || m.From[0].Name != tc.fromName || m.From[0].Email != tc.fromEmail {
				t.Errorf("From = %+v, want %s <%s>", m.From, tc.fromName, tc.fromEmail)
			}
			if len(m.To) != 1 || m.To[0].Email != "user@example.com" {
				t.Errorf("To = %+v", m.To)
			}
			if m.Date.IsZero() || m.Date.Year() != 2026 {
				t.Errorf("Date = %v", m.Date)
			}
			if m.MessageID == "" {
				t.Error("MessageID is empty")
			}
			for _, want := range tc.text {
				if !strings.Contains(m.Text, want) {
					t.Errorf("Text does not contain %q:\n%s", want, m.Text)
				}
			}
			for _, bad := range tc.notText {
				if strings.Contains(m.Text, bad) {
					t.Errorf("Text contains %q:\n%s", bad, m.Text)
				}
			}
			for _, want := range tc.html {
				if !strings.Contains(m.HTML, want) {
					t.Errorf("HTML does not contain %q", want)
				}
			}
			if len(m.Attachments) != len(tc.att) {
				t.Fatalf("Attachments = %+v, want %d", m.Attachments, len(tc.att))
			}
			for i, want := range tc.att {
				if m.Attachments[i] != want {
					t.Errorf("Attachments[%d] = %+v, want %+v", i, m.Attachments[i], want)
				}
			}
		})
	}
}

func TestParseAttachmentData(t *testing.T) {
	raw := readTestdata(t, "multipart_attachment.eml")

	meta, data, err := AttachmentData(raw, 1)
	if err != nil {
		t.Fatalf("AttachmentData(1): %v", err)
	}
	if meta.Filename != "logo.png" || !meta.Inline || meta.Size != len(data) {
		t.Errorf("meta = %+v, len(data) = %d", meta, len(data))
	}
	if len(data) < 8 || string(data[1:4]) != "PNG" {
		t.Errorf("attachment 1 is not a PNG: %q", data[:min(8, len(data))])
	}

	meta, data, err = AttachmentData(raw, 2)
	if err != nil {
		t.Fatalf("AttachmentData(2): %v", err)
	}
	if meta.Filename != "invoice.pdf" || meta.Inline || meta.Size != len(data) {
		t.Errorf("meta = %+v, len(data) = %d", meta, len(data))
	}
	if !strings.HasPrefix(string(data), "%PDF-1.4") {
		t.Errorf("attachment 2 is not a PDF: %q", data[:min(8, len(data))])
	}

	for _, index := range []int{0, 3, -1} {
		_, _, err := AttachmentData(raw, index)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("AttachmentData(%d) error = %v, want ErrNotFound", index, err)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, raw := range []string{"", "   ", "not a message"} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("Parse(%q) returned no error", raw)
		}
	}
}

func TestParseHTMLKeepsLinkText(t *testing.T) {
	raw := []byte(`From: a@example.com
To: b@example.com
Subject: test
MIME-Version: 1.0
Content-Type: text/html; charset=UTF-8

<html><body><p>Open <a href="https://example.com/verify?token=1">the link</a> now.</p></body></html>
`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := "Open the link (https://example.com/verify?token=1) now."
	if !strings.Contains(m.Text, want) {
		t.Errorf("Text = %q, want it to contain %q", m.Text, want)
	}
}

func TestParsePrefersPlainOverHTML(t *testing.T) {
	// The HTML part comes first, but text/plain still wins.
	raw := []byte(`From: a@example.com
To: b@example.com
Subject: test
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="b"

--b
Content-Type: text/html; charset=UTF-8

<p>HTML body</p>

--b
Content-Type: text/plain; charset=UTF-8

plain body

--b--
`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Text != "plain body" {
		t.Errorf("Text = %q, want %q", m.Text, "plain body")
	}
	if !strings.Contains(m.HTML, "<p>HTML body</p>") {
		t.Errorf("HTML = %q", m.HTML)
	}
}

func TestParseInlineCID(t *testing.T) {
	raw := []byte(`From: a@example.com
To: b@example.com
Subject: test
MIME-Version: 1.0
Content-Type: multipart/related; boundary="r"

--r
Content-Type: text/html; charset=UTF-8

<p><img src="cid:pic@example.com"></p>

--r
Content-Type: image/gif
Content-ID: <pic@example.com>
Content-Transfer-Encoding: base64

R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7

--r--
`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Attachments) != 1 {
		t.Fatalf("Attachments = %+v", m.Attachments)
	}
	a := m.Attachments[0]
	if !a.Inline || a.ContentType != "image/gif" || a.Size != 42 {
		t.Errorf("attachment = %+v", a)
	}
}
