package app

import "net/http"

// runtimeRouteResponse is the JSON shape returned by the debug resolve
// endpoint. Endpoint/token fields are compatibility metadata only; normal
// strategy traffic routes by runtime_id through control-panel proxy RPCs.
type runtimeRouteResponse struct {
	RuntimeID     string `json:"runtime_id"`
	Name          string `json:"name"`
	GRPCEndpoint  string `json:"grpc_endpoint"`
	DebugEndpoint string `json:"debug_endpoint,omitempty"`
}

// handleResolveRuntimeRoute is a feature-flagged debug endpoint that lets
// operators verify control-panel-service is reachable and returns the expected
// hosted runtime route for the authenticated user.
func (s *server) handleResolveRuntimeRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.controlPanelRouteFeature {
		http.NotFound(w, r)
		return
	}
	writeErr(w, http.StatusGone, "runtime route by name has been removed; use runtime_id")
}
