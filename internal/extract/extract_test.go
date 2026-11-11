package extract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestOTPFullWidth(t *testing.T) {
	m := &model.Message{Subject: "验证码", Text: "您的验证码是１２３４５６，请勿泄露。"}
	got, ok := OTP(m, "")
	if !ok || got != "123456" {
		t.Errorf("OTP = %q, %v; want 123456", got, ok)
	}
}

func TestOTPSplitDigits(t *testing.T) {
	cases := []struct {
		name string
		msg  *model.Message
	}{
		{"spaces", &model.Message{Subject: "验证码", Text: "您的验证码是 1 2 3 4 5 6，请勿泄露。"}},
		{"line breaks", &model.Message{Subject: "验证码", Text: "您的验证码是\n1\n2\n3\n4\n5\n6\n请勿泄露。"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := OTP(tc.msg, "")
			if !ok || got != "123456" {
				t.Errorf("OTP = %q, %v; want 123456", got, ok)
			}
		})
	}
}

func TestOTPSplitDigitsFromTable(t *testing.T) {
	raw := []byte(`From: a@example.com
Subject: 验证码
MIME-Version: 1.0
Content-Type: text/html; charset=UTF-8

<p>您的验证码是</p><table><tr><td>1</td><td>2</td><td>3</td><td>4</td><td>5</td><td>6</td></tr></table>
`)
	m, err := message.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got, ok := OTP(m, "")
	if !ok || got != "123456" {
		t.Errorf("OTP = %q, %v; want 123456 (text %q)", got, ok, m.Text)
	}
}

func TestOTPLowercaseAlnum(t *testing.T) {
	m := &model.Message{Subject: "Your code", Text: "Your verification code is ab12cd."}
	got, ok := OTP(m, "")
	if !ok || got != "ab12cd" {
		t.Errorf("OTP = %q, %v; want ab12cd", got, ok)
	}
}

func TestOTPYearNeedsDateContext(t *testing.T) {
	// A four digit number near a keyword is a code unless the text around it
	// reads like a date.
	m := &model.Message{Subject: "Your code", Text: "Your verification code is 2026 for the beta."}
	if got, ok := OTP(m, ""); !ok || got != "2026" {
		t.Errorf("OTP = %q, %v; want 2026", got, ok)
	}

	m = &model.Message{Subject: "验证码", Text: "验证码 2026年有效。"}
	if got, ok := OTP(m, ""); ok {
		t.Errorf("OTP = %q, want the year to be ignored", got)
	}

	m = &model.Message{Subject: "Your code", Text: "Your code 2026. Thanks."}
	if got, ok := OTP(m, ""); ok {
		t.Errorf("OTP = %q, want the year before a period to be ignored", got)
	}

	m = &model.Message{Subject: "Your code", Text: "Your code 2026-11-11, use 445566."}
	if got, ok := OTP(m, ""); !ok || got != "445566" {
		t.Errorf("OTP = %q, %v; want 445566", got, ok)
	}
}

func TestLinkUnsubscribeContext(t *testing.T) {
	m := &model.Message{Text: "unsubscribe here: https://example.com/list?u=1"}
	if got, ok := Link(m, ""); ok {
		t.Errorf("Link = %q, want the unsubscribe link to be skipped", got)
	}

	// A sentence break keeps an earlier unsubscribe word from tainting a
	// later verification link.
	m = &model.Message{Text: "To unsubscribe, click here. Verify your email: https://example.com/verify?t=1"}
	got, ok := Link(m, "")
	if !ok || got != "https://example.com/verify?t=1" {
		t.Errorf("Link = %q, %v", got, ok)
	}
}

func TestLinkJunkByPathSegment(t *testing.T) {
	if got, ok := Link(&model.Message{Text: "See https://example.com/help for more."}, ""); ok {
		t.Errorf("Link = %q, want the help link to be skipped", got)
	}
	got, ok := Link(&model.Message{Text: "See https://example.com/help-center/verify?token=1 for more."}, "")
	if !ok || got != "https://example.com/help-center/verify?token=1" {
		t.Errorf("Link = %q, %v; help-center is not a help path segment", got, ok)
	}
}

func TestLinkWordBoundary(t *testing.T) {
	// "auth" must not match "authors", so the verify link wins.
	m := &model.Message{HTML: `<html><body>
		<a href="https://example.com/authors">Authors</a>
		<a href="https://example.com/verify?t=1">Go</a>
	</body></html>`}
	got, ok := Link(m, "")
	if !ok || got != "https://example.com/verify?t=1" {
		t.Errorf("Link = %q, %v", got, ok)
	}
}

func TestLinkWrappedURL(t *testing.T) {
	prefix := "https://example.com/account/verify?token="
	line := prefix + strings.Repeat("a", 78-len(prefix))
	m := &model.Message{Text: "Click below:\n" + line + "\nb1234567890 to finish."}
	want := prefix + strings.Repeat("a", 78-len(prefix)) + "b1234567890"
	got, ok := Link(m, "")
	if !ok || got != want {
		t.Errorf("Link = %q, %v; want %q", got, ok, want)
	}
}

func TestLinkImageOnlyAnchor(t *testing.T) {
	m := &model.Message{HTML: `<a href="https://example.com/verify?t=1"><img src="x.png"></a>`}
	if got, ok := Link(m, ""); ok {
		t.Errorf("Link = %q, want an anchor with no text or title to be skipped", got)
	}

	m = &model.Message{HTML: `<a href="https://example.com/verify?t=1"><img src="x.png" alt="Verify"></a>`}
	got, ok := Link(m, "")
	if !ok || got != "https://example.com/verify?t=1" {
		t.Errorf("Link = %q, %v", got, ok)
	}
}

func TestOTPLargeBodyIsFast(t *testing.T) {
	text := strings.Repeat("lorem ipsum dolor sit amet ", 80000) + " your verification code is 998877"
	start := time.Now()
	got, ok := OTP(&model.Message{Subject: "x", Text: text}, "")
	if !ok || got != "998877" {
		t.Errorf("OTP = %q, %v; want 998877", got, ok)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("OTP took %v on a %d byte body", elapsed, len(text))
	}
}
