// Phase D3: HTTP endpoints for runtime credentials. The handler proxies
// to control-panel-service.IssueRuntimeCredential / ListRuntimeCredentials
// / RevokeRuntimeCredential. The authenticated JWT subject (`uid` claim)
// is the only valid `user_id` — handlers force the gRPC request to use
// the JWT user, not anything from the body / path. Frontend never sees
// the private key persisted; it is only present in the IssueResponse.
package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
)

// ── JSON shapes ─────────────────────────────────────────────────────────────

type runtimeCredentialJSON struct {
	KeyID             string `json:"key_id"`
	UserID            int64  `json:"user_id"`
	Label             string `json:"label,omitempty"`
	Role              string `json:"role"`
	Status            string `json:"status"`
	PublicKeyPEM      string `json:"public_key_pem"`
	CreatedAt         string `json:"created_at"`
	DownloadedAt      string `json:"downloaded_at,omitempty"`
	ConsumedAt        string `json:"consumed_at,omitempty"`
	ConsumedRuntimeID string `json:"consumed_runtime_id,omitempty"`
	ExpiresAt         string `json:"expires_at,omitempty"`
	LastUsedAt        string `json:"last_used_at,omitempty"`
	RevokedAt         string `json:"revoked_at,omitempty"`
	HostedInternal    bool   `json:"hosted_internal,omitempty"`
}

// issueRuntimeCredentialBody is the (small) request body for POST
// /api/runtime-credentials. user_id is intentionally not accepted from
// the body — the JWT is authoritative.
type issueRuntimeCredentialBody struct {
	Label string `json:"label"`
	Role  string `json:"role"`
}

// issueRuntimeCredentialResponseJSON includes the private key — this is
// the ONLY response that ever does. The frontend MUST trigger a download
// and clear the value from memory immediately.
type issueRuntimeCredentialResponseJSON struct {
	KeyID         string `json:"key_id"`
	PrivateKeyPEM string `json:"private_key_pem"`
	PublicKeyPEM  string `json:"public_key_pem"`
	CreatedAt     string `json:"created_at"`
	Role          string `json:"role"`
}

type revokeRuntimeCredentialResponseJSON struct {
	Credential    runtimeCredentialJSON `json:"credential"`
	StreamsClosed int32                 `json:"streams_closed"`
	RuntimesEnded int32                 `json:"runtimes_ended"`
}

// ── Converters ──────────────────────────────────────────────────────────────

func runtimeCredentialToJSON(c *controlpanelv1.RuntimeCredential) runtimeCredentialJSON {
	if c == nil {
		return runtimeCredentialJSON{}
	}
	out := runtimeCredentialJSON{
		KeyID:             c.GetKeyId(),
		UserID:            c.GetUserId(),
		Label:             c.GetLabel(),
		Role:              c.GetRole(),
		Status:            c.GetStatus(),
		PublicKeyPEM:      c.GetPublicKeyPem(),
		ConsumedRuntimeID: c.GetConsumedRuntimeId(),
		HostedInternal:    c.GetHostedInternal(),
	}
	if c.GetCreatedAt() != nil {
		out.CreatedAt = c.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if c.GetDownloadedAt() != nil {
		out.DownloadedAt = c.GetDownloadedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if c.GetConsumedAt() != nil {
		out.ConsumedAt = c.GetConsumedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if c.GetExpiresAt() != nil {
		out.ExpiresAt = c.GetExpiresAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if c.GetLastUsedAt() != nil {
		out.LastUsedAt = c.GetLastUsedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if c.GetRevokedAt() != nil {
		out.RevokedAt = c.GetRevokedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	return out
}

// ── Route dispatcher ────────────────────────────────────────────────────────

// handleRuntimeCredentialsCollection: GET / POST /api/runtime-credentials
func (s *server) handleRuntimeCredentialsCollection(w http.ResponseWriter, r *http.Request) {
	if s.cpRuntime == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service is not configured (runtime credentials unavailable)")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listRuntimeCredentials(w, r)
	case http.MethodPost:
		s.issueRuntimeCredential(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRuntimeCredentialsByID: DELETE /api/runtime-credentials/{key_id}
func (s *server) handleRuntimeCredentialsByID(w http.ResponseWriter, r *http.Request) {
	if s.cpRuntime == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service is not configured (runtime credentials unavailable)")
		return
	}
	keyID := strings.TrimPrefix(r.URL.Path, "/api/runtime-credentials/")
	keyID = strings.Trim(keyID, "/")
	if keyID == "" {
		writeErr(w, http.StatusBadRequest, "key_id is required in path")
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.revokeRuntimeCredential(w, r, keyID)
}

// ── Handlers ────────────────────────────────────────────────────────────────

func (s *server) issueRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var body issueRuntimeCredentialBody
	// An empty body is OK — label is optional.
	_ = json.NewDecoder(r.Body).Decode(&body)
	role := strings.TrimSpace(body.Role)
	if role == "" {
		role = "executor"
	}

	resp, err := s.cpRuntime.IssueRuntimeCredential(r.Context(), &controlpanelv1.IssueRuntimeCredentialRequest{
		UserId: uid,
		Label:  strings.TrimSpace(body.Label),
		Role:   role,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	out := issueRuntimeCredentialResponseJSON{
		KeyID:         resp.GetKeyId(),
		PrivateKeyPEM: resp.GetPrivateKeyPem(),
		PublicKeyPEM:  resp.GetPublicKeyPem(),
		Role:          resp.GetRole(),
	}
	if resp.GetCreatedAt() != nil {
		out.CreatedAt = resp.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *server) listRuntimeCredentials(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	includeRevoked := r.URL.Query().Get("include_revoked") == "true"
	includeInactive := r.URL.Query().Get("include_inactive") == "true"
	resp, err := s.cpRuntime.ListRuntimeCredentials(r.Context(), &controlpanelv1.ListRuntimeCredentialsRequest{
		UserId:          uid,
		IncludeRevoked:  includeRevoked,
		IncludeInactive: includeInactive,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	out := make([]runtimeCredentialJSON, 0, len(resp.GetCredentials()))
	for _, c := range resp.GetCredentials() {
		out = append(out, runtimeCredentialToJSON(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) revokeRuntimeCredential(w http.ResponseWriter, r *http.Request, keyID string) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	resp, err := s.cpRuntime.RevokeRuntimeCredential(r.Context(), &controlpanelv1.RevokeRuntimeCredentialRequest{
		UserId: uid,
		KeyId:  keyID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, revokeRuntimeCredentialResponseJSON{
		Credential:    runtimeCredentialToJSON(resp.GetCredential()),
		StreamsClosed: resp.GetStreamsClosed(),
		RuntimesEnded: resp.GetRuntimesEnded(),
	})
}
