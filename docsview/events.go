package docsview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

const heartbeat = 15 * time.Second

// events streams live-reload notices as Server-Sent Events. On each tick the
// connection compares the vault version and the viewed file. "tree" reports
// any vault change; "page" reports a change to the viewed file or to its
// rendered HTML, such as a link that now resolves.
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
	vault, page, err := h.versions(viewed)
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
	if !send("hello", map[string]string{"version": vault}) {
		return
	}
	ticks, stop := h.ticker()
	defer stop()
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
		case <-ticks:
			nextVault, nextPage, err := h.versions(viewed)
			if err != nil || nextVault == vault && nextPage == page {
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

// versions returns the vault version and a version of the viewed page. With
// git, both include the git state.
func (h *Handler) versions(viewed string) (string, string, error) {
	idx, err := h.current()
	if err != nil {
		return "", "", err
	}
	vault := idx.version
	state := h.gitState(idx)
	if state != nil {
		// A commit, a stage, or a branch switch changes the tree marks.
		vault += ":" + state.digest
	}
	if viewed == "" {
		return vault, "", nil
	}
	page := "missing"
	if info, err := h.stat(viewed); err == nil {
		page = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	}
	if n, ok := idx.byPath[viewed]; ok {
		sum := sha256.Sum256([]byte(n.doc.HTML))
		page += ":" + hex.EncodeToString(sum[:8])
	}
	if state != nil && allowed(viewed) {
		// The diff of the page changes with HEAD and with the file status.
		page += ":" + state.head + ":" + h.fileStatus(idx, viewed)
	}
	return vault, page, nil
}
