package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/model"
)

const (
	dateTimeFormat = "2006-01-02 15:04"
	fetchWorkers   = 4
	lookupLimit    = 200
)

// searchMessages runs the search and reads every hit, at most fetchWorkers at
// a time, keeping the order the server returned.
func (a *App) searchMessages(ctx context.Context, f filterFlags) ([]model.Message, error) {
	api, err := a.connect()
	if err != nil {
		return nil, err
	}
	accountID, err := a.resolveAccount(ctx, api, f.account)
	if err != nil {
		return nil, err
	}
	refs, err := api.Search(ctx, f.options(accountID, 0))
	if err != nil {
		return nil, err
	}
	return a.fetchAll(ctx, api, refs)
}

// fetchAll reads every ref and drops the ones that fail, with a line on
// stderr: one unreadable message must not hide the rest of the listing.
func (a *App) fetchAll(ctx context.Context, api API, refs []model.MessageRef) ([]model.Message, error) {
	msgs := make([]model.Message, len(refs))
	errs := make([]error, len(refs))
	workers := fetchWorkers
	if len(refs) < workers {
		workers = len(refs)
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				m, _, err := a.fetchMessage(ctx, api, refs[n])
				if err != nil {
					errs[n] = err
					continue
				}
				msgs[n] = *m
			}
		}()
	}
	for i := range refs {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	out := make([]model.Message, 0, len(refs))
	var firstErr error
	for i := range refs {
		if errs[i] != nil {
			fmt.Fprintf(a.Stderr, "gofer-cli: skipping message %s: %v\n", refs[i].ID, errs[i])
			if firstErr == nil {
				firstErr = errs[i]
			}
			continue
		}
		out = append(out, msgs[i])
	}
	if len(out) == 0 && firstErr != nil {
		// Nothing could be shown: report the failure instead of an empty
		// listing that looks like a search without hits.
		return nil, firstErr
	}
	return out, nil
}

func (a *App) fetchMessage(ctx context.Context, api API, ref model.MessageRef) (*model.Message, []byte, error) {
	raw, err := api.Source(ctx, ref.ID)
	if err != nil {
		return nil, nil, err
	}
	m, err := a.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("parse message %s: %w", ref.ID, err)
	}
	m.ID = ref.ID
	m.Thread = ref.Thread
	if m.Account == "" {
		m.Account = ref.Account
	}
	if m.FolderID == "" {
		m.FolderID = ref.FolderID
	}
	return m, raw, nil
}

func (a *App) resolveAccount(ctx context.Context, api API, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	accounts, err := api.Accounts(ctx)
	if err != nil {
		return "", err
	}
	for _, acc := range accounts {
		if acc.ID == ref || strings.EqualFold(acc.Email, ref) {
			return acc.ID, nil
		}
	}
	return "", fmt.Errorf("%w: account %s", model.ErrNotFound, ref)
}

// accountForIDs returns the account the messages belong to. It is empty when
// no id is found; it fails when the ids span more than one account, because a
// move takes a single folder id.
func (a *App) accountForIDs(ctx context.Context, api API, ids []string) (string, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	refs, err := api.Search(ctx, client.SearchOptions{Limit: lookupLimit})
	if err != nil {
		return "", err
	}
	account := ""
	for _, ref := range refs {
		if !want[ref.ID] || ref.Account == "" {
			continue
		}
		if account == "" {
			account = ref.Account
			continue
		}
		if account != ref.Account {
			return "", errors.New("messages belong to different accounts; move one account at a time")
		}
	}
	return account, nil
}

func (a *App) writeList(msgs []model.Message) error {
	if a.flags.json {
		rows, err := listJSON(msgs)
		if err != nil {
			return err
		}
		return a.writeJSON(rows)
	}
	for i := range msgs {
		m := &msgs[i]
		fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\n",
			m.ID,
			oneLine(m.Account),
			a.displayTime(m.Date),
			oneLine(firstAddress(m.From)),
			oneLine(formatAddresses(m.To)),
			oneLine(m.Subject),
		)
	}
	return nil
}

// displayTime shows a date the way a mail client does, in the reader's zone.
// Gofer stamps its own outgoing mail in UTC, and a bare "07:38" for a message
// that arrived at 15:38 reads as the wrong time.
func (a *App) displayTime(t time.Time) string {
	loc := a.Location
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format(dateTimeFormat)
}

