package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/model"
)

// filterFlags are the message filters every search-based command shares.
type filterFlags struct {
	query         string
	to            string
	from          string
	subject       string
	account       string
	folder        string
	unread        bool
	hasAttachment bool
	limit         int
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

// registerFolder adds --folder to the commands that list or read one place.
func (f *filterFlags) registerFolder(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.folder, "folder", "", "only this folder, by name or id")
}

// registerAttachments adds --has-attachment to the commands that list.
func (f *filterFlags) registerAttachments(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.hasAttachment, "has-attachment", false, "only messages with an attachment")
}

// options turns the filters into client search options. accountID is the
// resolved id of --account; limit 0 means --limit.
func (f filterFlags) options(accountID string, limit int) client.SearchOptions {
	if limit == 0 {
		limit = f.limit
	}
	return client.SearchOptions{
		Query:         f.query,
		From:          f.from,
		To:            f.to,
		Subject:       f.subject,
		AccountID:     accountID,
		Unread:        f.unread,
		HasAttachment: f.hasAttachment,
		Limit:         limit,
	}
}

// prepareSearch resolves --account and --folder and returns the search
// options with every folder, so the caller can drop trash and name rows. A
// folder list that cannot be read is a warning: a search without folder names
// or without trash filtering still answers the question.
func (a *App) prepareSearch(ctx context.Context, api API, f filterFlags, limit int) (client.SearchOptions, []model.Folder, error) {
	accountID, err := a.resolveAccount(ctx, api, f.account)
	if err != nil {
		return client.SearchOptions{}, nil, err
	}
	opts := f.options(accountID, limit)
	folders, ferr := api.Folders(ctx, "")
	if f.folder != "" {
		if ferr != nil {
			return opts, nil, ferr
		}
		ids, err := a.resolveSearchFolders(folders, f.folder, accountID)
		if err != nil {
			return opts, nil, err
		}
		opts.FolderIDs = ids
		return opts, folders, nil
	}
	if ferr != nil {
		_, _ = fmt.Fprintf(a.Stderr, "gofer-cli: cannot list folders: %v\n", ferr)
		return opts, nil, nil
	}
	return opts, folders, nil
}
