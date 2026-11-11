package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noir017/gofer-cli/internal/model"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		EnvURL, EnvUser, EnvPassword, EnvPasswordCmd, EnvTOTPSecret,
		EnvConfigFile, EnvCacheDir, "XDG_CONFIG_HOME", "XDG_CACHE_HOME",
	} {
		t.Setenv(name, "")
	}
}

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "gofer-cli", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadPrecedence(t *testing.T) {
	clearEnv(t)
	configHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	path := writeConfig(t, configHome, `
url = "https://file.example.com"
user = "file-user"
password = "file-pass"
totp_secret = "GEZDGNBVGY3TQOJQ"
`)
	t.Setenv(EnvURL, "https://env.example.com")
	t.Setenv(EnvUser, "env-user")

	cfg, err := Load(Config{User: "flag-user"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.URL != "https://env.example.com" {
		t.Errorf("URL = %q, want the environment value", cfg.URL)
	}
	if cfg.User != "flag-user" {
		t.Errorf("User = %q, want the flag value", cfg.User)
	}
	if cfg.Password != "file-pass" {
		t.Errorf("Password = %q, want the file value", cfg.Password)
	}
	if cfg.TOTPSecret != "GEZDGNBVGY3TQOJQ" {
		t.Errorf("TOTPSecret = %q, want the file value", cfg.TOTPSecret)
	}
	if cfg.ConfigFile != path {
		t.Errorf("ConfigFile = %q, want %q", cfg.ConfigFile, path)
	}
	if want := filepath.Join(cacheHome, "gofer-cli"); cfg.CacheDir != want {
		t.Errorf("CacheDir = %q, want %q", cfg.CacheDir, want)
	}
}

func TestLoadDefaultConfigFileIsOptional(t *testing.T) {
	clearEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(EnvURL, "https://gofer.example.com")
	t.Setenv(EnvUser, "agent@example.com")
	t.Setenv(EnvPassword, "secret")

	cfg, err := Load(Config{})
	if err != nil {
		t.Fatalf("Load without a config file: %v", err)
	}
	if cfg.Password != "secret" {
		t.Errorf("Password = %q", cfg.Password)
	}
}

func TestLoadExplicitConfigFileMustExist(t *testing.T) {
	clearEnv(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(EnvConfigFile, filepath.Join(t.TempDir(), "missing.toml"))

	_, err := Load(Config{})
	if !errors.Is(err, model.ErrConfig) {
		t.Fatalf("Load error = %v, want ErrConfig", err)
	}
}

func TestLoadMalformedConfigFile(t *testing.T) {
	clearEnv(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := writeConfig(t, t.TempDir(), "url = \"https://example.com\n")
	t.Setenv(EnvConfigFile, path)

	_, err := Load(Config{})
	if !errors.Is(err, model.ErrConfig) {
		t.Fatalf("Load error = %v, want ErrConfig", err)
	}
}

func TestLoadMissingSettings(t *testing.T) {
	clearEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"url", map[string]string{EnvUser: "u", EnvPassword: "p"}, EnvURL},
		{"user", map[string]string{EnvURL: "https://gofer.example.com", EnvPassword: "p"}, EnvUser},
		{"password", map[string]string{EnvURL: "https://gofer.example.com", EnvUser: "u"}, EnvPassword},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			_, err := Load(Config{})
			if !errors.Is(err, model.ErrConfig) {
				t.Fatalf("Load error = %v, want ErrConfig", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s", err, tc.want)
			}
		})
	}
}

func TestLoadRejectsBadURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(EnvURL, "gofer.example.com")
	t.Setenv(EnvUser, "u")
	t.Setenv(EnvPassword, "p")

	if _, err := Load(Config{}); !errors.Is(err, model.ErrConfig) {
		t.Fatalf("Load error = %v, want ErrConfig", err)
	}
}

func TestLoadPasswordFromCommand(t *testing.T) {
	clearEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	path := writeConfig(t, t.TempDir(), `
url = "https://gofer.example.com"
user = "u"
password_cmd = "printf 'secret\\n'"
`)
	t.Setenv(EnvConfigFile, path)

	cfg, err := Load(Config{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	password, err := cfg.ResolvePassword()
	if err != nil {
		t.Fatalf("ResolvePassword: %v", err)
	}
	if password != "secret" {
		t.Errorf("password = %q, want secret", password)
	}
}

func TestResolvePassword(t *testing.T) {
	password, err := Config{Password: "direct", PasswordCmd: "exit 1"}.ResolvePassword()
	if err != nil || password != "direct" {
		t.Fatalf("ResolvePassword = %q, %v; want the direct password", password, err)
	}

	password, err = Config{PasswordCmd: "printf '  spaced \\n' "}.ResolvePassword()
	if err != nil || password != "spaced" {
		t.Fatalf("ResolvePassword = %q, %v; want the trimmed command output", password, err)
	}

	if _, err := (Config{PasswordCmd: "echo boom >&2; exit 2"}).ResolvePassword(); !errors.Is(err, model.ErrConfig) {
		t.Fatalf("failing command error = %v, want ErrConfig", err)
	} else if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not carry the command's stderr", err)
	}

	if _, err := (Config{PasswordCmd: "true"}).ResolvePassword(); !errors.Is(err, model.ErrConfig) {
		t.Fatalf("empty output error = %v, want ErrConfig", err)
	}

	if _, err := (Config{}).ResolvePassword(); !errors.Is(err, model.ErrConfig) {
		t.Fatalf("missing password error = %v, want ErrConfig", err)
	}
}

func TestParseTOML(t *testing.T) {
	values, err := parseTOML(`
# leading comment
URL = "https://gofer.example.com"   # trailing comment
user = 'agent@example.com'
password = "a#b\"c\\d\te"
password_cmd = "printf '%s' secret"
totp_secret = "GEZDGNBVGY3TQOJQ"
`)
	if err != nil {
		t.Fatalf("parseTOML: %v", err)
	}
	want := map[string]string{
		"url":          "https://gofer.example.com",
		"user":         "agent@example.com",
		"password":     "a#b\"c\\d\te",
		"password_cmd": "printf '%s' secret",
		"totp_secret":  "GEZDGNBVGY3TQOJQ",
	}
	for key, value := range want {
		if values[key] != value {
			t.Errorf("%s = %q, want %q", key, values[key], value)
		}
	}
}

func TestParseTOMLErrors(t *testing.T) {
	for _, text := range []string{
		"[section]\nkey = \"value\"",
		"key \"value\"",
		"key = \"unterminated",
		"key = \"\"\"multi\"\"\"",
		"key = \"bad \\q escape\"",
		"key = bare value",
	} {
		if _, err := parseTOML(text); err == nil {
			t.Errorf("parseTOML(%q) accepted invalid input", text)
		}
	}
}
