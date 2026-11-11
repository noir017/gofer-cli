package cli

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/model"
)

func (a *App) listCommand() *cobra.Command {
	var f filterFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List messages, newest first",
		Long: "List messages, newest first.\n\n" +
			"Text columns: id, account, folder, date, from, to, subject.\n" +
			"Messages in Trash are left out unless --folder names it.",
		Example: "  gofer-cli list --from alerts@example.com --unread --limit 5",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			msgs, err := a.searchMessages(cmd.Context(), f)
			if err != nil {
				return err
			}
			if len(msgs) == 0 && f.to != "" {
				_, _ = fmt.Fprintln(a.Stderr, "gofer-cli: --to matches the To header; to pick a mailbox use --account <email>")
			}
			return a.writeList(msgs)
		},
	}
	f.register(cmd)
	f.registerFolder(cmd)
	f.registerAttachments(cmd)
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
			if err := a.exclusiveOutput(raw, html); err != nil {
				return err
			}
			api, err := a.connect()
			if err != nil {
				return err
			}
			var names map[string]string
			if !raw {
				names = folderNames(a.allFolders(cmd.Context(), api))
			}
			return a.printRef(cmd.Context(), api, model.MessageRef{ID: args[0]}, raw, html, names)
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
			if err := a.exclusiveOutput(raw, html); err != nil {
				return err
			}
			ctx := cmd.Context()
			api, err := a.connect()
			if err != nil {
				return err
			}
			opts, folders, err := a.prepareSearch(ctx, api, f, findLimit)
			if err != nil {
				return err
			}
			refs, err := api.Search(ctx, opts)
			if err != nil {
				return err
			}
			if len(opts.FolderIDs) == 0 {
				refs = withoutTrash(refs, folders)
			}
			if len(refs) == 0 {
				return fmt.Errorf("%w: no message matches", model.ErrNotFound)
			}
			return a.printRef(ctx, api, refs[0], raw, html, folderNames(folders))
		},
	}
	f.register(cmd)
	f.registerFolder(cmd)
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
			if len(m.Attachments) == 0 {
				_, _ = fmt.Fprintf(a.Stderr, "gofer-cli: message %s has no attachments\n", m.ID)
			}
			if a.flags.json {
				list := m.Attachments
				if list == nil {
					list = []model.Attachment{}
				}
				return a.writeJSON(list)
			}
			for _, att := range m.Attachments {
				_, _ = fmt.Fprintf(a.Stdout, "%d\t%s\t%s\t%d\n", att.Index, cleanText(att.Filename), cleanText(att.ContentType), att.Size)
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
			if index < 0 {
				return fmt.Errorf("save: --index must not be negative")
			}
			api, err := a.connect()
			if err != nil {
				return err
			}
			m, src, err := a.fetchMessage(cmd.Context(), api, model.MessageRef{ID: args[0]})
			if err != nil {
				return err
			}
			if len(m.Attachments) == 0 {
				return notFoundf("message %s has no attachments", m.ID)
			}
			written := make([]string, 0, len(m.Attachments))
			for _, att := range m.Attachments {
				if index > 0 && att.Index != index {
					continue
				}
				got, data, err := a.AttachmentData(src, att.Index)
				if err != nil {
					return fmt.Errorf("attachment %d of %s: %w", att.Index, m.ID, err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return err
				}
				target := freePath(dir, safeFilename(got.Filename, att.Index))
				if err := os.WriteFile(target, data, 0o644); err != nil {
					return err
				}
				written = append(written, target)
			}
			if index > 0 && len(written) == 0 {
				return fmt.Errorf("%w: message %s has no attachment %d", model.ErrNotFound, m.ID, index)
			}
			if a.flags.json {
				return a.writeJSON(struct {
					Files []string `json:"files"`
				}{written})
			}
			for _, p := range written {
				_, _ = fmt.Fprintln(a.Stdout, p)
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

// freePath returns dir/name, or dir/name-1, dir/name-2 and so on when that
// file already exists, so a save never overwrites anything.
func freePath(dir, name string) string {
	target := filepath.Join(dir, name)
	if !exists(target) {
		return target
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		target = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
		if !exists(target) {
			return target
		}
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
