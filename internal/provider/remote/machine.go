package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"deck2/internal/health"
	"deck2/internal/home"
	"deck2/internal/provider"
)

// Machine is a node deck runs turns and scripts on, and what deck keeps of them on its own machine.
type Machine struct {
	// Node is the name of the node, Device its device.
	Node   string
	Device int64
	Hub    Hub
	// Home is deck's folder, the folders of whose turns and scripts go to the node and back.
	Home home.Layout
	// Wait is how long a turn or script whose node is away waits for it.
	Wait time.Duration
	// Low hears that the disk of deck runs low by files a turn on a node brought back.
	Low func(ctx context.Context, text string)
	// Denied hears that the node refused a folder its owner did not allow.
	Denied func(device int64)
}

func (m *Machine) online() bool { return m.Hub.State(m.Device).Online }

func (m *Machine) wait0() time.Duration {
	if m.Wait <= 0 {
		return 3 * time.Minute
	}
	return m.Wait
}

// rel is a folder of deck's work by its path under deck's folder; no other goes to a node.
func (m *Machine) rel(path string) (string, error) {
	r, err := filepath.Rel(string(m.Home), path)
	if err != nil || !filepath.IsLocal(r) || !strings.HasPrefix(filepath.ToSlash(r), "work/") {
		return "", fmt.Errorf("%s is no folder of deck's work", path)
	}
	return filepath.ToSlash(r), nil
}

// call sends a call of the node's agents and gives its answer, a failure when the node answers another status.
func (m *Machine) call(ctx context.Context, method, path string, query url.Values, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://node"+path+"?"+query.Encode(), body)
	if err != nil {
		return nil, err
	}
	resp, err := m.Hub.Agent(m.Device, m.Node).RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &refused{status: resp.StatusCode, text: strings.TrimSpace(string(b)), folder: resp.Header.Get(folderHeader) != ""}
	}
	return resp, nil
}

// refused is a call of a node's agents the node answered with a failure; folder says it refused a folder its owner
// did not allow.
type refused struct {
	status int
	text   string
	folder bool
}

func (r *refused) Error() string { return r.text }

// denied tells health that the node refused a folder, so its row says what its owner adds.
func (m *Machine) denied(r *refused) {
	if r.folder && m.Denied != nil {
		m.Denied(m.Device)
	}
}

// tooBig is a run whose folders are more than deck hands a node.
type tooBig struct{ n int64 }

func (e *tooBig) Error() string {
	return fmt.Sprintf("the folders of the run hold %d MB, more than the %d MB deck hands a node", e.n>>20, sendLimit>>20)
}

// send hands the node the files of the folders of a run, in place of those it kept of the session; sent are the files
// as they were on deck.
func (m *Machine) send(ctx context.Context, session string, roots []string) (sent map[string]stamp, err error) {
	files, err := walk(string(m.Home), roots)
	if err != nil {
		return nil, err
	}
	if n := size(files); n > sendLimit {
		return nil, &tooBig{n}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	r, w := io.Pipe()
	go func() { w.CloseWithError(pack(w, string(m.Home), names)) }()
	resp, err := m.call(ctx, http.MethodPut, filesPath, url.Values{"session": {session}, "roots": roots}, r)
	r.Close()
	if err != nil {
		return nil, err
	}
	resp.Body.Close()
	return files, nil
}

// wait waits for the node while it is away, as the feed says with then, what waits; back is false when it did not
// come back in time.
func (m *Machine) wait(ctx context.Context, emit func(provider.Event), what, then string) (back bool, err error) {
	emit(provider.Event{Kind: provider.EventWarning, Title: health.Node(m.Node), Text: fmt.Sprintf("node %s %s; %s", m.Node, what, then)})
	deadline := time.NewTimer(m.wait0())
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for !m.online() {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-tick.C:
		}
	}
	return true, nil
}

// bringing keeps the files of one run coming back apart from those of another.
var bringing sync.Mutex

// bring takes the files a run changed back from the node, unless deck's disk would not hold them; a file that changed
// on deck since it was sent, as by another run of the project, stays deck's, and say tells the run's log which.
func (m *Machine) bring(ctx context.Context, session string, roots []string, sent map[string]stamp, say func(string)) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	q := url.Values{"session": {session}}
	gone, err := m.call(ctx, http.MethodGet, gonePath, q, nil)
	if err != nil {
		say(fmt.Sprintf("deck: the files the run changed on node %s did not come back: %v", m.Node, err))
		return
	}
	var listed struct {
		Size int64    `json:"size"`
		Gone []string `json:"gone"`
	}
	err = json.NewDecoder(io.LimitReader(gone.Body, 64<<20)).Decode(&listed)
	gone.Body.Close()
	if err != nil {
		say(fmt.Sprintf("deck: node %s did not say what the run changed: %v", m.Node, err))
		return
	}
	var fs syscall.Statfs_t
	if syscall.Statfs(string(m.Home), &fs) == nil {
		free := int64(fs.Bavail) * int64(fs.Bsize)
		if free-listed.Size < keepFree {
			text := fmt.Sprintf("the files of a run on node %s (%d MB) do not fit the disk of deck (%d MB free); the next run of the session on the node starts from deck's copy", m.Node, listed.Size>>20, free>>20)
			m.low(ctx, text)
			say("deck: " + text)
			return
		}
		if free-listed.Size < lowFree {
			m.low(ctx, fmt.Sprintf("the disk is almost full: the files of a run on node %s took %d MB, %d MB are free", m.Node, listed.Size>>20, (free-listed.Size)>>20))
		}
	}
	// Two runs coming back at once check and write deck's files one after the other.
	bringing.Lock()
	defer bringing.Unlock()
	var moved []string
	changed := func(name string) bool {
		now, there := stampOf(string(m.Home), name)
		was, sentIt := sent[name]
		if there != sentIt || there && (now.Size != was.Size || !now.Mod.Equal(was.Mod)) {
			moved = append(moved, name)
			return true
		}
		return false
	}
	resp, err := m.call(ctx, http.MethodGet, filesPath, q, nil)
	if err == nil {
		// A tar holds a header and padding of up to a KB a file beside what the node said its files hold.
		body := io.LimitReader(resp.Body, listed.Size+64<<20)
		err = home.Unpack(string(m.Home), body, func(name string) bool { return under(name, roots) }, changed)
		resp.Body.Close()
	}
	var removed []string
	for _, g := range listed.Gone {
		if under(g, roots) && !changed(g) {
			removed = append(removed, g)
		}
	}
	if err == nil {
		err = home.RemoveIn(string(m.Home), removed)
	}
	if len(moved) > 0 {
		say(fmt.Sprintf("deck: %d files the run changed on node %s changed on deck meanwhile and stay as they are on deck; the run's changes to them are lost: %s",
			len(moved), m.Node, strings.Join(moved[:min(len(moved), 20)], ", ")))
	}
	if err != nil {
		say(fmt.Sprintf("deck: the files the run changed on node %s did not all come back: %v", m.Node, err))
		return
	}
	if resp, err := m.call(ctx, http.MethodDelete, filesPath, q, nil); err == nil {
		resp.Body.Close()
	}
}

func (m *Machine) low(ctx context.Context, text string) {
	if m.Low != nil {
		m.Low(ctx, text)
	}
}
