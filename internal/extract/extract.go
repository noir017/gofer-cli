// Package extract finds verification codes and links in a message.
package extract

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/noir017/gofer-cli/internal/model"
)

// OTP returns the verification code in m. A non-empty pattern is a regexp
// whose first group (or whole match) is the code.
func OTP(m *model.Message, pattern string) (string, bool) {
	if m == nil {
		return "", false
	}

	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return "", false
		}
		for _, text := range []string{m.Subject, m.Text, m.HTML} {
			if text == "" {
				continue
			}
			match := re.FindStringSubmatch(text)
			if match == nil {
				continue
			}
			if len(match) > 1 {
				if match[1] != "" {
					return match[1], true
				}
				continue
			}
			if match[0] != "" {
				return match[0], true
			}
		}
		return "", false
	}

	var cands []otpCandidate
	scanOTP(m.Subject, 40, &cands)
	scanOTP(m.Text, 30, &cands)
	if m.Text == "" && m.HTML != "" {
		scanOTP(m.HTML, 20, &cands)
	}
	if len(cands) == 0 {
		return "", false
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].pos < cands[j].pos
	})
	return cands[0].code, true
}

var (
	reKeywordCN = regexp.MustCompile(`验证码|校验码|动态码|动态密码|确认码|激活码|安全码|登录码|一次性密码|验证代码|驗證碼|校驗碼|動態碼|動態密碼|確認碼|啟動碼|安全碼|登入碼|一次性密碼|驗證代碼|認證碼`)
	reKeywordEN = regexp.MustCompile(`(?i)\b(?:otp|pin|passcode|password|verification|verify|confirmation|security|authentication|auth|code)\b`)
	reOTPDigits = regexp.MustCompile(`\d{4,8}`)
	reOTPAlnum  = regexp.MustCompile(`[A-Z0-9]{4,8}`)
	reTextURL   = regexp.MustCompile("https?://[^\\s<>\"'`，。；：！？）】》」、]+")
	// Links that usually lead straight to the action come first; generic
	// sign-in links are weaker.
	strongLinkKeywords = []string{"verify", "confirm", "activate", "validate", "token", "magic", "reset"}
	weakLinkKeywords   = []string{"login", "signin", "sign-in", "auth"}
)

type span struct{ start, end int }

type otpCandidate struct {
	code  string
	score int
	pos   int
}

func scanOTP(text string, weight int, out *[]otpCandidate) {
	if text == "" {
		return
	}
	kws := keywordSpans(text)
	if len(kws) == 0 {
		return
	}

	// 4 to 8 digits close to a keyword.
	for _, loc := range reOTPDigits.FindAllStringIndex(text, -1) {
		if !asciiTokenBoundary(text, loc[0], loc[1]) {
			continue
		}
		if noiseNumber(text, loc[0], loc[1]) {
			continue
		}
		dist := keywordDistance(kws, loc[0], loc[1])
		if dist > 80 {
			continue
		}
		code := text[loc[0]:loc[1]]
		score := weight + 60 - dist
		if len(code) == 6 {
			score += 15
		}
		*out = append(*out, otpCandidate{code: code, score: score, pos: loc[0]})
	}

	// Upper-case letters and digits mixed, only when right next to a keyword.
	for _, loc := range reOTPAlnum.FindAllStringIndex(text, -1) {
		code := text[loc[0]:loc[1]]
		if !mixedUpperAlnum(code) {
			continue
		}
		if !asciiTokenBoundary(text, loc[0], loc[1]) {
			continue
		}
		dist := keywordDistance(kws, loc[0], loc[1])
		if dist > 16 {
			continue
		}
		*out = append(*out, otpCandidate{code: code, score: weight + 60 - dist + 5, pos: loc[0]})
	}
}

func keywordSpans(text string) []span {
	var out []span
	for _, loc := range reKeywordCN.FindAllStringIndex(text, -1) {
		out = append(out, span{loc[0], loc[1]})
	}
	for _, loc := range reKeywordEN.FindAllStringIndex(text, -1) {
		out = append(out, span{loc[0], loc[1]})
	}
	return out
}

func keywordDistance(kws []span, start, end int) int {
	best := -1
	for _, k := range kws {
		var d int
		switch {
		case start >= k.end:
			d = start - k.end
		case end <= k.start:
			d = k.start - end
		}
		if best < 0 || d < best {
			best = d
		}
	}
	return best
}

// asciiTokenBoundary reports whether the match is not glued to other ASCII
// letters or digits, so "123456" inside "AB123456" or "20261111" is ignored.
func asciiTokenBoundary(text string, start, end int) bool {
	if start > 0 && isASCIIAlnum(text[start-1]) {
		return false
	}
	if end < len(text) && isASCIIAlnum(text[end]) {
		return false
	}
	return true
}

func mixedUpperAlnum(s string) bool {
	var letter, digit bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			letter = true
		case c >= '0' && c <= '9':
			digit = true
		default:
			return false
		}
	}
	return letter && digit
}

