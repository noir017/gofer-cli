package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/model"
)

const (
	pollInterval = 3 * time.Second
	syncInterval = 15 * time.Second
)

// findFlags are the flags of otp and link: the shared filters plus a time
// window and a wait budget.
type findFlags struct {
	filterFlags
	since time.Duration
	wait  int
}

func (f *findFlags) register(cmd *cobra.Command) {
	f.filterFlags.register(cmd)
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

// findMessage looks at the messages of the last since, newest first, and
// returns the first value match finds. With wait > 0 it keeps searching every
// pollInterval, asking the server to sync every syncInterval, until the wait
// budget is spent.
func (a *App) findMessage(ctx context.Context, f filterFlags, since, wait time.Duration, match func(*model.Message) (string, bool)) (string, *model.Message, error) {
	api, err := a.connect()
	if err != nil {
		return "", nil, err
	}
	accountID, err := a.resolveAccount(ctx, api, f.account)
	if err != nil {
		return "", nil, err
	}
	opts := searchOptions(f, accountID, 0)
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
			raw, err := api.Source(ctx, ref.ID)
			if err != nil {
				if errors.Is(err, model.ErrNotFound) {
					continue // the message went away between search and read
				}
				return "", nil, err
			}
			m, err := a.Parse(raw)
			if err != nil {
				continue
			}
			m.ID = ref.ID
			if m.Account == "" {
				m.Account = ref.Account
			}
			if !m.Date.IsZero() && m.Date.Before(cutoff) {
				break
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
	fmt.Fprintln(a.Stdout, value)
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
			opts := searchOptions(f, accountID, 0)
			refs, err := api.Search(ctx, opts)
			if err != nil {
				return err
			}
			seen := make(map[string]bool, len(refs))
			for _, ref := range refs {
				seen[ref.ID] = true
			}
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
					if seen[ref.ID] {
						continue
					}
					seen[ref.ID] = true
					m, src, err := a.fetchMessage(ctx, api, ref)
					if err != nil {
						continue
					}
					return a.printRead(m, src, false, false)
				}
				if a.Now().Sub(lastSync) >= syncInterval {
					_ = api.Sync(ctx, "")
					lastSync = a.Now()
				}
			}
			return fmt.Errorf("%w: no new message within %s", model.ErrNotFound, time.Duration(timeout)*time.Second)
		},
	}
	f.register(cmd)
	cmd.Flags().IntVar(&timeout, "timeout", 120, "give up after this many seconds")
	return cmd
}
