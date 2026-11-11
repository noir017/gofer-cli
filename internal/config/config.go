// Package config reads gofer-cli settings from flags, the environment and the
// config file, in that order of precedence.
package config

import "errors"

// Config is everything the client needs. Nothing here has a default that
// points at a real server or account.
type Config struct {
	URL         string // GOFER_URL, e.g. https://gofer.example.com
	User        string // GOFER_USER
	Password    string // GOFER_PASSWORD
	PasswordCmd string // GOFER_PASSWORD_CMD: run with sh -c, stdout (trimmed) is the password
	TOTPSecret  string // GOFER_TOTP_SECRET: base32, for accounts with 2FA
	ConfigFile  string // GOFER_CONFIG, default $XDG_CONFIG_HOME/gofer-cli/config.toml
	CacheDir    string // GOFER_CACHE_DIR, default $XDG_CACHE_HOME/gofer-cli (session cookie, 0600)
}

// Load fills a Config from the environment and the config file. Fields already
// set in base (from flags) win.
func Load(base Config) (Config, error) {
	return base, errors.New("config.Load: not implemented")
}

// ResolvePassword returns Password, or runs PasswordCmd.
func (c Config) ResolvePassword() (string, error) {
	return "", errors.New("config.ResolvePassword: not implemented")
}