// noiseNumber drops numbers that are dates, times, prices, order numbers or
// phone numbers rather than verification codes.
func noiseNumber(text string, start, end int) bool {
	code := text[start:end]

	// Years.
	if len(code) == 4 {
		if n, err := strconv.Atoi(code); err == nil && n >= 1900 && n <= 2099 {
			return true
		}
	}

	// A phone number split by spaces, dashes or parentheses.
	if digitRun(text, start, end) > 8 {
		return true
	}

	// Times such as 10:30 or 9:05.
	if end+2 < len(text) && text[end] == ':' && isASCIIDigit(text[end+1]) && isASCIIDigit(text[end+2]) {
		return true
	}
	if start >= 3 && text[start-1] == ':' && isASCIIDigit(text[start-2]) && isASCIIDigit(text[start-3]) {
		return true
	}

	// Prices.
	if start > 0 && strings.ContainsRune("¥$€£", rune(text[start-1])) {
		return true
	}
	rest := text[end:]
	for _, suffix := range []string{"元", "美元", "USD", "CNY", "RMB"} {
		if strings.HasPrefix(rest, suffix) {
			return true
		}
	}

	// Order and invoice numbers.
	left := start - 16
	if left < 0 {
		left = 0
	}
	right := end + 8
	if right > len(text) {
		right = len(text)
	}
	ctx := strings.ToLower(text[left:start] + " " + text[end:right])
	for _, word := range []string{"订单", "单号", "编号", "order number", "order no", "order id", "invoice"} {
		if strings.Contains(ctx, word) {
			return true
		}
	}
	return false
}

// digitRun counts the digits around a match when they are joined only by
// phone-number separators.
func digitRun(text string, start, end int) int {
	count := end - start
	for i := start - 1; i >= 0; i-- {
		c := text[i]
		if isASCIIDigit(c) {
			count++
			continue
		}
		if isPhoneSeparator(c) {
			continue
		}
		break
	}
	for i := end; i < len(text); i++ {
		c := text[i]
		if isASCIIDigit(c) {
			count++
			continue
		}
		if isPhoneSeparator(c) {
			continue
		}
		break
	}
	return count
}

func isPhoneSeparator(c byte) bool {
	switch c {
	case ' ', '\t', '-', '(', ')', '+':
		return true
	}
	return false
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }

func isASCIIAlnum(c byte) bool {
	return isASCIIDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// Link returns the verification or sign-in link in m. A non-empty match is a
// regexp the link must match.
func Link(m *model.Message, match string) (string, bool) {
	if m == nil {
		return "", false
	}
	var filter *regexp.Regexp
	if match != "" {
		re, err := regexp.Compile(match)
		if err != nil {
			return "", false
		}
		filter = re
	}

	var cands []linkCandidate
	cands = append(cands, htmlAnchors(m.HTML)...)
	for _, loc := range reTextURL.FindAllStringIndex(m.Text, -1) {
		if u := cleanTextURL(m.Text[loc[0]:loc[1]]); u != "" {
			cands = append(cands, linkCandidate{url: u, order: len(cands)})
		}
	}

	seen := make(map[string]bool)
	kept := make([]linkCandidate, 0, len(cands))
	for _, c := range cands {
		if seen[c.url] || junkLink(c) {
			continue
		}
		if filter != nil && !filter.MatchString(c.url) {
			continue
		}
		seen[c.url] = true
		c.score = linkScore(c)
		kept = append(kept, c)
	}
	if len(kept) == 0 {
		return "", false
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].score != kept[j].score {
			return kept[i].score > kept[j].score
		}
		return kept[i].order < kept[j].order
	})
	return kept[0].url, true
}

type linkCandidate struct {
	url   string
	text  string
	score int
	order int
}

func linkScore(c linkCandidate) int {
	u := strings.ToLower(c.url)
	t := strings.ToLower(c.text)
	score := 0
	switch {
	case containsAny(u, strongLinkKeywords):
		score += 100
	case containsAny(u, weakLinkKeywords):
		score += 60
	}
	switch {
	case containsAny(t, strongLinkKeywords):
		score += 80
	case containsAny(t, weakLinkKeywords):
		score += 40
	}
	if c.text != "" {
		score += 5
	}
	return score
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func junkLink(c linkCandidate) bool {
	s := strings.ToLower(c.url + " " + c.text)
	for _, word := range []string{
		"unsubscribe", "opt out", "opt-out", "退订", "取消订阅",
		"privacy", "隐私", "隱私",
		"help", "帮助", "幫助",
		"/track", "pixel", "beacon", "open.gif",
	} {
		if strings.Contains(s, word) {
			return true
		}
	}
	return false
}

func htmlAnchors(raw string) []linkCandidate {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil
	}
	base := ""
	var out []linkCandidate
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "base":
				if base == "" {
					base = htmlAttr(n, "href")
				}
			case "a":
				href := htmlAttr(n, "href")
				if u := resolveURL(base, href); u != "" {
					out = append(out, linkCandidate{
						url:   u,
						text:  normalizeText(nodeText(n)),
						order: len(out),
					})
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

func resolveURL(base, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	lower := strings.ToLower(href)
	for _, prefix := range []string{"mailto:", "tel:", "javascript:", "data:", "cid:", "#"} {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	if strings.HasPrefix(href, "//") {
		href = "https:" + href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if !u.IsAbs() {
		if base == "" {
			return ""
		}
		b, err := url.Parse(base)
		if err != nil {
			return ""
		}
		u = b.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	return u.String()
}

func cleanTextURL(s string) string {
	s = html.UnescapeString(strings.TrimSpace(s))
	s = strings.TrimRight(s, ".,;:!?'\"]}，。；：！？、）】」》")
	// A closing paren is punctuation unless it balances one inside the URL.
	for strings.HasSuffix(s, ")") && strings.Count(s, ")") > strings.Count(s, "(") {
		s = strings.TrimSuffix(s, ")")
		s = strings.TrimRight(s, ".,;:!?'\"]}，。；：！？、）】」》")
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.String()
}

func nodeText(n *html.Node) string {
	var parts []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			parts = append(parts, n.Data)
			return
		}
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "head", "title":
				return
			case "br":
				parts = append(parts, " ")
				return
			case "img":
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

func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.Join(strings.Fields(s), " ")
}
