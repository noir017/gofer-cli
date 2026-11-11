package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/config"
	"github.com/noir017/gofer-cli/internal/model"
)

type fakeAct struct {
	action client.Action
	ids    []string
}

type fakeMove struct {
	ids    []string
	folder string
}

// fakeAPI stands in for *client.Client.
type fakeAPI struct {
	refs     []model.MessageRef
	sources  map[string][]byte
	accounts []model.Account
	folders  []model.Folder
	info     model.ServerInfo

	searchErr error
	sourceErr error
	infoErr   error
	loginErr  error
	sendErr   error

	searches []client.SearchOptions
	syncs    []string
	acts     []fakeAct
	moves    []fakeMove
	sends    []client.SendRequest
	logins   int
}

func (f *fakeAPI) Login(context.Context) error  { f.logins++; return f.loginErr }
func (f *fakeAPI) Logout(context.Context) error { return nil }

func (f *fakeAPI) Info(context.Context) (model.ServerInfo, error) { return f.info, f.infoErr }

func (f *fakeAPI) Search(_ context.Context, opts client.SearchOptions) ([]model.MessageRef, error) {
	f.searches = append(f.searches, opts)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.refs, nil
}

func (f *fakeAPI) Source(_ context.Context, id string) ([]byte, error) {
	if f.sourceErr != nil {
		return nil, f.sourceErr
	}
	raw, ok := f.sources[id]
	if !ok {
		return nil, fmt.Errorf("%w: message %s", model.ErrNotFound, id)
	}
	return raw, nil
}

func (f *fakeAPI) Act(_ context.Context, action client.Action, ids []string) error {
	f.acts = append(f.acts, fakeAct{action: action, ids: ids})
	return nil
}

func (f *fakeAPI) Move(_ context.Context, ids []string, folderID string) error {
	f.moves = append(f.moves, fakeMove{ids: ids, folder: folderID})
	return nil
}

func (f *fakeAPI) Sync(_ context.Context, accountID string) error {
	f.syncs = append(f.syncs, accountID)
	return nil
}

func (f *fakeAPI) Accounts(context.Context) ([]model.Account, error) { return f.accounts, nil }

func (f *fakeAPI) Folders(context.Context, string) ([]model.Folder, error) { return f.folders, nil }

func (f *fakeAPI) Send(_ context.Context, req client.SendRequest) error {
	f.sends = append(f.sends, req)
	return f.sendErr
}

type fakeClock struct {
	t      time.Time
	sleeps int
}

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) Sleep(d time.Duration) {
	c.sleeps++
	c.t = c.t.Add(d)
}

type env struct {
	app    *App
	api    API
	out    *bytes.Buffer
	errOut *bytes.Buffer
	clock  *fakeClock
}

func newEnv(t *testing.T, api API) *env {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}
	e := &env{api: api, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, clock: clock}
	e.app = New()
	e.app.API = api
	e.app.Stdout = e.out
	e.app.Stderr = e.errOut
	e.app.Stdin = strings.NewReader("")
	e.app.StdinIsTerminal = func() bool { return true }
	e.app.Now = clock.Now
	e.app.Sleep = clock.Sleep
	e.app.Version = "test"
	e.app.Parse = func([]byte) (*model.Message, error) {
		return nil, errors.New("test did not set Parse")
	}
	e.app.AttachmentData = func([]byte, int) (model.Attachment, []byte, error) {
		return model.Attachment{}, nil, errors.New("test did not set AttachmentData")
	}
	e.app.OTP = func(*model.Message, string) (string, bool) { return "", false }
	e.app.Link = func(*model.Message, string) (string, bool) { return "", false }
	e.app.loadConfig = func() (config.Config, error) {
		return config.Config{}, errors.New("test did not set loadConfig")
	}
	return e
}

func (e *env) run(args ...string) int { return e.app.Run(args) }

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"ok", nil, 0},
		{"not found", fmt.Errorf("read: %w", model.ErrNotFound), 2},
		{"auth", fmt.Errorf("login: %w", model.ErrAuth), 3},
		{"config", fmt.Errorf("config: %w", model.ErrConfig), 3},
		{"other", errors.New("boom"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Fatalf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestNotFoundExitsTwo(t *testing.T) {
	api := &fakeAPI{searchErr: fmt.Errorf("search: %w", model.ErrNotFound)}
	e := newEnv(t, api)
	if code := e.run("list"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if got := e.errOut.String(); !strings.HasPrefix(got, "gofer-cli: ") {
		t.Fatalf("stderr = %q, want the gofer-cli prefix", got)
	}
	if e.out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", e.out.String())
	}
}

func TestConfigErrorExitsThree(t *testing.T) {
	e := newEnv(t, nil)
	if code := e.run("list"); code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
}

func TestUnknownCommandExitsOne(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("bogus"); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.HasPrefix(e.errOut.String(), "gofer-cli: ") {
		t.Fatalf("stderr = %q", e.errOut.String())
	}
}

func TestVersion(t *testing.T) {
	e := newEnv(t, &fakeAPI{})
	if code := e.run("version"); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := e.out.String(); got != "test\n" {
		t.Fatalf("stdout = %q, want %q", got, "test\n")
	}
}
