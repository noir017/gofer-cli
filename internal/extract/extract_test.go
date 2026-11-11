package extract

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noir017/gofer-cli/internal/message"
	"github.com/noir017/gofer-cli/internal/model"
)

func parseTestdata(t *testing.T, name string) *model.Message {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "message", "testdata", name))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	m, err := message.Parse(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return m
}

func TestOTP(t *testing.T) {
	cases := []struct {
		file string
		want string
		ok   bool
	}{
		{"gbk_base64.eml", "739261", true},
		{"big5_base64.eml", "573914", true},
		{"gb18030_base64.eml", "860417", true},
		{"qp_english.eml", "482913", true},
		{"distractors.eml", "614209", true},
		{"alnum_code.eml", "AB12CD", true},
		{"html_only.eml", "", false},
		{"multipart_attachment.eml", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got, ok := OTP(parseTestdata(t, tc.file), "")
			if ok != tc.ok || got != tc.want {
				t.Errorf("OTP = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestOTPPattern(t *testing.T) {
	m := parseTestdata(t, "qp_english.eml")
	cases := []struct {
		name    string
		pattern string
		want    string
		ok      bool
	}{
		{"group", `code is (\d{6})`, "482913", true},
		{"whole", `\d{6}`, "482913", true},
		{"case insensitive", `(?i)VERIFICATION CODE IS (\d+)`, "482913", true},
		{"invalid regexp", `(`, "", false},
		{"no match", `\d{8}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := OTP(m, tc.pattern)
			if ok != tc.ok || got != tc.want {
				t.Errorf("OTP(%q) = %q, %v; want %q, %v", tc.pattern, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestOTPIgnoresNoise(t *testing.T) {
	m := &model.Message{
		Subject: "Your order",
		Text: "Order number 2026111100123 was placed on 2026-11-11 at 10:30.\n" +
			"Total: $1,299.00. Phone: +86 138-0013-8000.\n" +
			"Your verification code is 918273.",
	}
	got, ok := OTP(m, "")
	if !ok || got != "918273" {
		t.Errorf("OTP = %q, %v; want 918273, true", got, ok)
	}
}

func TestOTPInSubject(t *testing.T) {
	m := &model.Message{Subject: "【Example】验证码 552211，10 分钟内有效"}
	got, ok := OTP(m, "")
	if !ok || got != "552211" {
		t.Errorf("OTP = %q, %v; want 552211, true", got, ok)
	}
}

func TestOTPNoKeyword(t *testing.T) {
	m := &model.Message{Subject: "Hello", Text: "The meeting is at 10:30 in room 4021."}
	if got, ok := OTP(m, ""); ok {
		t.Errorf("OTP = %q, want no match", got)
	}
}

func TestLink(t *testing.T) {
	cases := []struct {
		file string
		want string
		ok   bool
	}{
		{"html_only.eml", "https://example.com/account/verify?token=AbC123xyz&uid=42", true},
		{"multipart_attachment.eml", "https://example.com/login/confirm?t=7f3a9c", true},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			got, ok := Link(parseTestdata(t, tc.file), "")
			if ok != tc.ok || got != tc.want {
				t.Errorf("Link = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestLinkMatch(t *testing.T) {
	m := parseTestdata(t, "html_only.eml")
	if got, ok := Link(m, `example\.com/account`); !ok || got != "https://example.com/account/verify?token=AbC123xyz&uid=42" {
		t.Errorf("Link(match) = %q, %v", got, ok)
	}
	if got, ok := Link(m, `nomatch`); ok {
		t.Errorf("Link(nomatch) = %q, want no match", got)
	}
	if got, ok := Link(m, `(`); ok {
		t.Errorf("Link(invalid regexp) = %q, want no match", got)
	}
}

func TestLinkSkipsJunk(t *testing.T) {
	m := &model.Message{
		HTML: `<html><body>
			<a href="https://example.com/unsubscribe?u=1">Unsubscribe</a>
			<a href="https://example.com/privacy">Privacy Policy</a>
			<a href="https://example.com/help">Help</a>
			<a href="mailto:support@example.com">Email us</a>
			<img src="https://track.example.com/open.gif" width="1" height="1">
		</body></html>`,
	}
	if got, ok := Link(m, ""); ok {
		t.Errorf("Link = %q, want no match", got)
	}
}

func TestLinkPrefersVerification(t *testing.T) {
	m := &model.Message{
		HTML: `<html><body>
			<a href="https://example.com/home">Home</a>
			<a href="https://example.com/account/confirm?token=xyz">Confirm account</a>
		</body></html>`,
	}
	got, ok := Link(m, "")
	if !ok || got != "https://example.com/account/confirm?token=xyz" {
		t.Errorf("Link = %q, %v", got, ok)
	}
}

func TestLinkPrefersMagicOverLogin(t *testing.T) {
	m := &model.Message{
		HTML: `<html><body>
			<a href="https://example.com/login">Sign in</a>
			<a href="https://example.com/magic/abc123">Sign in</a>
		</body></html>`,
	}
	got, ok := Link(m, "")
	if !ok || got != "https://example.com/magic/abc123" {
		t.Errorf("Link = %q, %v", got, ok)
	}
}

func TestLinkKeepsBalancedParen(t *testing.T) {
	m := &model.Message{Text: "See https://example.com/wiki/Go_(language) for details."}
	got, ok := Link(m, "")
	if !ok || got != "https://example.com/wiki/Go_(language)" {
		t.Errorf("Link = %q, %v", got, ok)
	}
}

func TestLinkPlainText(t *testing.T) {
	m := &model.Message{
		Text: "Open https://example.com/reset/abc123. The link expires soon.",
	}
	got, ok := Link(m, "")
	if !ok || got != "https://example.com/reset/abc123" {
		t.Errorf("Link = %q, %v; want trailing punctuation removed", got, ok)
	}
}

func TestLinkRelativeWithBase(t *testing.T) {
	m := &model.Message{
		HTML: `<html><head><base href="https://example.com/app/"></head>
			<body><a href="confirm?token=1">Confirm</a></body></html>`,
	}
	got, ok := Link(m, "")
	if !ok || got != "https://example.com/app/confirm?token=1" {
		t.Errorf("Link = %q, %v", got, ok)
	}
}
