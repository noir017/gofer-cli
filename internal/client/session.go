package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// sessionFile is the cached gofer_session cookie. Several gofer-cli processes
// share it, so writes go to a temporary file that is renamed into place: a
// reader sees either the old file or the new one, never a partial write.
type sessionFile struct {
	URL     string    `json:"url"`
	User    string    `json:"user"`
	Token   string    `json:"token"`
	SavedAt time.Time `json:"saved_at"`
}

// sessionPath names the cache file after the server and the user, so one
// machine can talk to several gofer instances without mixing sessions up.
func sessionPath(dir, rawURL, user string) string {
	sum := sha256.Sum256([]byte(rawURL + "\x00" + user))
	return filepath.Join(dir, "session-"+hex.EncodeToString(sum[:16])+".json")
}

func loadSession(dir, rawURL, user string) (sessionFile, error) {
	var saved sessionFile
	data, err := os.ReadFile(sessionPath(dir, rawURL, user))
	if err != nil {
		return saved, err
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		return saved, err
	}
	if saved.URL != rawURL || saved.User != user || saved.Token == "" {
		return saved, fmt.Errorf("cached session does not belong to %s", rawURL)
	}
	return saved, nil
}

func saveSession(dir string, saved sessionFile) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// MkdirAll leaves an existing directory's mode alone.
	_ = os.Chmod(dir, 0o700)

	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, sessionPath(dir, saved.URL, saved.User))
}

func removeSession(dir, rawURL, user string) error {
	if dir == "" {
		return nil
	}
	if err := os.Remove(sessionPath(dir, rawURL, user)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
