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

// markResults is what each mark action reports for every message.
var markResults = map[string]string{
	"read":   "read",
	"unread": "unread",
	"star":   "starred",
	"unstar": "unstarred",
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
			return a.writeResults(ids, markResults[args[0]])
		},
	}
}

// actionCommand builds archive, spam and delete. result is what each changed
// message reports: the place it went to.
func (a *App) actionCommand(use, short, example, result string, action client.Action) *cobra.Command {
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
			return a.writeResults(ids, result)
		},
	}
}

func (a *App) moveCommand() *cobra.Command {
	var folder, account string
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
			target, err := a.resolveFolder(cmd.Context(), api, folder, ids, account)
			if err != nil {
				return err
			}
			if err := api.Move(cmd.Context(), ids, target.ID); err != nil {
				return err
			}
			return a.writeResults(ids, target.Name)
		},
	}
	cmd.Flags().StringVar(&folder, "folder", "", "folder name or id")
	cmd.Flags().StringVar(&account, "account", "", "account the messages belong to, by email or id")
	return cmd
}

// resolveFolder turns --folder into a folder. An id is taken as is. A name
// that matches one folder is used at once; when several accounts have such a
// folder, the account of the messages decides, and --account overrides that.
func (a *App) resolveFolder(ctx context.Context, api API, ref string, ids []string, account string) (model.Folder, error) {
	folders, err := api.Folders(ctx, "")
	if err != nil {
		return model.Folder{}, err
	}
	scope := ""
	if account != "" {
		if scope, err = a.resolveAccount(ctx, api, account); err != nil {
			return model.Folder{}, err
		}
	}
	return a.resolveFolderIn(ctx, api, folders, ref, ids, scope)
}

// resolveFolderIn is resolveFolder over a folder list and an account id the
// caller already has.
func (a *App) resolveFolderIn(ctx context.Context, api API, folders []model.Folder, ref string, ids []string, scope string) (model.Folder, error) {
	for _, f := range folders {
		if f.ID == ref {
			return f, nil
		}
	}

	var err error
	matches := foldersNamed(folders, ref, scope)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return model.Folder{}, a.noSuchFolder(ctx, api, folders, ref, ids, scope)
	}

	// Several folders share the name: narrow them down to the account of the
	// messages unless --account already did.
	if scope == "" {
		if scope, err = a.accountForIDs(ctx, api, ids); err != nil {
			return model.Folder{}, err
		}
	}
	if scope == "" {
		return model.Folder{}, fmt.Errorf("folder name %q matches %d folders; use the folder id or --account (%s)",
			ref, len(matches), folderIDs(matches))
	}
	matches = foldersNamed(folders, ref, scope)
	switch len(matches) {
	case 0:
		return model.Folder{}, a.noSuchFolder(ctx, api, folders, ref, ids, scope)
	case 1:
		return matches[0], nil
	default:
		return model.Folder{}, fmt.Errorf("folder name %q matches %d folders in account %s; use one of these ids: %s",
			ref, len(matches), scope, folderIDs(matches))
	}
}

// maxSearchFolders is gofer's cap on the folders one scoped search may name
// (storage.MaxMailSearchFolders).
const maxSearchFolders = 200

// resolveSearchFolders turns --folder into the folders a search covers.
// Unlike move, a name several accounts share is not an error: a listing
// across accounts searches all of them, and --account narrows the choice.
func (a *App) resolveSearchFolders(folders []model.Folder, ref, accountID string) ([]string, error) {
	for _, f := range folders {
		if f.ID == ref {
			return []string{f.ID}, nil
		}
	}
	matches := foldersNamed(folders, ref, accountID)
	if len(matches) == 0 {
		available := folders
		if accountID != "" {
			available = foldersOf(folders, accountID)
		}
		return nil, fmt.Errorf("%w: folder %s; available folders: %s", model.ErrNotFound, ref, folderChoices(available))
	}
	if len(matches) > maxSearchFolders {
		return nil, fmt.Errorf("folder name %q matches %d folders; narrow it with --account (at most %d folders per search)",
			ref, len(matches), maxSearchFolders)
	}
	ids := make([]string, 0, len(matches))
	for _, f := range matches {
		ids = append(ids, f.ID)
	}
	return ids, nil
}

// noSuchFolder names the folders the caller could have asked for. When the
// account is not known yet it is taken from the messages; a listing of every
// folder is the fallback, since a wrong list still beats no hint at all.
func (a *App) noSuchFolder(ctx context.Context, api API, folders []model.Folder, ref string, ids []string, scope string) error {
	if scope == "" {
		scope, _ = a.accountForIDs(ctx, api, ids)
	}
	available := folders
	if scope != "" {
		available = foldersOf(folders, scope)
	}
	return fmt.Errorf("%w: folder %s; available folders: %s", model.ErrNotFound, ref, folderChoices(available))
}

// foldersNamed returns the folders called ref, limited to accountID when it is
// not empty.
func foldersNamed(folders []model.Folder, ref, accountID string) []model.Folder {
	matches := make([]model.Folder, 0, 2)
	for _, f := range folders {
		if accountID != "" && f.AccountID != accountID {
			continue
		}
		if strings.EqualFold(f.Name, ref) || strings.EqualFold(f.Path, ref) {
			matches = append(matches, f)
		}
	}
	return matches
}

func folderIDs(folders []model.Folder) string {
	ids := make([]string, 0, len(folders))
	for _, f := range folders {
		ids = append(ids, f.ID)
	}
	return strings.Join(ids, ", ")
}

func (a *App) accountsCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "accounts",
		Short:   "List the accounts",
		Long:    "List the accounts.\n\nText columns: id, email, name, provider.",
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
				_, _ = fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\n", cleanText(acc.ID), cleanText(acc.Email), cleanText(acc.Name), cleanText(acc.Provider))
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
		Long:    "List the folders.\n\nText columns: id, account, name, role, unread.",
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
				_, _ = fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\t%d\n", cleanText(f.ID), cleanText(f.AccountID), cleanText(f.Name), cleanText(f.Role), f.Unread)
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
		if a.StdinIsTerminal() {
			return nil, errors.New("message ids come from stdin, but stdin is a terminal; pass ids or pipe them in")
		}
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
