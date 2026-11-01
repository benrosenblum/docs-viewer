package docsview

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

const heartbeat = 15 * time.Second

// poller holds the subscribers of the one poll loop of a handler. The loop
// runs only while a subscriber exists.
type poller struct {
	mu   sync.Mutex
	subs map[chan *index]bool
	quit chan struct{} // Closed to end the running loop.
}

// subscribe returns the channel of poll results and the function that ends
// the subscription. The first subscriber starts the loop; the last stops it.
func (h *Handler) subscribe() (<-chan *index, func()) {
	updates := make(chan *index, 1)
	p := &h.poller
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.subs) == 0 {
		p.quit = make(chan struct{})
		go h.poll(p.quit)
	}
	p.subs[updates] = true
	return updates, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		delete(p.subs, updates)
		if len(p.subs) == 0 {
			close(p.quit)
		}
	}
}

// poll scans once on each tick and publishes the snapshot. A subscriber that
// is busy misses the result and gets the next one.
func (h *Handler) poll(quit <-chan struct{}) {
	ticks, stop := h.ticker()
	defer stop()
	for {
		select {
		case <-quit:
			return
		case <-ticks:
			idx, err := h.current()
			if err != nil {
				continue
			}
			h.poller.mu.Lock()
			for updates := range h.poller.subs {
				select {
				case updates <- idx:
				default:
				}
			}
			h.poller.mu.Unlock()
		}
	}
}

// events streams live-reload notices as Server-Sent Events. On each poll
// result the connection compares the vault version and the viewed file.
// "tree" reports any vault change; "page" reports a change to the viewed file
// or to its rendered HTML, such as a link that now resolves.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming is unavailable", http.StatusInternalServerError)
		return
	}
	viewed := strings.Trim(r.URL.Query().Get("path"), "/")
	if viewed != "" && (path.Clean(viewed) != viewed || !allowed(viewed)) {
		viewed = ""
	}
	base := compareBase(r)
	vault, page, err := h.versions(viewed, base)
	if err != nil {
		http.Error(w, "Cannot read the documentation directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	send := func(event string, data any) bool {
		encoded, _ := json.Marshal(data)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	updates, unsubscribe := h.subscribe()
	defer unsubscribe()
	if !send("hello", map[string]string{"version": vault}) {
		return
	}
	ping := time.NewTicker(heartbeat)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case idx := <-updates:
			nextVault, nextPage := h.versionsOf(idx, viewed, base)
			if nextVault == vault && nextPage == page {
				continue
			}
			change := map[string]any{"tree": nextVault != vault, "page": nextPage != page, "version": nextVault}
			vault, page = nextVault, nextPage
			if !send("change", change) {
				return
			}
		}
	}
}

// versions returns the versions of the present snapshot.
func (h *Handler) versions(viewed, base string) (string, string, error) {
	idx, err := h.current()
	if err != nil {
		return "", "", err
	}
	vault, page := h.versionsOf(idx, viewed, base)
	return vault, page, nil
}

// versionsOf returns the vault version and a version of the viewed page. With
// git, both include the git state against the compare base. It runs on each
// tick for each connection, so it only does stats and reads caches.
func (h *Handler) versionsOf(idx *index, viewed, base string) (string, string) {
	vault := idx.version
	state := h.gitState(idx, base)
	if state != nil {
		// A commit, a stage, or a branch switch changes the tree marks.
		vault += ":" + state.digest
	}
	if viewed == "" {
		return vault, ""
	}
	page := "missing"
	if info, err := h.stat(viewed); err == nil {
		page = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	}
	if n, ok := idx.byPath[viewed]; ok {
		page += ":" + n.hash
	}
	if state != nil && allowed(viewed) {
		// The diff of the page changes with HEAD and with the file status.
		page += ":" + state.head + ":" + h.fileStatus(state, viewed)
	}
	return vault, page
}
