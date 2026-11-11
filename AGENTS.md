# gofer-cli

Command-line client for [Gofer](https://github.com/cristianadrielbraun/gofer) webmail, built for agents and scripts.

## Layout

- `cmd/gofer-cli` entry point
- `internal/cli` commands, output, exit codes
- `internal/client` HTTP client for gofer (login, session, search, source, actions, send)
- `internal/config` flags > env > config file
- `internal/message` raw mail → `model.Message`
- `internal/extract` OTP and link extraction
- `internal/model` shared types and error kinds

## Rules

- Gofer has no public API. Use endpoints that return JSON or raw mail; parse HTML only where nothing else exists, in one file, with a test on a saved page.
- Writes send `X-Gofer-Request: 1`.
- No accounts, passwords, tokens or private hostnames in code, tests or docs. Use `example.com`.
- stdout carries results only; diagnostics go to stderr. Exit codes: 0 ok, 1 error, 2 not found / timeout, 3 login or config.
- Never prompt. Every input comes from a flag, an env var or the config file.
- `go test ./...` and `go vet ./...` pass before a commit.
