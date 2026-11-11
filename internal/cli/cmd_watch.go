package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/model"
)

const (
	pollInterval = 3 * time.Second
	syncInterval = 15 * time.Second
	findLimit    = 50  // otp and link look further back than list
	waitLimit    = 200 // wait may sit for minutes, so it looks further
	dateFormat   = "2006-01-02"
)

// findFlags are the flags of otp and link: the shared filters plus a time
// window and a wait budget.
type findFlags struct {
	filterFlags
	since time.Duration
	wait  int
}

func (f *findFlags) register(cmd *cobra.Command) {
	f.registerLimit(cmd, findLimit)
	cmd.Flags().DurationVar(&f.since, "since", 10*time.Minute, "only messages newer than this")
	cmd.Flags().IntVar(&f.wait, "wait", 0, "keep looking for this many seconds")
}

func (a *App) otpCommand() *cobra.Command {
	var f findFlags
	var pattern string
	cmd := &cobra.Command{
		Use:     "otp",
		Short:   "Print the verification code of the newest matching message",
		Example: "  gofer-cli otp --from no-reply@example.com --wait 60",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkRegexp("otp", "--pattern", pattern); err != nil {
				return err
			}
			code, m, err := a.findMessage(cmd.Context(), f.filterFlags, f.since, time.Duration(f.wait)*time.Second,
				func(m *model.Message) (string, bool) { return a.OTP(m, pattern) })
			if err != nil {
				return err
			}
			return a.writeFound("code", code, m)
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&pattern, "pattern", "", "regexp the code must match")
	return cmd
}

func (a *App) linkCommand() *cobra.Command {
	var f findFlags
	var match string
	cmd := &cobra.Command{
		Use:     "link",
		Short:   "Print the sign-in link of the newest matching message",
		Example: "  gofer-cli link --from no-reply@example.com --wait 60",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkRegexp("link", "--match", match); err != nil {
				return err
			}
			link, m, err := a.findMessage(cmd.Context(), f.filterFlags, f.since, time.Duration(f.wait)*time.Second,
				func(m *model.Message) (string, bool) { return a.Link(m, match) })
			if err != nil {
				return err
			}
			return a.writeFound("link", link, m)
		},
	}
	f.register(cmd)
	cmd.Flags().StringVar(&match, "match", "", "regexp the link must match")
	return cmd
}

// checkRegexp rejects a flag value that is not a valid regexp before any mail
// is read, so a typo is reported as a usage error instead of "nothing found".
func checkRegexp(command, flag, pattern string) error {
	if pattern == "" {
		return nil
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("%s: %s: %w", command, flag, err)
	}
	return nil
}

// outgoingFolders returns the ids of the folders a message that arrived never
// sits in: sent and drafts. otp, link and wait look for mail that arrived, so
// the copy of a message the caller has just sent must not match. Trash is not
// skipped: verification mail often ends up in spam and is thrown away from
// there. A failure to list folders is a warning, not an error: the search
// then simply matches every folder.
func (a *App) outgoingFolders(ctx context.Context, api API) map[string]bool {
	folders, err := api.Folders(ctx, "")
	if err != nil {
		_, _ = fmt.Fprintf(a.Stderr, "gofer-cli: cannot list folders, sent and draft copies are not skipped: %v\n", err)
		return nil
	}
	ids := make(map[string]bool)
	for _, folder := range folders {
		switch strings.ToLower(folder.Role) {
		case "sent", "drafts":
			ids[folder.ID] = true
		}
	}
	return ids
}

