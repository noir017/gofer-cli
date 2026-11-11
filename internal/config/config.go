// Package config reads gofer-cli settings from flags, the environment and the
// config file, in that order of precedence.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/noir017/gofer-cli/internal/model"
)

// Environment variables read by Load.
const (
	EnvURL         = "GOFER_URL"
	EnvUser        = "GOFER_USER"
	EnvPassword    = "GOFER_PASSWORD"
	EnvPasswordCmd = "GOFER_PASSWORD_CMD"
	EnvTOTPSecret  = "GOFER_TOTP_SECRET"
	EnvConfigFile  = "GOFER_CONFIG"
	EnvCacheDir    = "GOFER_CACHE_DIR"
)

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
	cfg := base
	if cfg.URL == "" {
		cfg.URL = os.Getenv(EnvURL)
	}
	if cfg.User == "" {
		cfg.User = os.Getenv(EnvUser)
	}
	if cfg.Password == "" {
		cfg.Password = os.Getenv(EnvPassword)
	}
	if cfg.PasswordCmd == "" {
		cfg.PasswordCmd = os.Getenv(EnvPasswordCmd)
	}
	if cfg.TOTPSecret == "" {
		cfg.TOTPSecret = os.Getenv(EnvTOTPSecret)
	}
	if cfg.ConfigFile == "" {
		cfg.ConfigFile = os.Getenv(EnvConfigFile)
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = os.Getenv(EnvCacheDir)
	}

	// A config file named on the command line or in the environment must
	// exist; the default one is optional.
	explicitFile := cfg.ConfigFile != ""
	if !explicitFile {
		cfg.ConfigFile = defaultConfigFile()
	}
	if cfg.ConfigFile != "" {
		values, err := readConfigFile(cfg.ConfigFile, explicitFile)
		if err != nil {
			return Config{}, err
		}
		if cfg.URL == "" {
			cfg.URL = values["url"]
		}
		if cfg.User == "" {
			cfg.User = values["user"]
		}
		if cfg.Password == "" {
			cfg.Password = values["password"]
		}
		if cfg.PasswordCmd == "" {
			cfg.PasswordCmd = values["password_cmd"]
		}
		if cfg.TOTPSecret == "" {
			cfg.TOTPSecret = values["totp_secret"]
		}
	}

	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.User = strings.TrimSpace(cfg.User)
	cfg.PasswordCmd = strings.TrimSpace(cfg.PasswordCmd)
	cfg.TOTPSecret = strings.TrimSpace(cfg.TOTPSecret)
	if cfg.CacheDir == "" {
		cfg.CacheDir = defaultCacheDir()
	}

	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ResolvePassword returns Password, or runs PasswordCmd.
func (c Config) ResolvePassword() (string, error) {
	if c.Password != "" {
		return c.Password, nil
	}
	if c.PasswordCmd == "" {
		return "", fmt.Errorf("%w: set %s or %s", model.ErrConfig, EnvPassword, EnvPasswordCmd)
	}
	cmd := exec.Command("sh", "-c", c.PasswordCmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%w: %s failed: %s", model.ErrConfig, EnvPasswordCmd, msg)
	}
	password := strings.TrimSpace(stdout.String())
	if password == "" {
		return "", fmt.Errorf("%w: %s printed nothing", model.ErrConfig, EnvPasswordCmd)
	}
	return password, nil
}

func validate(cfg Config) error {
	if cfg.URL == "" {
		return fmt.Errorf("%w: set %s", model.ErrConfig, EnvURL)
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%w: %s must be an http(s) URL", model.ErrConfig, EnvURL)
	}
	if cfg.User == "" {
		return fmt.Errorf("%w: set %s", model.ErrConfig, EnvUser)
	}
	if cfg.Password == "" && cfg.PasswordCmd == "" {
		return fmt.Errorf("%w: set %s or %s", model.ErrConfig, EnvPassword, EnvPasswordCmd)
	}
	return nil
}

func readConfigFile(path string, required bool) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !required {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: read %s: %v", model.ErrConfig, path, err)
	}
	values, err := parseTOML(string(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", model.ErrConfig, path, err)
	}
	return values, nil
}

func defaultConfigFile() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gofer-cli", "config.toml")
}

func defaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gofer-cli")
}
