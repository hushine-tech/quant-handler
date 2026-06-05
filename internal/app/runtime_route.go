package app

import "net/http"

// handleResolveRuntimeRoute is kept only to return a stable removal error for
// older operator bookmarks. Runtime selection is by runtime_id via /api/runtimes.
func (s *server) handleResolveRuntimeRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeErr(w, http.StatusGone, "runtime route by name has been removed; use runtime_id")
}
