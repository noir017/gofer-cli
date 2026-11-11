package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/noir017/gofer-cli/internal/model"
)

func (a *App) loginCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "login",
		Short:   "Log in and keep the session",
		Example: "  gofer-cli login",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, err := a.connect()
			if err != nil {
				return err
			}
			if err := api.Login(cmd.Context()); err != nil {
				return err
			}
			return a.done("logged in")
		},
	}
}

func (a *App) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "logout",
		Short:   "Log out and drop the session",
		Example: "  gofer-cli logout",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			api, err := a.connect()
			if err != nil {
				return err
			}
			if err := api.Logout(cmd.Context()); err != nil {
				return err
			}
			return a.done("logged out")
		},
	}
}

func (a *App) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Check the configuration and the connection",
		Example: "  gofer-cli doctor",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			failures := 0
			var kind error
			ok := func(name, note string) {
				fmt.Fprintf(a.Stdout, "%-8s ok    %s\n", name, strings.TrimSpace(note))
			}
			fail := func(k error, name, format string, args ...any) {
				failures++
				if kind == nil {
					kind = k
				}
				fmt.Fprintf(a.Stdout, "%-8s FAIL  %s\n", name, fmt.Sprintf(format, args...))
			}

			cfg, err := a.loadConfig()
			configOK := true
			switch {
			case err != nil:
				fail(model.ErrConfig, "config", "%v", err)
				configOK = false
			case cfg.URL == "":
				fail(model.ErrConfig, "config", "url is not set")
				configOK = false
			case cfg.User == "":
				fail(model.ErrConfig, "config", "user is not set")
				configOK = false
			case cfg.Password == "" && cfg.PasswordCmd == "":
				fail(model.ErrConfig, "config", "password is not set")
				configOK = false
			default:
				ok("config", cfg.URL)
			}

			if configOK || a.API != nil {
				api, cerr := a.connect()
				var info model.ServerInfo
				if cerr == nil {
					info, cerr = api.Info(ctx)
				}
				switch {
				case cerr != nil && api == nil:
					fail(model.ErrConfig, "server", "%v", cerr)
				case cerr != nil:
					fail(model.ErrAuth, "server", "%v", cerr)
				default:
					ok("server", info.URL+" "+info.Version)
					if err := api.Login(ctx); err != nil {
						fail(model.ErrAuth, "login", "%v", err)
					} else {
						ok("login", info.User)
					}
				}
			}

			ok("version", a.Version)
			if failures > 0 {
				return fmt.Errorf("%w: %d check(s) failed", kind, failures)
			}
			return nil
		},
	}
}

func (a *App) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the version",
		Example: "  gofer-cli version",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.flags.json {
				return a.writeJSON(struct {
					Version string `json:"version"`
				}{a.Version})
			}
			fmt.Fprintln(a.Stdout, a.Version)
			return nil
		},
	}
}
