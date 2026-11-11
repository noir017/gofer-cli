package client

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// searchFilters is the parameter set gofer's search page builds from its
// search box (assets/js/app.js applyKeywordToken in gofer). Gofer full-text
// searches q alone, so the keywords this client supports become their own
// request parameters. Unknown keywords stay in q; known ones this client does
// not implement are rejected, since searching for them as text would almost
// always find nothing.
type searchFilters struct {
	query           string
	from            string
	to              string
	recipientType   string
	subject         string
	body            string
	fromDomain      string
	recipientDomain string
	after           string
	before          string
	unread          bool
	read            bool
	starred         bool
}

// parseSearchQuery splits the key:value tokens out of query. Tokens it does
// not understand stay in the full-text query, as they do in the web UI.
func parseSearchQuery(query string) (searchFilters, error) {
	var filters searchFilters
	var plain []string
	for _, token := range splitSearchTokens(query) {
		if idx := strings.IndexByte(token, ':'); idx > 0 {
			key, value := token[:idx], token[idx+1:]
			if err := unsupportedSearchKeyword(key); err != nil {
				return filters, err
			}
			if filters.apply(key, value) {
				continue
			}
		}
		plain = append(plain, token)
	}
	filters.query = strings.Join(plain, " ")
	return filters, nil
}

// unsupportedSearchKeyword names the search-box keywords this client does not
// translate yet. account: points at the flag that replaces it.
func unsupportedSearchKeyword(key string) error {
	key = strings.ToLower(strings.ReplaceAll(key, "_", "-"))
	switch key {
	case "account":
		return fmt.Errorf("search: unsupported keyword %q; use --account instead", key+":")
	case "tag", "attachment", "filetype", "larger", "smaller",
		"has", "no", "recipient", "recipients", "participant", "contact",
		"q", "query", "text":
		return fmt.Errorf("search: unsupported keyword %q", key+":")
	}
	return nil
}

func (f *searchFilters) apply(key, value string) bool {
	key = strings.ToLower(strings.ReplaceAll(key, "_", "-"))
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	switch key {
	case "from":
		f.from = value
	case "to", "cc", "bcc":
		f.to = value
		f.recipientType = key
	case "subject", "subj":
		f.subject = value
	case "body":
		f.body = value
	case "after":
		f.after = value
	case "before":
		f.before = value
	case "domain", "from-domain", "fromdomain":
		f.fromDomain = value
	case "to-domain", "todomain", "recipient-domain":
		f.recipientDomain = value
	case "is":
		switch value {
		case "unread":
			f.unread = true
		case "read":
			f.read = true
		case "starred":
			f.starred = true
		default:
			return false
		}
	default:
		return false
	}
	return true
}

func (f searchFilters) params() url.Values {
	values := url.Values{
		"q": {f.query},
		// Pin the order: the web UI otherwise uses the user's saved sort.
		"sort_by":    {"date"},
		"sort_order": {"desc"},
	}
	set := func(name, value string) {
		if value != "" {
			values.Set(name, value)
		}
	}
	set("from", f.from)
	set("to", f.to)
	set("recipient_type", f.recipientType)
	set("subject", f.subject)
	set("body", f.body)
	set("from_domain", f.fromDomain)
	set("recipient_domain", f.recipientDomain)
	set("after_date", f.after)
	set("before_date", f.before)
	if f.unread {
		values.Set("unread", "1")
	}
	if f.read {
		values.Set("read", "1")
	}
	if f.starred {
		values.Set("starred", "1")
	}
	return values
}

// splitSearchTokens splits on whitespace and drops single or double quotes,
// which may wrap part or all of a token.
func splitSearchTokens(text string) []string {
	var tokens []string
	var buf strings.Builder
	var quote rune
	for _, r := range text {
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				buf.WriteRune(r)
			}
			continue
		}
		switch {
		case r == '"' || r == '\'':
			quote = r
		case unicode.IsSpace(r):
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		default:
			buf.WriteRune(r)
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}