// findMessage looks at the messages of the last since, newest first, and
// returns the first value match finds. With wait > 0 it keeps searching every
// pollInterval, asking the server to sync every syncInterval, until the wait
// budget is spent. Messages already read are not downloaded again.
func (a *App) findMessage(ctx context.Context, f filterFlags, since, wait time.Duration, match func(*model.Message) (string, bool)) (string, *model.Message, error) {
	api, err := a.connect()
	if err != nil {
		return "", nil, err
	}
	accountID, err := a.resolveAccount(ctx, api, f.account)
	if err != nil {
		return "", nil, err
	}
	opts := f.options(accountID, 0)
	// The server's after_date takes a date, not a time, and the caller's
	// zone decides which date it is. Ask for one day more and let the exact
	// cutoff below drop what is older.
	opts.After = a.Now().In(a.location()).Add(-since).AddDate(0, 0, -1).Format(dateFormat)
	outgoing := a.outgoingFolders(ctx, api)
	checked := make(map[string]bool)
	deadline := a.Now().Add(wait)
	lastSync := a.Now()
	if wait > 0 {
		_ = api.Sync(ctx, "")
		lastSync = a.Now()
	}
	for {
		refs, err := api.Search(ctx, opts)
		if err != nil {
			return "", nil, err
		}
		cutoff := a.Now().Add(-since)
		for _, ref := range refs {
			// A conversation row is kept even when its newest message is a
			// sent copy: the received mail is still in that conversation.
			if !ref.Thread && outgoing[ref.FolderID] {
				continue // a sent or draft copy, not mail that arrived
			}
			if checked[ref.ID] {
				continue
			}
			raw, err := api.Source(ctx, ref.ID)
			if err != nil {
				if errors.Is(err, model.ErrNotFound) {
					continue // the message went away between search and read
				}
				return "", nil, err
			}
			m, err := a.Parse(raw)
			if err != nil {
				continue // not marked as checked: retried on the next poll
			}
			checked[ref.ID] = true
			m.ID = ref.ID
			m.Thread = ref.Thread
			if m.Account == "" {
				m.Account = ref.Account
			}
			// An undated message is not inside the window.
			if m.Date.IsZero() || m.Date.Before(cutoff) {
				continue
			}
			if value, ok := match(m); ok {
				return value, m, nil
			}
		}
		if wait <= 0 || !a.Now().Before(deadline) {
			break
		}
		if a.Now().Sub(lastSync) >= syncInterval {
			_ = api.Sync(ctx, "")
			lastSync = a.Now()
		}
		a.Sleep(pollInterval)
	}
	return "", nil, fmt.Errorf("%w: nothing in the last %s", model.ErrNotFound, since)
}

type foundJSON struct {
	Code    string    `json:"code,omitempty"`
	Link    string    `json:"link,omitempty"`
	ID      string    `json:"id"`
	Subject string    `json:"subject"`
	From    string    `json:"from"`
	Date    time.Time `json:"date"`
}

func (a *App) writeFound(kind, value string, m *model.Message) error {
	if a.flags.json {
		out := foundJSON{ID: m.ID, Subject: m.Subject, From: firstEmail(m.From), Date: m.Date}
		if kind == "code" {
			out.Code = value
		} else {
			out.Link = value
		}
		return a.writeJSON(out)
	}
	_, _ = fmt.Fprintln(a.Stdout, cleanText(value))
	return nil
}

func (a *App) waitCommand() *cobra.Command {
	var f filterFlags
	var timeout int
	cmd := &cobra.Command{
		Use:     "wait",
		Short:   "Wait for a new message and print it",
		Example: "  gofer-cli wait --from ci@example.com --timeout 300",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			api, err := a.connect()
			if err != nil {
				return err
			}
			accountID, err := a.resolveAccount(ctx, api, f.account)
			if err != nil {
				return err
			}
			opts := f.options(accountID, 0)
			outgoing := a.outgoingFolders(ctx, api)
			refs, err := api.Search(ctx, opts)
			if err != nil {
				return err
			}
			before := make(map[string]bool, len(refs))
			for _, ref := range refs {
				before[ref.ID] = true
			}
			checked := make(map[string]bool)
			deadline := a.Now().Add(time.Duration(timeout) * time.Second)
			lastSync := a.Now()
			_ = api.Sync(ctx, "")
			for a.Now().Before(deadline) {
				a.Sleep(pollInterval)
				refs, err := api.Search(ctx, opts)
				if err != nil {
					return err
				}
				for _, ref := range refs {
					// A conversation row is kept even when its newest
					// message is a sent copy: the received mail is still in
					// that conversation.
					if !ref.Thread && outgoing[ref.FolderID] {
						continue // a sent or draft copy, not mail that arrived
					}
					if before[ref.ID] || checked[ref.ID] {
						continue
					}
					raw, err := api.Source(ctx, ref.ID)
					if err != nil {
						if errors.Is(err, model.ErrNotFound) {
							continue
						}
						_, _ = fmt.Fprintf(a.Stderr, "gofer-cli: reading message %s: %v\n", ref.ID, err)
						continue // try again on the next round
					}
					m, err := a.Parse(raw)
					if err != nil {
						continue
					}
					checked[ref.ID] = true
					m.ID = ref.ID
					m.Thread = ref.Thread
					if m.Account == "" {
						m.Account = ref.Account
					}
					return a.printMessage(m, false)
				}
				if a.Now().Sub(lastSync) >= syncInterval {
					_ = api.Sync(ctx, "")
					lastSync = a.Now()
				}
			}
			return fmt.Errorf("%w: no new message within %s", model.ErrNotFound, time.Duration(timeout)*time.Second)
		},
	}
	f.registerLimit(cmd, waitLimit)
	cmd.Flags().IntVar(&timeout, "timeout", 120, "give up after this many seconds")
	return cmd
}
