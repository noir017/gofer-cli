package cli

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/model"
)

func (a *App) listCommand() *cobra.Command {
	var f filterFlags
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List messages, newest first",
		Example: "  gofer-cli list --from alerts@example.com --unread --limit 5",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			msgs, err := a.searchMessages(cmd.Context(), f)
			if err != nil {
				return err
			}
			return a.writeList(msgs)
		},
	}
	f.register(cmd)
	return cmd
}

func (a *App) readCommand() *cobra.Command {
	var raw, html bool
	cmd := &cobra.Command{
		Use:     "read <id>",
		Short:   "Print one message",
		Example: "  gofer-cli read 42\n  gofer-cli read 42 --raw",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := a.connect()
			if err != nil {
				return err
			}
			if raw {
				src, err := api.Source(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				_, err = a.Stdout.Write(src)
				return err
			}
			m, src, err := a.fetchMessage(cmd.Context(), api, model.MessageRef{ID: args[0]})
			if err != nil {
				return err
			}
			return a.printRead(m, src, false, html)
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "print the message exactly as received")
	cmd.Flags().BoolVar(&html, "html", false, "print the HTML part")
	return cmd
}

func (a *App) latestCommand() *cobra.Command {
	var f filterFlags
	var raw, html bool
	cmd := &cobra.Command{
		Use:     "latest",
		Short:   "Print the newest message that matches",
		Example: "  gofer-cli latest --from alerts@example.com",
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
			refs, err := api.Search(ctx, searchOptions(f, accountID, 1))
			if err != nil {
				return err
			}
			if len(refs) == 0 {
				return fmt.Errorf("%w: no message matches", model.ErrNotFound)
			}
			m, src, err := a.fetchMessage(ctx, api, refs[0])
			if err != nil {
				return err
			}
			return a.printRead(m, src, raw, html)
		},
	}
	f.register(cmd)
	cmd.Flags().BoolVar(&raw, "raw", false, "print the message exactly as received")
	cmd.Flags().BoolVar(&html, "html", false, "print the HTML part")
	return cmd
}

func (a *App) attachmentsCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "attachments <id>",
		Short:   "List the attachments of a message",
		Example: "  gofer-cli attachments 42",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := a.connect()
			if err != nil {
				return err
			}
			m, _, err := a.fetchMessage(cmd.Context(), api, model.MessageRef{ID: args[0]})
			if err != nil {
				return err
			}
			if a.flags.json {
				list := m.Attachments
				if list == nil {
					list = []model.Attachment{}
				}
				return a.writeJSON(list)
			}
			for _, att := range m.Attachments {
				fmt.Fprintf(a.Stdout, "%d\t%s\t%s\t%d\n", att.Index, att.Filename, att.ContentType, att.Size)
			}
			return nil
		},
	}
}

func (a *App) saveCommand() *cobra.Command {
	var index int
	var dir string
	cmd := &cobra.Command{
		Use:     "save <id>",
		Short:   "Write the attachments of a message to a directory",
		Example: "  gofer-cli save 42 -o /tmp/attachments",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := a.connect()
			if err != nil {
				return err
			}
			m, src, err := a.fetchMessage(cmd.Context(), api, model.MessageRef{ID: args[0]})
			if err != nil {
				return err
			}
			if index > 0 && index > len(m.Attachments) {
				return fmt.Errorf("%w: message %s has no attachment %d", model.ErrNotFound, m.ID, index)
			}
			written := make([]string, 0, len(m.Attachments))
			for i := range m.Attachments {
				n := i + 1
				if index > 0 && n != index {
					continue
				}
				att, data, err := a.AttachmentData(src, n)
				if err != nil {
					return fmt.Errorf("attachment %d of %s: %w", n, m.ID, err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				target := filepath.Join(dir, safeFilename(att.Filename, n))
				if err := os.WriteFile(target, data, 0o644); err != nil {
					return err
				}
				written = append(written, target)
			}
			if a.flags.json {
				return a.writeJSON(struct {
					Files []string `json:"files"`
				}{written})
			}
			for _, p := range written {
				fmt.Fprintln(a.Stdout, p)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&index, "index", 0, "only the attachment with this index")
	cmd.Flags().StringVarP(&dir, "out", "o", ".", "directory to write to")
	return cmd
}

// safeFilename keeps an attachment inside the output directory: the name is
// reduced to its last element and control characters are dropped.
func safeFilename(name string, index int) string {
	name = strings.ReplaceAll(name, `\`, "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || name == "/" {
		return fmt.Sprintf("attachment-%d", index)
	}
	return name
}

// searchOptions builds the client options for f; limit 0 means --limit.
func searchOptions(f filterFlags, accountID string, limit int) client.SearchOptions {
	if limit == 0 {
		limit = f.limit
	}
	return client.SearchOptions{
		Query:     f.searchQuery(),
		AccountID: accountID,
		Unread:    f.unread,
		Limit:     limit,
	}
}
