# gofer-cli

[![CI](https://github.com/noir017/gofer-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/noir017/gofer-cli/actions/workflows/ci.yml)

An unofficial command-line client for [Gofer](https://github.com/cristianadrielbraun/gofer), the self-hosted webmail. It talks to a Gofer server the way the web UI does and is built for agents and scripts: every input comes from a flag, an environment variable, a config file or stdin, results go to stdout, diagnostics go to stderr, and it never prompts.

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
| `--config` | `GOFER_CONFIG` | | Config file, default `gofer-cli/config.toml` under the system config directory (on Linux `~/.config` or `$XDG_CONFIG_HOME`) |
| | `GOFER_CACHE_DIR` | | Session cookie directory, default `gofer-cli` under the system cache directory (on Linux `~/.cache` or `$XDG_CACHE_HOME`) |

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

`password_cmd` runs with `sh -c`. Windows has no `sh`; there use `password`, or supply a `sh` of your own.

## Commands

Global flags: `--url`, `--user`, `--config`, `--json`. Every command accepts `--json`, `doctor` included, and prints results as JSON instead of text. `--raw` or `--html` together with `--json` is an error.

| Command | What it does |
| --- | --- |
| `list [-q query] [--to A] [--from A] [--subject S] [--account EMAIL] [--folder F] [--unread] [--has-attachment] [--limit 20]` | Search or list messages |
| `read <id> [--raw] [--html]` | Print one message as text (default), raw source, or HTML |
| `latest [filters] [--raw] [--html]` | Newest message matching the filters |
| `otp [filters] [--since 10m] [--wait seconds] [--pattern regexp]` | Print only the verification code |
| `link [filters] [--since 10m] [--wait seconds] [--match regexp]` | Print only the link |
| `wait [filters] [--wait 120]` | Wait for a new message, then print it in `read`'s text or JSON form |
| `attachments <id>` | List attachments |
| `save <id> [--index N] [-o dir]` | Download attachments and print the paths written |
| `mark read\|unread\|star\|unstar <id>... \| -` | Change message flags; `-` reads ids from stdin |
| `archive \| spam \| delete <id>... \| -` | Move messages to archive, spam or Trash |
| `move <id>... --folder name-or-id [--account EMAIL]` | Move messages to a folder |
| `send --to A[,A] [--from account] [--subject S] [--cc A[,A]] [--bcc A[,A]] [--body text \| --body-file file \| stdin] [--attach file]...` | Send a message; only `--to` is required |
| `reply <id> [--all] [--body text \| --body-file file \| stdin] [--attach file]...` | Reply; without `--from`, from the account the original message arrived on |
| `accounts` | List accounts |
| `folders [--account EMAIL]` | List folders |
| `sync [--account EMAIL]` | Ask the server to fetch new mail |
| `login` / `logout` | Create or drop the saved session |
| `doctor` | Check configuration and connectivity |
| `version` | Print the version |

`list`, `latest`, `otp`, `link` and `wait` share the same filters. `--to`, `--from` and `--subject` are independent filters. `--folder` takes a folder name or id and searches only that folder; a name that exists in several accounts searches all of them, and `--account` narrows it. `-q` takes the same syntax as Gofer's search box: `from:`, `to:`, `subject:`, `body:`, `after:`, `before:`, `is:unread`, `attachment:`, `filetype:` and `has:attachment`. `filetype:` takes a preset (`pdf`, `image`, `document`, `spreadsheet`, `presentation`, `archive`, `audio`, `video`) or a file extension. Keywords this client cannot translate, such as `tag:` or `larger:`, are rejected with an error instead of being searched as plain text.

`delete` moves messages to Trash; it does not delete them permanently.

`otp`, `link` and `wait` look for mail that arrived: a single message in Sent, Drafts or Trash is skipped. A conversation row is kept even when its newest message is a sent copy.

`list` leaves Trash out unless `--folder` names it. Its text output is one message per line: id, account, folder, date, from, to, subject. `read` adds a `Folder:` line, and `--json` carries the folder name as `folder`.

`mark`, `archive`, `spam`, `delete` and `move` report every message they changed as `id<TAB>result`, where the result is `read`, `unread`, `starred`, `unstarred`, `archive`, `spam`, `trash` or the destination folder's name. With `--json` the output is `[{"id":"42","result":"trash"}]`.

`otp`, `link` and `wait` accept `--wait` and its hidden alias `--timeout`. When a wait runs out they say `timed out after 5s; ...`; when no wait was asked for they say `no match since 11:50 (last 10m); widen with --since 24h`. Both exit 2.

The client retries a GET, or a sign-in, once after a second when the network drops the connection (reset, EOF, TLS handshake timeout, i/o timeout, connection refused). Other POSTs are never retried, so a write cannot run twice.

If `list --to` finds nothing, stderr points out that `--to` matches the To header and that `--account` picks a mailbox.

`move --folder` with a name no folder has lists the account's folders, with names and ids, in the error.

## Use from an agent

Read the verification code from a fresh email, waiting up to a minute for it:

```
gofer-cli otp --from no-reply@example.com --subject "Sign in" --since 5m --wait 60
```

`--pattern` filters the codes the client extracts: only a matching code is printed. When no extracted code matches, the pattern is searched in the message text and the whole match is the code. A pattern with a capture group is searched in the text first, and the group is the code.

Read the link from a fresh email:

```
gofer-cli link --from no-reply@example.com --wait 60 --match 'https://example.com/reset'
```

Wait for a message and get the whole thing as JSON:

```
gofer-cli wait --from alerts@example.com --wait 120 --json
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
| 2 | Nothing matched or the wait timed out (`latest`, `otp`, `link`, `wait`); no such id (`read`, `attachments`, `save`, `mark`, `archive`, `spam`, `delete`, `move`); no such `--account` or `--folder` target |
| 3 | Login or configuration problem |

`list`, `accounts` and `folders` exit 0 when the result is empty.

## Compatibility

Tested against Gofer v0.23.0. Gofer is pre-1.0 and changes between releases; other versions may work, but run `gofer-cli doctor` after upgrading the server.

## Known limitations

With conversation grouping enabled in Gofer, a thread is listed as its newest message only. Such rows carry `"thread": true` in JSON output.

- `wait` can take the conversation your just-sent reply belongs to as a new message.
- A verification code in an earlier message of a conversation cannot be read: only the newest message is listed.

## How it works

Gofer has no public API. This tool calls the endpoints the Gofer web UI itself uses: the JSON search endpoint for message ids, the raw-message endpoint for full mail, and the same form posts as the UI for writes, marked with an `X-Gofer-Request: 1` header so Gofer accepts them from a non-browser client. A Gofer upgrade can change any of these, in which case gofer-cli needs a matching change.

## License

MIT. See [LICENSE](LICENSE).