// listJSON drops Text and HTML: a listing only needs the headers.
func listJSON(msgs []model.Message) ([]map[string]any, error) {
	rows := make([]map[string]any, 0, len(msgs))
	for i := range msgs {
		raw, err := json.Marshal(msgs[i])
		if err != nil {
			return nil, err
		}
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		delete(row, "text")
		delete(row, "html")
		rows = append(rows, row)
	}
	return rows, nil
}

func (a *App) writeMessage(m *model.Message) error {
	w := a.Stdout
	fmt.Fprintf(w, "ID: %s\n", cleanText(m.ID))
	if s := formatAddresses(m.From); s != "" {
		fmt.Fprintf(w, "From: %s\n", s)
	}
	if s := formatAddresses(m.To); s != "" {
		fmt.Fprintf(w, "To: %s\n", s)
	}
	if s := formatAddresses(m.Cc); s != "" {
		fmt.Fprintf(w, "Cc: %s\n", s)
	}
	if !m.Date.IsZero() {
		fmt.Fprintf(w, "Date: %s\n", a.displayTime(m.Date))
	}
	fmt.Fprintf(w, "Subject: %s\n", cleanText(m.Subject))
	fmt.Fprintln(w)
	fmt.Fprint(w, strings.TrimRight(cleanText(m.Text), "\r\n"))
	fmt.Fprintln(w)
	if len(m.Attachments) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Attachments:")
		for _, att := range m.Attachments {
			fmt.Fprintf(w, "  %d\t%s\t%s\t%d\n", att.Index, cleanText(att.Filename), cleanText(att.ContentType), att.Size)
		}
	}
	return nil
}

// printRef writes one message by reference. --raw goes to stdout exactly as
// received, without parsing.
func (a *App) printRef(ctx context.Context, api API, ref model.MessageRef, raw, html bool) error {
	if raw {
		src, err := api.Source(ctx, ref.ID)
		if err != nil {
			return err
		}
		_, err = a.Stdout.Write(src)
		return err
	}
	m, _, err := a.fetchMessage(ctx, api, ref)
	if err != nil {
		return err
	}
	return a.printMessage(m, html)
}

// printMessage writes a parsed message the way read and latest do.
func (a *App) printMessage(m *model.Message, html bool) error {
	if html {
		if m.HTML == "" {
			return fmt.Errorf("message %s has no HTML part", m.ID)
		}
		fmt.Fprint(a.Stdout, m.HTML)
		if !strings.HasSuffix(m.HTML, "\n") {
			fmt.Fprintln(a.Stdout)
		}
		return nil
	}
	if a.flags.json {
		return a.writeJSON(m)
	}
	return a.writeMessage(m)
}

// exclusiveOutput rejects --raw, --html and --json together.
func (a *App) exclusiveOutput(raw, html bool) error {
	n := 0
	if raw {
		n++
	}
	if html {
		n++
	}
	if a.flags.json {
		n++
	}
	if n > 1 {
		return errors.New("use only one of --raw, --html and --json")
	}
	return nil
}

func formatAddress(a model.Address) string {
	name, email := cleanText(a.Name), cleanText(a.Email)
	switch {
	case name == "":
		return email
	case email == "":
		return name
	default:
		return name + " <" + email + ">"
	}
}

func formatAddresses(list []model.Address) string {
	parts := make([]string, 0, len(list))
	for _, a := range list {
		parts = append(parts, formatAddress(a))
	}
	return strings.Join(parts, ", ")
}

func firstAddress(list []model.Address) string {
	if len(list) == 0 {
		return ""
	}
	return formatAddress(list[0])
}

func firstEmail(list []model.Address) string {
	if len(list) == 0 {
		return ""
	}
	return list[0].Email
}

func emails(list []model.Address) []string {
	out := make([]string, 0, len(list))
	for _, a := range list {
		if a.Email != "" {
			out = append(out, a.Email)
		}
	}
	return out
}

// without returns list minus the addresses in exclude, with duplicates
// dropped.
func without(list, exclude []string) []string {
	skip := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		skip[strings.ToLower(e)] = true
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		key := strings.ToLower(v)
		if v == "" || skip[key] {
			continue
		}
		skip[key] = true
		out = append(out, v)
	}
	return out
}

// cleanText drops C0 control characters, keeping tab, newline and carriage
// return so multi-line text keeps its layout.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, s)
}

// oneLine keeps a value on one tab-separated field.
func oneLine(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(cleanText(s))
}
