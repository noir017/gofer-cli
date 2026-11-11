// Package extract finds verification codes and links in a message.
package extract

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"

	"github.com/noir017/gofer-cli/internal/model"
)

const (
	// otpWindow is how far around a keyword a code may sit.
	otpWindow = 120
	// otpKeywordMax bounds the work on very large or repetitive bodies.
	otpKeywordMax = 500
	// otpLinkMax bounds the number of URLs considered per message.
	otpLinkMax = 500
	// otpURLContext is how much text before a plain URL is inspected for
	// words such as "unsubscribe".
	otpURLContext = 60
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
			match := re.FindStringSubmatch(normalizeWidth(text))
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

	seen := make(map[string]bool, len(cands))
	uniq := make([]otpCandidate, 0, len(cands))
	for _, c := range cands {
		key := c.code + "|" + strconv.Itoa(c.pos)
		if seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, c)
	}
	sort.SliceStable(uniq, func(i, j int) bool {
		if uniq[i].score != uniq[j].score {
			return uniq[i].score > uniq[j].score
		}
		return uniq[i].pos < uniq[j].pos
	})
	return uniq[0].code, true
}

var (
	cnKeywords = []string{
		"验证码", "校验码", "动态码", "动态密码", "确认码", "激活码", "安全码",
		"登录码", "一次性密码", "验证代码", "认证码",
		"驗證碼", "校驗碼", "動態碼", "動態密碼", "確認碼", "啟動碼", "安全碼",
		"登入碼", "一次性密碼", "驗證代碼", "認證碼",
	}
	enKeywords = []string{
		"otp", "pin", "passcode", "password", "verification", "verify",
		"confirmation", "security", "authentication", "auth", "code",
	}

	reOTPDigits   = regexp.MustCompile(`\d{4,8}`)
	reSplitDigits = regexp.MustCompile(`\d(?:\s+\d){3,7}`)
	reOTPAlnum    = regexp.MustCompile(`[A-Za-z0-9]{4,8}`)

	reTextURL       = regexp.MustCompile("https?://[^\\s<>\"'`，。；：！？）】》」、]+")
	reLineEndsURL   = regexp.MustCompile(`https?://\S+$`)
	strongLinkWords = []string{
		"verify", "verification", "confirm", "confirmation", "activate",
		"activation", "validate", "validation", "token", "magic", "reset",
	}
	weakLinkWords = []string{
		"login", "log in", "signin", "sign in", "sign-in", "auth", "authentication",
	}
	junkWords = []string{
		"unsubscribe", "opt-out", "optout", "privacy", "help", "track",
		"pixel", "beacon", "open.gif",
		"退订", "取消订阅", "隐私", "隱私", "帮助", "幫助",
	}
	// contextJunkWords are the only words that make a plain-text URL junk
	// based on the text before it; help, privacy and track are too common in
	// ordinary sentences.
	contextJunkWords = []string{"unsubscribe", "opt-out", "optout", "退订", "取消订阅"}
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
	text = normalizeWidth(text)
	kws := keywordSpans(text)
	if len(kws) == 0 {
		return
	}
	for _, kw := range kws {
		start := kw.start - otpWindow
		if start < 0 {
			start = 0
		}
		end := kw.end + otpWindow
		if end > len(text) {
			end = len(text)
		}
		scanWindow(text, start, end, kw, weight, out)
	}
}

// scanWindow looks for codes in text[start:end] and scores them by their
// distance to kw.
func scanWindow(text string, start, end int, kw span, weight int, out *[]otpCandidate) {
	window := text[start:end]

	for _, loc := range reOTPDigits.FindAllStringIndex(window, -1) {
		s, e := start+loc[0], start+loc[1]
		if !asciiTokenBoundary(text, s, e) || noiseNumber(text, s, e) {
			continue
		}
		dist := keywordGap(kw, s, e)
		if dist > 80 {
			continue
		}
		code := text[s:e]
		score := weight + 60 - dist
		if len(code) == 6 {
			score += 15
		}
		*out = append(*out, otpCandidate{code: code, score: score, pos: s})
	}

	// Digits split by spaces or line breaks, as "1 2 3 4 5 6" or one digit
	// per table cell rendered to text.
	for _, loc := range reSplitDigits.FindAllStringIndex(window, -1) {
		s, e := start+loc[0], start+loc[1]
		code := strings.Join(strings.Fields(text[s:e]), "")
		if len(code) < 4 || len(code) > 8 {
			continue
		}
		if !asciiTokenBoundary(text, s, e) || noiseNumber(text, s, e) {
			continue
		}
		dist := keywordGap(kw, s, e)
		if dist > 80 {
			continue
		}
		score := weight + 60 - dist + 5
		if len(code) == 6 {
			score += 15
		}
		*out = append(*out, otpCandidate{code: code, score: score, pos: s})
	}

	// Letters and digits mixed, only right next to a keyword.
	for _, loc := range reOTPAlnum.FindAllStringIndex(window, -1) {
		s, e := start+loc[0], start+loc[1]
		code := text[s:e]
		if !mixedAlnum(code) || !asciiTokenBoundary(text, s, e) {
			continue
		}
		dist := keywordGap(kw, s, e)
		if dist > 16 {
			continue
		}
		*out = append(*out, otpCandidate{code: code, score: weight + 60 - dist + 5, pos: s})
	}
}

