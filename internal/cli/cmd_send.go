package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/model"
)

func (a *App) sendCommand() *cobra.Command {
	var (
		from, subject, body, bodyFile string
		to, cc, bcc, attach           []string
	)
	cmd := &cobra.Command{
		Use:   "send [--from A] --to B[,C] [--subject S]",
		Short: "Send a message",
		Long: "Send a message. Only --to is required.\n\n" +
			"The body comes from --body, from --body-file, or from stdin when stdin is not a terminal:\n" +
			"  printf 'hello' | gofer-cli send --to ana@example.com",
		Example: "  gofer-cli send --from me@example.com --to ana@example.com --subject Hi --body 'hello'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			toList, ccList, bccList := addressList(to), addressList(cc), addressList(bcc)
			if len(toList) == 0 {
				return errors.New("send: --to is required")
			}
			if err := exclusiveBody(cmd); err != nil {
				return err
			}
			text, err := a.bodyText(body, bodyFile)
			if err != nil {
				return err
			}
			api, err := a.connect()
			if err != nil {
				return err
			}
			err = api.Send(cmd.Context(), client.SendRequest{
				From:        from,
				To:          toList,
				Cc:          ccList,
				Bcc:         bccList,
				Subject:     subject,
				Text:        text,
				Attachments: attach,
			})
			if err != nil {
				return err
			}
			return a.done("sent")
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&from, "from", "", "account email or identity to send from")
	fl.StringSliceVar(&to, "to", nil, "recipients, comma separated")
	fl.StringSliceVar(&cc, "cc", nil, "cc recipients, comma separated")
	fl.StringSliceVar(&bcc, "bcc", nil, "bcc recipients, comma separated")
	fl.StringVar(&subject, "subject", "", "subject (optional)")
	fl.StringVar(&body, "body", "", "body text")
	fl.StringVar(&bodyFile, "body-file", "", "file to read the body from")
	fl.StringSliceVar(&attach, "attach", nil, "file to attach, repeatable")
	return cmd
}

func (a *App) replyCommand() *cobra.Command {
	var (
		all            bool
		from           string
		body, bodyFile string
		attach         []string
	)
	cmd := &cobra.Command{
		Use:     "reply <id> [--from A]",
		Short:   "Reply to a message",
		Example: "  gofer-cli reply 42 --all --body 'thanks'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := exclusiveBody(cmd); err != nil {
				return err
			}
			api, err := a.connect()
			if err != nil {
				return err
			}
			m, _, err := a.fetchMessage(ctx, api, model.MessageRef{ID: args[0]})
			if err != nil {
				return err
			}
			to := emails(m.ReplyTo)
			if len(to) == 0 {
				to = emails(m.From)
			}
			if len(to) == 0 {
				return fmt.Errorf("%w: message %s has no sender", model.ErrNotFound, m.ID)
			}
			var cc []string
			if all {
				own, err := a.ownAddresses(ctx, api)
				if err != nil {
					return err
				}
				cc = without(append(emails(m.To), emails(m.Cc)...), append(to, own...))
			}
			text, err := a.bodyText(body, bodyFile)
			if err != nil {
				return err
			}
			// With no --from the client picks the account the message came
			// from, using ReplyTo.
			err = api.Send(ctx, client.SendRequest{
				From:        from,
				To:          to,
				Cc:          cc,
				Subject:     replySubject(m.Subject),
				Text:        text,
				Attachments: attach,
				ReplyTo:     m.ID,
			})
			if err != nil {
				return err
			}
			return a.done("sent")
		},
	}
	fl := cmd.Flags()
	fl.BoolVar(&all, "all", false, "reply to every recipient")
	fl.StringVar(&from, "from", "", "account email or identity to send from")
	fl.StringVar(&body, "body", "", "body text")
	fl.StringVar(&bodyFile, "body-file", "", "file to read the body from")
	fl.StringSliceVar(&attach, "attach", nil, "file to attach, repeatable")
	return cmd
}

// exclusiveBody rejects --body together with --body-file.
func exclusiveBody(cmd *cobra.Command) error {
	if cmd.Flags().Changed("body") && cmd.Flags().Changed("body-file") {
		return fmt.Errorf("%s: --body and --body-file are mutually exclusive", cmd.Name())
	}
	return nil
}

// ownAddresses lists the addresses of every configured account, so a reply
// all does not cc the user.
func (a *App) ownAddresses(ctx context.Context, api API) ([]string, error) {
	accounts, err := api.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(accounts))
	for _, acc := range accounts {
		if acc.Email != "" {
			out = append(out, acc.Email)
		}
	}
	return out, nil
}

// bodyText takes the body from --body, then --body-file, then stdin when
// stdin is not a terminal.
func (a *App) bodyText(body, file string) (string, error) {
	switch {
	case body != "":
		return body, nil
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case !a.StdinIsTerminal():
		b, err := io.ReadAll(a.Stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		return "", nil
	}
}

func replySubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "re:") {
		return subject
	}
	return "Re: " + subject
}

// addressList drops the empty entries a trailing comma leaves behind.
func addressList(list []string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
