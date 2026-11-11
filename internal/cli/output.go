package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/noir017/gofer-cli/internal/client"
	"github.com/noir017/gofer-cli/internal/model"
)

const (
	dateTimeFormat = "2006-01-02 15:04"
	fetchWorkers   = 4
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
	refs, err := api.Search(ctx, client.SearchOptions{
		Query:     f.searchQuery(),
		AccountID: accountID,
		Unread:    f.unread,
		Limit:     f.limit,
	})
	if err != nil {
		return nil, err
	}
	return a.fetchAll(ctx, api, refs)
}

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
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return msgs, nil
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
		fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%s\t%s\n",
			m.ID,
			m.Date.Format(dateTimeFormat),
			oneLine(firstAddress(m.From)),
			oneLine(formatAddresses(m.To)),
			oneLine(m.Subject),
		)
	}
	return nil
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
	fmt.Fprintf(w, "ID: %s\n", m.ID)
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
		fmt.Fprintf(w, "Date: %s\n", m.Date.Format(dateTimeFormat))
	}
	fmt.Fprintf(w, "Subject: %s\n", m.Subject)
	fmt.Fprintln(w)
	fmt.Fprint(w, strings.TrimRight(m.Text, "\r\n"))
	fmt.Fprintln(w)
	if len(m.Attachments) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Attachments:")
		for _, att := range m.Attachments {
			fmt.Fprintf(w, "  %d\t%s\t%s\t%d\n", att.Index, att.Filename, att.ContentType, att.Size)
		}
	}
	return nil
}

// printRead writes one message the way read and latest do.
func (a *App) printRead(m *model.Message, src []byte, raw, html bool) error {
	if raw {
		_, err := a.Stdout.Write(src)
		return err
	}
	if html {
		if m.HTML == "" {
			return fmt.Errorf("%w: message %s has no HTML part", model.ErrNotFound, m.ID)
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

func formatAddress(a model.Address) string {
	switch {
	case a.Name == "":
		return a.Email
	case a.Email == "":
		return a.Name
	default:
		return a.Name + " <" + a.Email + ">"
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

// oneLine keeps a value on one tab-separated field.
func oneLine(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}
