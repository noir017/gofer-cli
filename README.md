# gofer-cli

[![CI](https://github.com/noir017/gofer-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/noir017/gofer-cli/actions/workflows/ci.yml)

An unofficial command-line client for [Gofer](https://github.com/cristianadrielbraun/gofer), the self-hosted webmail. It talks to a Gofer server the way the web UI does and is built for agents and scripts: every input comes from a flag, an environment variable or a config file, results go to stdout, diagnostics go to stderr, and it never prompts.

Not affiliated with the Gofer project.

## Install

```
go install github.com/noir017/gofer-cli/cmd/gofer-cli@latest
```

Or download a binary for linux, darwin or windows (amd64, arm64) from [Releases](https://github.com/noir017/gofer-cli/releases) and put it on your `PATH`.

## Quick start

```
export GOFER_URL=https://gofer.example.com
export GOFER_USER=me@example.com
export GOFER_PASSWORD_CMD='pass show mail/gofer'

gofer-cli login
gofer-cli list --unread --limit 5
gofer-cli read 1234
```

`login` saves the session cookie in the cache directory with mode 0600. Later commands reuse it and sign in again when it expires.

## Configuration

Settings are read from flags first, then the environment, then the config file.

| Flag | Environment | Config key | Meaning |
| --- | --- | --- | --- |
| `--url` | `GOFER_URL` | `url` | Server base URL, e.g. `https://gofer.example.com` |
| `--user` | `GOFER_USER` | `user` | Login name (email address) |
| | `GOFER_PASSWORD` | `password` | Password |
| | `GOFER_PASSWORD_CMD` | `password_cmd` | Command whose stdout is the password, run with `sh -c` |
| | `GOFER_TOTP_SECRET` | `totp_secret` | Base32 TOTP secret for accounts with 2FA |
| `--config` | `GOFER_CONFIG` | | Config file, default `$XDG_CONFIG_HOME/gofer-cli/config.toml` |
| | `GOFER_CACHE_DIR` | | Session cookie directory, default `$XDG_CACHE_HOME/gofer-cli` |

The config file is TOML:

```toml
url = "https://gofer.example.com"
user = "me@example.com"
password_cmd = "pass show mail/gofer"
totp_secret = "JBSWY3DPEHPK3PXP"
```

`password_cmd` keeps the password out of the file. Any command works:

```toml
password_cmd = "pass show mail/gofer"                 # pass
password_cmd = "op read op://private/gofer/password"  # 1Password CLI
```

Prefer `password_cmd` over `password`: the command runs only when a password is needed, and its output never appears in the config file.

## Commands

Global flags: `--url`, `--user`, `--config`, `--json`. With `--json`, results are printed as JSON instead of text.

| Command | What it does |
| --- | --- |
| `list [-q query] [--to A] [--from A] [--subject S] [--account EMAIL] [--unread] [--limit 20]` | Search or list messages |
| `read <id> [--raw] [--html]` | Print one message as text (default), raw source, or HTML |
| `latest [filters]` | Newest message matching the filters |
| `otp [filters] [--since 10m] [--wait seconds] [--pattern regexp]` | Print only the verification code |
| `link [filters] [--since 10m] [--wait seconds] [--match regexp]` | Print only the link |
| `wait [filters] [--timeout 120]` | Wait for a new message, then print it like `read` |
| `attachments <id>` | List attachments |
| `save <id> [--index N] [-o dir]` | Download attachments and print the paths written |
| `mark read\|unread\|star\|unstar <id>... \| -` | Change message flags; `-` reads ids from stdin |
| `archive \| spam \| delete <id>... \| -` | Move messages to archive, spam or Trash |
| `move <id>... --folder name-or-id` | Move messages to a folder |
| `send --from account --to A[,A] [--cc] [--bcc] --subject S [--body text \| --body-file file \| stdin] [--attach file]...` | Send a message |
| `reply <id> [--all] [--body text \| --body-file file \| stdin] [--attach file]...` | Reply to a message |
| `accounts` | List accounts |
| `folders [--account EMAIL]` | List folders |
| `sync [--account EMAIL]` | Ask the server to fetch new mail |
| `login` / `logout` | Create or drop the saved session |
| `doctor` | Check configuration and connectivity |
| `version` | Print the version |

`list`, `latest`, `otp`, `link` and `wait` share the same filters: `-q` takes Gofer's search syntax, and `--to`, `--from`, `--subject`, `--account` and `--unread` narrow it further.

`delete` moves messages to Trash; it does not delete them permanently.

## Use from an agent

Read the verification code from a fresh email, waiting up to a minute for it:

```
gofer-cli otp --from no-reply@example.com --subject "Sign in" --since 5m --wait 60
```

`--pattern` narrows what counts as a code when the message has several numbers.

Read the link from a fresh email:

```
gofer-cli link --from no-reply@example.com --wait 60 --match 'https://example.com/reset'
```

Wait for a message and get the whole thing as JSON:

```
gofer-cli wait --from alerts@example.com --timeout 120 --json
```

Mark everything that is unread as read:

```
gofer-cli list --unread --limit 100 | gofer-cli mark read -
```

`list` writes message ids to stdout in the form `mark` accepts on stdin.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | Error |
| 2 | Nothing found, or `--wait` / `--timeout` expired |
| 3 | Login or configuration problem |

## Compatibility

Tested against Gofer v0.23.0. Gofer is pre-1.0 and changes between releases; other versions may work, but run `gofer-cli doctor` after upgrading the server.

## How it works

Gofer has no public API. This tool calls the endpoints the Gofer web UI itself uses: the JSON search endpoint for message ids, the raw-message endpoint for full mail, and the same form posts as the UI for writes, marked with an `X-Gofer-Request: 1` header so Gofer accepts them from a non-browser client. A Gofer upgrade can change any of these, in which case gofer-cli needs a matching change.

## License

MIT. See [LICENSE](LICENSE).
