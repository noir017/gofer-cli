// Package message parses raw mail into model.Message.
package message

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"

	"github.com/noir017/gofer-cli/internal/model"
)

// maxHTMLBytes bounds the HTML body kept and parsed. Larger bodies are cut so
// a hostile message cannot make the HTML parser work on unbounded input.
const maxHTMLBytes = 2 << 20

// Parse reads an RFC 822 message: headers decoded (RFC 2047), any charset to
// UTF-8, Text from text/plain or else from text/html, attachments listed.
func Parse(raw []byte) (*model.Message, error) {
	m, _, err := parse(raw)
	return m, err
}

// AttachmentData returns the decoded bytes of attachment index (1-based, as
// Parse numbers them).
func AttachmentData(raw []byte, index int) (model.Attachment, []byte, error) {
	_, atts, err := parse(raw)
	if err != nil {
		return model.Attachment{}, nil, err
	}
	if index < 1 || index > len(atts) {
		return model.Attachment{}, nil, fmt.Errorf("message: attachment %d of %d: %w", index, len(atts), model.ErrNotFound)
	}
	return atts[index-1].meta, atts[index-1].data, nil
}

type attachment struct {
	meta model.Attachment
	data []byte
}

func parse(raw []byte) (*model.Message, []attachment, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, fmt.Errorf("message: parse: empty message")
	}
	e, err := message.Read(bytes.NewReader(raw))
	if e == nil {
		if err == nil {
			err = fmt.Errorf("empty message")
		}
		return nil, nil, fmt.Errorf("message: parse: %w", err)
	}

	h := &mail.Header{Header: e.Header}
	m := &model.Message{}
	if s, err := h.Subject(); err == nil {
		m.Subject = s
	}
	if id, err := h.MessageID(); err == nil {
		m.MessageID = id
	}
	if ids, err := h.MsgIDList("In-Reply-To"); err == nil && len(ids) > 0 {
		m.InReplyTo = ids[0]
	}
	if d, err := h.Date(); err == nil {
		m.Date = d
	}
	m.From = addressList(h, "From")
	m.To = addressList(h, "To")
	m.Cc = addressList(h, "Cc")
	m.ReplyTo = addressList(h, "Reply-To")

	var plain, htmlBody string
	var plainHasName, htmlHasName bool
	var atts []attachment

	// Walk is best effort: a broken part is skipped rather than failing the
	// whole message.
	walkErr := e.Walk(func(_ []int, ent *message.Entity, _ error) error {
		if ent == nil || ent.MultipartReader() != nil {
			return nil
		}

		ct, params, _ := ent.Header.ContentType()
		ct = strings.ToLower(strings.TrimSpace(ct))
		if ct == "" {
			ct = "application/octet-stream"
		}
		disp, dispParams, _ := ent.Header.ContentDisposition()
		disp = strings.ToLower(strings.TrimSpace(disp))
		filename := dispParams["filename"]
		if filename == "" {
			filename = params["name"]
		}
		cid := strings.Trim(strings.TrimSpace(ent.Header.Get("Content-Id")), "<>")

		data, _ := io.ReadAll(ent.Body)

		// text/plain and text/html are the body unless the sender marked them
		// as an attachment. An inline HTML part keeps a filename in some
		// clients and is still the message body.
		if (ct == "text/plain" || ct == "text/html") && disp != "attachment" {
			if ct == "text/html" {
				data = truncateHTML(data, maxHTMLBytes)
			}
			body := string(data)
			if isPlaceholderBody(body) {
				return nil
			}
			if ct == "text/plain" {
				if plain == "" || (plainHasName && filename == "") {
					plain = body
					plainHasName = filename != ""
				}
			} else if htmlBody == "" || (htmlHasName && filename == "") {
				htmlBody = body
				htmlHasName = filename != ""
			}
			return nil
		}

		index := len(atts) + 1
		if filename == "" {
			filename = fmt.Sprintf("attachment-%d", index)
		}
		atts = append(atts, attachment{
			meta: model.Attachment{
				Index:       index,
				Filename:    filename,
				ContentType: ct,
				Size:        len(data),
				Inline:      disp == "inline" || (disp == "" && cid != ""),
			},
			data: data,
		})
		return nil
	})

	m.Text = strings.TrimSpace(strings.ReplaceAll(plain, "\r\n", "\n"))
	m.HTML = htmlBody
	if m.Text == "" && m.HTML != "" {
		m.Text = htmlToText(m.HTML)
	}
	// A multipart header without a boundary (or another broken multipart
	// structure) leaves Walk with nothing. Keep the raw body as text instead
	// of dropping the message content.
	if walkErr != nil && m.Text == "" && m.HTML == "" && len(atts) == 0 {
		if broken, _ := message.Read(bytes.NewReader(raw)); broken != nil {
			if data, err := io.ReadAll(broken.Body); err == nil {
				text := strings.ReplaceAll(string(data), "\r\n", "\n")
				// The body often starts with the part headers; keep what
				// follows the last blank line.
				if i := strings.LastIndex(text, "\n\n"); i >= 0 {
					text = text[i+2:]
				}
				m.Text = strings.TrimSpace(text)
			}
		}
	}
	if len(atts) > 0 {
		m.Attachments = make([]model.Attachment, len(atts))
		for i, a := range atts {
			m.Attachments[i] = a.meta
		}
	}
	return m, atts, nil
}