// keywordSpans finds keyword hits without scanning candidates over the whole
// body. It keeps at most otpKeywordMax hits.
func keywordSpans(text string) []span {
	lower := asciiLower(text)
	out := make([]span, 0, 8)
	for _, kw := range cnKeywords {
		for off := 0; len(out) < otpKeywordMax; {
			i := strings.Index(lower[off:], kw)
			if i < 0 {
				break
			}
			s := off + i
			out = append(out, span{s, s + len(kw)})
			off = s + len(kw)
		}
	}
	for _, kw := range enKeywords {
		for off := 0; len(out) < otpKeywordMax; {
			i := boundedIndex(lower, kw, off)
			if i < 0 {
				break
			}
			out = append(out, span{i, i + len(kw)})
			off = i + len(kw)
		}
	}
	return out
}

func keywordGap(k span, start, end int) int {
	switch {
	case start >= k.end:
		return start - k.end
	case end <= k.start:
		return k.start - end
	default:
		return 0
	}
}

// boundedIndex is strings.Index that requires the hit not to be glued to
// other ASCII letters or digits, so "auth" does not match "authors".
func boundedIndex(s, word string, from int) int {
	for from <= len(s)-len(word) {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return -1
		}
		start := from + i
		end := start + len(word)
		if (start == 0 || !isASCIIAlnum(s[start-1])) && (end == len(s) || !isASCIIAlnum(s[end])) {
			return start
		}
		from = start + 1
	}
	return -1
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

