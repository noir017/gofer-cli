package cli

import (
	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/client"
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
	f.registerLimit(cmd, 20)
}

// registerLimit adds the filters with a command-specific --limit default:
// otp, link and wait look further back than a listing.
func (f *filterFlags) registerLimit(cmd *cobra.Command, limit int) {
	fl := cmd.Flags()
	fl.StringVarP(&f.query, "query", "q", "", "free-text search, passed as is")
	fl.StringVar(&f.to, "to", "", "only messages sent to this address")
	fl.StringVar(&f.from, "from", "", "only messages from this address")
	fl.StringVar(&f.subject, "subject", "", "only messages whose subject contains this text")
	fl.StringVar(&f.account, "account", "", "only this account, by email or id")
	fl.BoolVar(&f.unread, "unread", false, "only unread messages")
	fl.IntVar(&f.limit, "limit", limit, "maximum number of messages")
}

// options turns the filters into client search options. accountID is the
// resolved id of --account; limit 0 means --limit.
func (f filterFlags) options(accountID string, limit int) client.SearchOptions {
	if limit == 0 {
		limit = f.limit
	}
	return client.SearchOptions{
		Query:     f.query,
		From:      f.from,
		To:        f.to,
		Subject:   f.subject,
		AccountID: accountID,
		Unread:    f.unread,
		Limit:     limit,
	}
}