func addressList(h *mail.Header, key string) []model.Address {
	list, err := h.AddressList(key)
	if err != nil || len(list) == 0 {
		return nil
	}
	out := make([]model.Address, 0, len(list))
	for _, a := range list {
		out = append(out, model.Address{Name: a.Name, Email: a.Address})
	}
	return out
}

// isPlaceholderBody reports bodies that carry no message: whitespace or the
// stock line multipart generators leave in a text/plain part.
func isPlaceholderBody(s string) bool {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return true
	}
	return strings.EqualFold(s, "This is a multi-part message in MIME format.") ||
		strings.EqualFold(s, "This is a multi-part message in MIME format")
}

// truncateHTML cuts an oversized HTML body, keeping valid UTF-8 at the end.
func truncateHTML(data []byte, max int) []byte {
	if len(data) <= max {
		return data
	}
	data = data[:max]
	for len(data) > 0 {
		r, size := utf8.DecodeLastRune(data)
		if r != utf8.RuneError || size > 1 {
			break
		}
		data = data[:len(data)-1]
	}
	return data
}

var (
	htmlSkip = map[string]bool{
		"head": true, "title": true, "script": true, "style": true,
		"noscript": true, "template": true, "svg": true, "meta": true, "link": true,
	}
	htmlBlock = map[string]bool{
		"address": true, "article": true, "aside": true, "blockquote": true,
		"dd": true, "div": true, "dl": true, "dt": true, "fieldset": true,
		"figcaption": true, "figure": true, "footer": true, "form": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"header": true, "hr": true, "li": true, "main": true, "nav": true,
		"ol": true, "p": true, "pre": true, "section": true, "table": true,
		"tbody": true, "tfoot": true, "thead": true, "tr": true, "ul": true,
	}
)

// htmlToText renders the visible text of an HTML body. Links keep their
// target as "text (url)" so a verification URL is never lost.
func htmlToText(raw string) string {
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return ""
	}
	var b textBuf
	writeHTML(&b, doc)
	return b.String()
}

func writeHTML(b *textBuf, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		b.write(normalizeInline(n.Data))
		return
	case html.ElementNode:
		name := strings.ToLower(n.Data)
		if htmlSkip[name] {
			return
		}
		switch name {
		case "br":
			b.newline()
			return
		case "img":
			if alt := htmlAttr(n, "alt"); alt != "" {
				b.write(normalizeInline(alt))
			}
			return
		case "a":
			href := strings.TrimSpace(htmlAttr(n, "href"))
			text := normalizeInline(htmlTextOf(n))
			switch {
			case text == "" && isHTTPURL(href):
				b.write(href)
			case text != "" && isHTTPURL(href) && text != href:
				b.write(text)
				b.b = append(b.b, " ("...)
				b.b = append(b.b, href...)
				b.b = append(b.b, ')')
			case text != "":
				b.write(text)
			}
			return
		}
		if htmlBlock[name] {
			b.newline()
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeHTML(b, c)
		}
		if htmlBlock[name] {
			b.newline()
		}
		return
	default:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeHTML(b, c)
		}
	}
}

// htmlTextOf collects the inline text of a node without formatting.
func htmlTextOf(n *html.Node) string {
	var parts []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			parts = append(parts, n.Data)
			return
		}
		if n.Type == html.ElementNode {
			name := strings.ToLower(n.Data)
			if htmlSkip[name] {
				return
			}
			if name == "br" {
				parts = append(parts, " ")
				return
			}
			if name == "img" {
				if alt := htmlAttr(n, "alt"); alt != "" {
					parts = append(parts, alt)
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(parts, " ")
}

func htmlAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func normalizeInline(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = strings.ReplaceAll(s, "\u200b", "")
	return strings.Join(strings.Fields(s), " ")
}

// textBuf accumulates readable text, collapsing runs of spaces and blank
// lines as it goes.
type textBuf struct {
	b []byte
}

func (t *textBuf) write(s string) {
	if s == "" {
		return
	}
	if n := len(t.b); n > 0 {
		last := t.b[n-1]
		if last != '\n' && last != ' ' && !startsWithPunct(s) {
			t.b = append(t.b, ' ')
		}
	}
	t.b = append(t.b, s...)
}

func (t *textBuf) newline() {
	for len(t.b) > 0 && t.b[len(t.b)-1] == ' ' {
		t.b = t.b[:len(t.b)-1]
	}
	if len(t.b) == 0 || t.b[len(t.b)-1] == '\n' {
		return
	}
	t.b = append(t.b, '\n')
}

func (t *textBuf) String() string {
	lines := strings.Split(string(t.b), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if line == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

func startsWithPunct(s string) bool {
	for _, r := range s {
		switch r {
		case '.', ',', ':', ';', '!', '?', ')', ']', '}', '，', '。', '；', '：', '！', '？', '）', '】':
			return true
		default:
			return false
		}
	}
	return false
}
