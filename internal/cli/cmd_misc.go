package cli

import (
	"fmt"
	"net/url"
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

type checkResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (a *App) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		Short:   "Check the configuration and the connection",
		Example: "  gofer-cli doctor",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			checks := make([]checkResult, 0, 4)
			var kind error
			pass := func(name, detail string) {
				checks = append(checks, checkResult{Name: name, OK: true, Detail: detail})
			}
			fail := func(name string, err error) {
				checks = append(checks, checkResult{Name: name, OK: false, Detail: err.Error()})
				if kind == nil {
					kind = err
				}
			}

			cfg, err := a.loadConfig()
			configOK := true
			switch {
			case err != nil:
				fail("config", fmt.Errorf("%w: %w", model.ErrConfig, err))
				configOK = false
			case cfg.URL == "":
				fail("config", fmt.Errorf("%w: url is not set", model.ErrConfig))
				configOK = false
			case cfg.User == "":
				fail("config", fmt.Errorf("%w: user is not set", model.ErrConfig))
				configOK = false
			case cfg.Password == "" && cfg.PasswordCmd == "":
				fail("config", fmt.Errorf("%w: password is not set", model.ErrConfig))
				configOK = false
			default:
				pass("config", displayURL(cfg.URL))
			}

			if configOK || a.API != nil {
				api, cerr := a.connect()
				if cerr != nil {
					fail("server", cerr)
				} else if info, ierr := api.Info(ctx); ierr != nil {
					fail("server", ierr)
				} else {
					pass("server", strings.TrimSpace(displayURL(info.URL)+" "+info.Version))
					if lerr := api.Login(ctx); lerr != nil {
						fail("login", lerr)
					} else {
						pass("login", info.User)
					}
				}
			}

			pass("version", a.Version)

			if a.flags.json {
				if err := a.writeJSON(struct {
					Checks []checkResult `json:"checks"`
				}{checks}); err != nil {
					return err
				}
			} else {
				for _, c := range checks {
					status := "ok"
					if !c.OK {
						status = "FAIL"
					}
					fmt.Fprintf(a.Stdout, "%-8s %-5s %s\n", c.Name, status, c.Detail)
				}
			}
			if kind != nil {
				failed := 0
				for _, c := range checks {
					if !c.OK {
						failed++
					}
				}
				return fmt.Errorf("doctor: %d check(s) failed: %w", failed, kind)
			}
			return nil
		},
	}
}

// displayURL hides any userinfo before a URL reaches the terminal.
func displayURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Host != "" {
		u.User = nil
		return u.String()
	}
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		return raw[i+1:]
	}
	return raw
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
