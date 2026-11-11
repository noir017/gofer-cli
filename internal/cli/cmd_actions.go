package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/model"
)

var markActions = map[string]client.Action{
	"read":   client.ActRead,
	"unread": client.ActUnread,
	"star":   client.ActStar,
	"unstar": client.ActUnstar,
}

func (a *App) markCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "mark read|unread|star|unstar <id>... | -",
		Short:   "Set the read or starred state of messages",
		Example: "  gofer-cli mark read 42 43\n  gofer-cli list --unread | gofer-cli mark read -",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			action, ok := markActions[args[0]]
			if !ok {
				return fmt.Errorf("unknown mark action %q, want read, unread, star or unstar", args[0])
			}
			ids, err := a.messageIDs(args[1:])
			if err != nil {
				return err
			}
			api, err := a.connect()
			if err != nil {
				return err
			}
			if err := api.Act(cmd.Context(), action, ids); err != nil {
				return err
			}
			return a.done("")
		},
	}
}

func (a *App) actionCommand(use, short, example string, action client.Action) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   short,
		Example: example,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := a.messageIDs(args)
			if err != nil {
				return err
			}
			api, err := a.connect()
			if err != nil {
				return err
			}
			if err := api.Act(cmd.Context(), action, ids); err != nil {
				return err
			}
			return a.done("")
		},
	}
}

func (a *App) moveCommand() *cobra.Command {
	var folder string
	cmd := &cobra.Command{
		Use:     "move <id>... --folder NAME",
		Short:   "Move messages to a folder",
		Example: "  gofer-cli move 42 --folder Archive",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if folder == "" {
				return errors.New("move: --folder is required")
			}
			ids, err := a.messageIDs(args)
			if err != nil {
				return err
			}
			api, err := a.connect()
			if err != nil {
				return err
			}
			folderID, err := a.resolveFolder(cmd.Context(), api, folder)
			if err != nil {
				return err
			}
			if err := api.Move(cmd.Context(), ids, folderID); err != nil {
				return err
			}
			return a.done("")
		},
	}
	cmd.Flags().StringVar(&folder, "folder", "", "folder name or id")
	return cmd
}

func (a *App) resolveFolder(ctx context.Context, api API, ref string) (string, error) {
	folders, err := api.Folders(ctx, "")
	if err != nil {
		return "", err
	}
	for _, f := range folders {
		if f.ID == ref {
			return f.ID, nil
		}
	}
	for _, f := range folders {
		if strings.EqualFold(f.Name, ref) || strings.EqualFold(f.Path, ref) {
			return f.ID, nil
		}
	}
	return "", fmt.Errorf("%w: folder %s", model.ErrNotFound, ref)
}

func (a *App) accountsCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "accounts",
		Short:   "List the accounts",
		Example: "  gofer-cli accounts",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, err := a.connect()
			if err != nil {
				return err
			}
			accounts, err := api.Accounts(cmd.Context())
			if err != nil {
				return err
			}
			if a.flags.json {
				if accounts == nil {
					accounts = []model.Account{}
				}
				return a.writeJSON(accounts)
			}
			for _, acc := range accounts {
				fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\n", acc.ID, acc.Email, acc.Name, acc.Provider)
			}
			return nil
		},
	}
}

func (a *App) foldersCommand() *cobra.Command {
	var account string
	cmd := &cobra.Command{
		Use:     "folders",
		Short:   "List the folders",
		Example: "  gofer-cli folders --account me@example.com",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, err := a.connect()
			if err != nil {
				return err
			}
			accountID, err := a.resolveAccount(cmd.Context(), api, account)
			if err != nil {
				return err
			}
			folders, err := api.Folders(cmd.Context(), accountID)
			if err != nil {
				return err
			}
			if a.flags.json {
				if folders == nil {
					folders = []model.Folder{}
				}
				return a.writeJSON(folders)
			}
			for _, f := range folders {
				fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\t%d\n", f.ID, f.AccountID, f.Name, f.Role, f.Unread)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "only this account, by email or id")
	return cmd
}

func (a *App) syncCommand() *cobra.Command {
	var account string
	cmd := &cobra.Command{
		Use:     "sync",
		Short:   "Ask the server to check for new mail",
		Example: "  gofer-cli sync --account me@example.com",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, err := a.connect()
			if err != nil {
				return err
			}
			accountID, err := a.resolveAccount(cmd.Context(), api, account)
			if err != nil {
				return err
			}
			if err := api.Sync(cmd.Context(), accountID); err != nil {
				return err
			}
			return a.done("")
		},
	}
	cmd.Flags().StringVar(&account, "account", "", "only this account, by email or id")
	return cmd
}

// messageIDs reads ids from the arguments. A "-" argument reads them from
// stdin instead: one message per line, the id in the first tab- or
// space-separated column, so list output can be piped in.
func (a *App) messageIDs(args []string) ([]string, error) {
	ids := make([]string, 0, len(args))
	fromStdin := false
	for _, arg := range args {
		switch {
		case arg == "-":
			fromStdin = true
		case arg != "":
			ids = append(ids, arg)
		}
	}
	if fromStdin {
		sc := bufio.NewScanner(a.Stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if i := strings.IndexAny(line, "\t "); i >= 0 {
				line = line[:i]
			}
			if line != "" {
				ids = append(ids, line)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("no message ids")
	}
	return ids, nil
}
