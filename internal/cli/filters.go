package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// filterFlags are the message filters every search-based command shares.
type filterFlags struct {
	query   string
	to      string
	from    string
	subject string
	account string
	unread  bool
	limit   int
}

func (f *filterFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVarP(&f.query, "query", "q", "", "search text, appended to the query as is")
	fl.StringVar(&f.to, "to", "", "only messages sent to this address")
	fl.StringVar(&f.from, "from", "", "only messages from this address")
	fl.StringVar(&f.subject, "subject", "", "only messages whose subject contains this text")
	fl.StringVar(&f.account, "account", "", "only this account, by email or id")
	fl.BoolVar(&f.unread, "unread", false, "only unread messages")
	fl.IntVar(&f.limit, "limit", 20, "maximum number of messages")
}

// searchQuery builds gofer's search syntax from the filters:
// to:A from:B subject:"S" account:E followed by -q unchanged.
func (f filterFlags) searchQuery() string {
	parts := make([]string, 0, 5)
	if f.to != "" {
		parts = append(parts, "to:"+searchToken(f.to))
	}
	if f.from != "" {
		parts = append(parts, "from:"+searchToken(f.from))
	}
	if f.subject != "" {
		parts = append(parts, "subject:"+searchQuote(f.subject))
	}
	if f.account != "" {
		parts = append(parts, "account:"+searchToken(f.account))
	}
	if f.query != "" {
		parts = append(parts, f.query)
	}
	return strings.Join(parts, " ")
}

func searchToken(v string) string {
	if strings.ContainsAny(v, " \t\"") {
		return searchQuote(v)
	}
	return v
}

func searchQuote(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}
