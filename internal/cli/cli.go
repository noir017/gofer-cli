// Package cli is the gofer-cli command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/config"
	"github.com/noir017/gofer-cli/internal/extract"
	"github.com/noir017/gofer-cli/internal/message"
	"github.com/noir017/gofer-cli/internal/model"
)

// Version is the release version. Set it at build time with
// -ldflags -X github.com/noir017/gofer-cli/internal/cli.Version=1.2.3.
var Version = "dev"

// Exit codes.
const (
	exitOK       = 0
	exitError    = 1
	exitNotFound = 2
	exitAuth     = 3
)

// API is what the commands need from internal/client. The real
// implementation is *client.Client; tests use their own.
type API interface {
	Login(ctx context.Context) error
	Logout(ctx context.Context) error
	Info(ctx context.Context) (model.ServerInfo, error)
	Search(ctx context.Context, opts client.SearchOptions) ([]model.MessageRef, error)
	Source(ctx context.Context, id string) ([]byte, error)
	Act(ctx context.Context, action client.Action, ids []string) error
	Move(ctx context.Context, ids []string, folderID string) error
	Sync(ctx context.Context, accountID string) error
	Accounts(ctx context.Context) ([]model.Account, error)
	Folders(ctx context.Context, accountID string) ([]model.Folder, error)
	Send(ctx context.Context, req client.SendRequest) error
}

// App holds everything a command needs. Every dependency that touches the
// network, the clock, the filesystem or the terminal is a field so tests can
// replace it.
type App struct {
	API             API
	Parse           func([]byte) (*model.Message, error)
	AttachmentData  func([]byte, int) (model.Attachment, []byte, error)
	OTP             func(*model.Message, string) (string, bool)
	Link            func(*model.Message, string) (string, bool)
	Now             func() time.Time
	Sleep           func(time.Duration)
	Location        *time.Location // where text output shows dates; nil = local time
	Stdin           io.Reader
	Stdout          io.Writer
	Stderr          io.Writer
	StdinIsTerminal func() bool
	Version         string

	loadConfig func() (config.Config, error)
	newClient  func(config.Config) (API, error)

	flags globalFlags
}

type globalFlags struct {
	url        string
	user       string
	configFile string
	json       bool
}

// New returns an App wired to the real client, clock and terminal.
func New() *App {
	a := &App{
		Parse:           message.Parse,
		AttachmentData:  message.AttachmentData,
		OTP:             extract.OTP,
		Link:            extract.Link,
		Now:             time.Now,
		Sleep:           time.Sleep,
		Stdin:           os.Stdin,
		Stdout:          os.Stdout,
		Stderr:          os.Stderr,
		StdinIsTerminal: stdinIsTerminal,
		Version:         Version,
	}
	a.loadConfig = func() (config.Config, error) {
		return config.Load(config.Config{
			URL:        a.flags.url,
			User:       a.flags.user,
			ConfigFile: a.flags.configFile,
		})
	}
	a.newClient = func(cfg config.Config) (API, error) {
		c, err := client.New(cfg)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	return a
}

// Execute runs the command line and returns the process exit code:
// 0 ok, 1 error, 2 not found or timed out, 3 login or configuration problem.
func Execute(args []string) int {
	return New().Run(args)
}

// Run parses args and runs the matching command.
func (a *App) Run(args []string) int {
	cmd := a.root()
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(a.Stderr, "gofer-cli: %v\n", err)
		return exitCode(err)
	}
	return exitOK
}

func exitCode(err error) int {
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, model.ErrNotFound):
		return exitNotFound
	case errors.Is(err, model.ErrAuth), errors.Is(err, model.ErrConfig):
		return exitAuth
	default:
		return exitError
	}
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// connect returns the API, loading the configuration and building the client
// on first use.
func (a *App) connect() (API, error) {
	if a.API != nil {
		return a.API, nil
	}
	cfg, err := a.loadConfig()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", model.ErrConfig, err)
	}
	c, err := a.newClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", model.ErrConfig, err)
	}
	a.API = c
	return c, nil
}

func (a *App) root() *cobra.Command {
	root := &cobra.Command{
		Use:               "gofer-cli",
		Short:             "Command-line client for Gofer webmail",
		SilenceErrors:     true,
		SilenceUsage:      true,
		Version:           a.Version,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)

	p := root.PersistentFlags()
	p.StringVar(&a.flags.url, "url", "", "gofer server URL")
	p.StringVar(&a.flags.user, "user", "", "account user")
	p.StringVar(&a.flags.configFile, "config", "", "config file")
	p.BoolVar(&a.flags.json, "json", false, "write JSON")

	root.AddCommand(
		a.listCommand(),
		a.readCommand(),
		a.latestCommand(),
		a.otpCommand(),
		a.linkCommand(),
		a.waitCommand(),
		a.attachmentsCommand(),
		a.saveCommand(),
		a.markCommand(),
		a.actionCommand("archive <id>... | -", "Move messages out of the inbox", "  gofer-cli archive 42 43", client.ActArchive),
		a.actionCommand("spam <id>... | -", "Mark messages as spam", "  gofer-cli spam 42", client.ActSpam),
		a.actionCommand("delete <id>... | -", "Move messages to the trash", "  gofer-cli delete 42", client.ActDelete),
		a.moveCommand(),
		a.sendCommand(),
		a.replyCommand(),
		a.accountsCommand(),
		a.foldersCommand(),
		a.syncCommand(),
		a.loginCommand(),
		a.logoutCommand(),
		a.doctorCommand(),
		a.versionCommand(),
	)
	return root
}

func (a *App) writeJSON(v any) error {
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// done writes the trailing diagnostics of a command that changes something.
func (a *App) done(note string) error {
	if a.flags.json {
		if err := a.writeJSON(struct {
			OK bool `json:"ok"`
		}{true}); err != nil {
			return err
		}
	}
	if note != "" {
		_, _ = fmt.Fprintln(a.Stderr, note)
	}
	return nil
}