func mixedAlnum(s string) bool {
	var letter, digit bool
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			letter = true
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

	// A year, but only when the surrounding text looks like a date.
	if len(code) == 4 && isYear(code) && inDateContext(text, start, end) {
		return true
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

func isYear(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1900 && n <= 2099
}

func inDateContext(text string, start, end int) bool {
	left := start - 6
	if left < 0 {
		left = 0
	}
	right := end + 6
	if right > len(text) {
		right = len(text)
	}
	for _, r := range text[left:start] + text[end:right] {
		if r == '年' || r == '月' || r == '日' {
			return true
		}
	}
	if end < len(text) {
		switch text[end] {
		case '-', '/':
			return true
		case '.':
			// A period only separates a date when a digit follows.
			if end+1 < len(text) && isASCIIDigit(text[end+1]) {
				return true
			}
		}
	}
	if start > 0 {
		switch text[start-1] {
		case '-', '/':
			return true
		case '.':
			if start >= 2 && isASCIIDigit(text[start-2]) {
				return true
			}
		}
	}
	return false
}

// digitRun counts the digits around a match when they are joined only by
// phone-number separators.
func digitRun(text string, start, end int) int {
	count := 0
	for i := start; i < end; i++ {
		if isASCIIDigit(text[i]) {
			count++
		}
	}
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

// normalizeWidth maps full-width ASCII forms to their half-width equivalents
// so １２３４５６ and １２３４５６ are read the same way.
func normalizeWidth(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\u3000':
			b.WriteByte(' ')
		case r >= '\uff01' && r <= '\uff5e':
			b.WriteRune(r - 0xfee0)
		case r == '\uffe5':
			b.WriteRune('¥')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func asciiLower(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
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

	text := joinWrappedURLs(strings.ReplaceAll(m.Text, "\r\n", "\n"))

	var cands []linkCandidate
	cands = append(cands, htmlAnchors(m.HTML)...)
	for _, loc := range reTextURL.FindAllStringIndex(text, -1) {
		if len(cands) >= otpLinkMax {
			break
		}
		u := cleanTextURL(text[loc[0]:loc[1]])
		if u == "" {
			continue
		}
		ctxStart := loc[0] - otpURLContext
		if ctxStart < 0 {
			ctxStart = 0
		}
		cands = append(cands, linkCandidate{
			url:     u,
			context: lastClause(text[ctxStart:loc[0]]),
			order:   len(cands),
		})
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
	url     string
	text    string
	context string
	score   int
	order   int
}

func linkScore(c linkCandidate) int {
	t := strings.ToLower(c.text)
	score := 0
	switch {
	case urlHasAnyWord(c.url, strongLinkWords):
		score += 100
	case urlHasAnyWord(c.url, weakLinkWords):
		score += 60
	}
	switch {
	case hasBoundedAny(t, strongLinkWords):
		score += 80
	case hasBoundedAny(t, weakLinkWords):
		score += 40
	}
	if c.text != "" {
		score += 5
	}
	return score
}

// urlHasAnyWord matches keywords against whole path segments and query tokens,
// and against their camelCase parts, so /verifyEmail counts as "verify".
func urlHasAnyWord(raw string, words []string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return hasBoundedAny(strings.ToLower(raw), words)
	}
	check := func(s string) bool {
		lower := strings.ToLower(s)
		for _, w := range words {
			if lower == w {
				return true
			}
		}
		for _, tok := range splitCamel(s) {
			for _, w := range words {
				if tok == w {
					return true
				}
			}
		}
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg != "" && check(seg) {
			return true
		}
	}
	for key, values := range u.Query() {
		if check(key) {
			return true
		}
		for _, v := range values {
			if check(v) {
				return true
			}
		}
	}
	return false
}

// splitCamel splits a token on punctuation and camelCase boundaries.
func splitCamel(s string) []string {
	runes := []rune(s)
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return out
}

func hasBoundedAny(s string, words []string) bool {
	for _, w := range words {
		if hasBoundedWord(s, w) {
			return true
		}
	}
	return false
}

func hasBoundedWord(s, word string) bool {
	return boundedIndex(s, word, 0) >= 0
}

// junkLink rejects unsubscribe, privacy and help links. URLs are matched by
// path segment or query token, so "help-center/verify" is not a help link;
// text is matched by word, so "authors" is not "auth". The surrounding
// context only counts for unsubscribe-like words.
func junkLink(c linkCandidate) bool {
	if urlHasJunk(c.url) {
		return true
	}
	if textHasJunk(c.text, junkWords) {
		return true
	}
	return textHasJunk(c.context, contextJunkWords)
}

func textHasJunk(s string, words []string) bool {
	s = strings.ToLower(s)
	for _, w := range words {
		if isASCIIWord(w) {
			if hasBoundedWord(s, w) {
				return true
			}
		} else if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func urlHasJunk(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	for _, seg := range strings.Split(strings.ToLower(u.Path), "/") {
		for _, w := range junkWords {
			if isASCIIWord(w) && seg == w {
				return true
			}
		}
	}
	for key, values := range u.Query() {
		for _, v := range append([]string{key}, values...) {
			lv := strings.ToLower(v)
			for _, w := range junkWords {
				if isASCIIWord(w) && lv == w {
					return true
				}
			}
		}
	}
	return false
}

func isASCIIWord(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
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
				text := normalizeText(nodeText(n))
				if text == "" {
					text = normalizeText(htmlAttr(n, "title"))
				}
				// A bare image with no alt or title is not a readable link.
				if text == "" {
					return
				}
				if u := resolveURL(base, htmlAttr(n, "href")); u != "" {
					out = append(out, linkCandidate{
						url:   u,
						text:  text,
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

// joinWrappedURLs joins URLs broken over a line by a 78-column wrap: the line
// ends in a URL and the next line starts with more URL, not with whitespace.
func joinWrappedURLs(text string) string {
	if !strings.Contains(text, "\n") {
		return text
	}
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		line := lines[i]
		if len(line) < 72 || len(line) > 84 {
			continue
		}
		if !reLineEndsURL.MatchString(line) {
			continue
		}
		next := lines[i+1]
		if next == "" || next[0] == ' ' || next[0] == '\t' {
			continue
		}
		token := next
		if j := strings.IndexAny(next, " \t"); j >= 0 {
			token = next[:j]
		}
		if !urlContinuation(token) {
			continue
		}
		lines[i] = line + token
		lines[i+1] = next[len(token):]
	}
	return strings.Join(lines, "\n")
}

// urlContinuation reports whether a token can be the rest of a wrapped URL.
// It must look like URL material (a query or encoding character) and must not
// be a word ending in punctuation or a date.
func urlContinuation(s string) bool {
	if len(s) < 4 {
		return false
	}
	if !strings.ContainsAny(s, "=%&") {
		return false
	}
	if strings.ContainsAny(s[len(s)-1:], ",.;:") {
		return false
	}
	if reDateToken.MatchString(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '.' || c == '_' || c == '~' || c == ':' || c == '/' ||
			c == '?' || c == '#' || c == '[' || c == ']' || c == '@' || c == '!' ||
			c == '$' || c == '&' || c == '\'' || c == '(' || c == ')' || c == '*' ||
			c == '+' || c == ',' || c == ';' || c == '=' || c == '%':
		default:
			return false
		}
	}
	return true
}

var reDateToken = regexp.MustCompile(`^\d{4}[-/.]\d{1,2}[-/.]\d{1,2}$|^\d{1,2}[-/.]\d{1,2}[-/.]\d{4}$`)

// lastClause keeps the part of the context after the last sentence end, so an
// "unsubscribe" earlier in the paragraph does not taint a verify URL. Only
// 。！？!? and a period followed by a space end a sentence; a newline or a
// semicolon does not. An empty result falls back to the whole window.
func lastClause(s string) string {
	cut := -1
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '。' || r == '！' || r == '？' || r == '!' || r == '?':
			cut = i + size
		case r == '.' && i+size < len(s) && s[i+size] == ' ':
			cut = i + size
		}
		i += size
	}
	if cut > 0 {
		if rest := s[cut:]; strings.TrimSpace(rest) != "" {
			return rest
		}
	}
	return s
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
				} else if title := htmlAttr(n, "title"); title != "" {
					parts = append(parts, title)
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
