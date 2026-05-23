package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hushine-tech/quant-handler/internal/controlpanel"
)

type runtimeJSON struct {
	RuntimeID                  string              `json:"runtime_id"`
	UserID                     int64               `json:"user_id"`
	Name                       string              `json:"name"`
	Source                     string              `json:"source"`
	Role                       string              `json:"role"`
	EndpointHost               string              `json:"endpoint_host,omitempty"`
	GRPCPort                   int32               `json:"grpc_port,omitempty"`
	DebugPort                  int32               `json:"debug_port,omitempty"`
	Capabilities               []string            `json:"capabilities,omitempty"`
	ResourceProfile            string              `json:"resource_profile"`
	Version                    string              `json:"version,omitempty"`
	Status                     string              `json:"status"`
	CredentialKeyID            string              `json:"credential_key_id,omitempty"`
	PairedAt                   string              `json:"paired_at,omitempty"`
	StartedAt                  string              `json:"started_at,omitempty"`
	EndedAt                    string              `json:"ended_at,omitempty"`
	EndedReason                string              `json:"ended_reason,omitempty"`
	CleanupStatus              string              `json:"cleanup_status,omitempty"`
	CleanupReason              string              `json:"cleanup_reason,omitempty"`
	CleanupAt                  string              `json:"cleanup_at,omitempty"`
	HeartbeatAt                string              `json:"heartbeat_at,omitempty"`
	ConnectionOwnerInstanceID  string              `json:"connection_owner_instance_id,omitempty"`
	ConnectionOwnerAcquiredAt  string              `json:"connection_owner_acquired_at,omitempty"`
	ConnectionOwnerHeartbeatAt string              `json:"connection_owner_heartbeat_at,omitempty"`
	CreatedAt                  string              `json:"created_at,omitempty"`
	UpdatedAt                  string              `json:"updated_at,omitempty"`
	DebugWorkspace             *debugWorkspaceJSON `json:"debug_workspace,omitempty"`
	DebugDataset               *debugDatasetJSON   `json:"debug_dataset,omitempty"`
}

type runtimeListJSON struct {
	Runtimes []runtimeJSON `json:"runtimes"`
	HasMore  bool          `json:"has_more"`
	Total    int64         `json:"total"`
}

type runtimeAdmissionFailureJSON struct {
	AdmissionFailureID int64  `json:"admission_failure_id"`
	UserID             int64  `json:"user_id"`
	CredentialKeyID    string `json:"credential_key_id,omitempty"`
	RequestedRuntimeID string `json:"requested_runtime_id,omitempty"`
	RequestedName      string `json:"requested_name,omitempty"`
	Source             string `json:"source,omitempty"`
	Role               string `json:"role,omitempty"`
	FailureCode        string `json:"failure_code"`
	Reason             string `json:"reason"`
	ConsumedRuntimeID  string `json:"consumed_runtime_id,omitempty"`
	FirstSeenAt        string `json:"first_seen_at,omitempty"`
	LastSeenAt         string `json:"last_seen_at,omitempty"`
	AttemptCount       int32  `json:"attempt_count"`
}

type ensureHostedRuntimeBody struct {
	Name            string `json:"name"`
	ResourceProfile string `json:"resource_profile"`
}

type ensureHostedRuntimeJSON struct {
	Runtime     runtimeJSON `json:"runtime"`
	Provisioned bool        `json:"provisioned"`
}

type debugWorkspaceJSON struct {
	HostPath              string `json:"host_path,omitempty"`
	ContainerPath         string `json:"container_path,omitempty"`
	TemplatePath          string `json:"template_path,omitempty"`
	ArchivedTemplatePath  string `json:"archived_template_path,omitempty"`
	VSCodeLaunchCreated   bool   `json:"vscode_launch_created"`
	VSCodeLaunchPreserved bool   `json:"vscode_launch_preserved"`
	PyCharmDocCreated     bool   `json:"pycharm_doc_created"`
	PyCharmDocPreserved   bool   `json:"pycharm_doc_preserved"`
	PreparedAt            string `json:"prepared_at,omitempty"`
	LastError             string `json:"last_error,omitempty"`
}

type debugDatasetJSON struct {
	DatasetID      string `json:"dataset_id,omitempty"`
	UserID         int64  `json:"user_id,omitempty"`
	AccountID      int64  `json:"account_id,omitempty"`
	RuntimeID      string `json:"runtime_id,omitempty"`
	Market         string `json:"market,omitempty"`
	Symbol         string `json:"symbol,omitempty"`
	Interval       string `json:"interval,omitempty"`
	StartTimeMS    int64  `json:"start_time_ms,omitempty"`
	EndTimeMS      int64  `json:"end_time_ms,omitempty"`
	BarCount       int64  `json:"bar_count,omitempty"`
	CoverageStatus string `json:"coverage_status,omitempty"`
	LoadedAt       string `json:"loaded_at,omitempty"`
	State          string `json:"state,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type prepareDebugWorkspaceBody struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
}

func runtimeToJSON(rt controlpanel.Runtime) runtimeJSON {
	return runtimeJSON{
		RuntimeID:                  rt.RuntimeID,
		UserID:                     rt.UserID,
		Name:                       rt.Name,
		Source:                     rt.Source,
		Role:                       rt.Role,
		EndpointHost:               rt.EndpointHost,
		GRPCPort:                   rt.GRPCPort,
		DebugPort:                  rt.DebugPort,
		Capabilities:               rt.Capabilities,
		ResourceProfile:            rt.ResourceProfile,
		Version:                    rt.Version,
		Status:                     rt.Status,
		CredentialKeyID:            rt.CredentialKeyID,
		PairedAt:                   formatRuntimeTime(rt.PairedAt),
		StartedAt:                  formatRuntimeTime(rt.StartedAt),
		EndedAt:                    formatRuntimeTime(rt.EndedAt),
		EndedReason:                rt.EndedReason,
		CleanupStatus:              rt.CleanupStatus,
		CleanupReason:              rt.CleanupReason,
		CleanupAt:                  formatRuntimeTime(rt.CleanupAt),
		HeartbeatAt:                formatRuntimeTime(rt.HeartbeatAt),
		ConnectionOwnerInstanceID:  rt.ConnectionOwnerInstanceID,
		ConnectionOwnerAcquiredAt:  formatRuntimeTime(rt.ConnectionOwnerAcquiredAt),
		ConnectionOwnerHeartbeatAt: formatRuntimeTime(rt.ConnectionOwnerHeartbeatAt),
		CreatedAt:                  formatRuntimeTime(rt.CreatedAt),
		UpdatedAt:                  formatRuntimeTime(rt.UpdatedAt),
		DebugWorkspace:             debugWorkspaceToJSON(rt.DebugWorkspace),
		DebugDataset:               debugDatasetToJSON(rt.DebugDataset),
	}
}

func debugWorkspaceToJSON(ws *controlpanel.DebugWorkspaceState) *debugWorkspaceJSON {
	if ws == nil {
		return nil
	}
	return &debugWorkspaceJSON{
		HostPath:              ws.HostPath,
		ContainerPath:         ws.ContainerPath,
		TemplatePath:          ws.TemplatePath,
		ArchivedTemplatePath:  ws.ArchivedTemplatePath,
		VSCodeLaunchCreated:   ws.VSCodeLaunchCreated,
		VSCodeLaunchPreserved: ws.VSCodeLaunchPreserved,
		PyCharmDocCreated:     ws.PyCharmDocCreated,
		PyCharmDocPreserved:   ws.PyCharmDocPreserved,
		PreparedAt:            formatRuntimeTime(ws.PreparedAt),
		LastError:             ws.LastError,
	}
}

func debugDatasetToJSON(ds *controlpanel.DebugDatasetState) *debugDatasetJSON {
	if ds == nil {
		return nil
	}
	return &debugDatasetJSON{
		DatasetID:      ds.DatasetID,
		UserID:         ds.UserID,
		AccountID:      ds.AccountID,
		RuntimeID:      ds.RuntimeID,
		Market:         ds.Market,
		Symbol:         ds.Symbol,
		Interval:       ds.Interval,
		StartTimeMS:    ds.StartTimeMS,
		EndTimeMS:      ds.EndTimeMS,
		BarCount:       ds.BarCount,
		CoverageStatus: ds.CoverageStatus,
		LoadedAt:       formatRuntimeTime(ds.LoadedAt),
		State:          ds.State,
		LastError:      ds.LastError,
	}
}

func admissionFailureToJSON(f controlpanel.RuntimeAdmissionFailure) runtimeAdmissionFailureJSON {
	return runtimeAdmissionFailureJSON{
		AdmissionFailureID: f.AdmissionFailureID,
		UserID:             f.UserID,
		CredentialKeyID:    f.CredentialKeyID,
		RequestedRuntimeID: f.RequestedRuntimeID,
		RequestedName:      f.RequestedName,
		Source:             f.Source,
		Role:               f.Role,
		FailureCode:        f.FailureCode,
		Reason:             f.Reason,
		ConsumedRuntimeID:  f.ConsumedRuntimeID,
		FirstSeenAt:        formatRuntimeTime(f.FirstSeenAt),
		LastSeenAt:         formatRuntimeTime(f.LastSeenAt),
		AttemptCount:       f.AttemptCount,
	}
}

func formatRuntimeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (s *server) handleRuntimesCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listRuntimes(w, r)
	case http.MethodPost:
		s.ensureHostedRuntime(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleRuntimeByID(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/runtimes/")
	suffix = strings.Trim(suffix, "/")
	parts := strings.Split(suffix, "/")
	runtimeID := strings.TrimSpace(parts[0])
	if runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	if len(parts) == 2 && parts[1] == "prepare-debugging" {
		s.handlePrepareDebugWorkspace(w, r, uid, runtimeID)
		return
	}
	if len(parts) == 2 && parts[1] == "debug-dataset" {
		s.handleRuntimeDebugDataset(w, r, uid, runtimeID)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		rt, err := s.controlPanel.GetRuntime(r.Context(), uid, runtimeID)
		if err != nil {
			writeControlPanelErr(w, err, "control-panel-service not configured")
			return
		}
		writeJSON(w, http.StatusOK, runtimeToJSON(rt))
	case http.MethodDelete:
		rt, err := s.controlPanel.EndRuntime(r.Context(), uid, runtimeID)
		if err != nil {
			writeControlPanelErr(w, err, "control-panel-service not configured")
			return
		}
		writeJSON(w, http.StatusOK, runtimeToJSON(rt))
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handlePrepareDebugWorkspace(w http.ResponseWriter, r *http.Request, uid int64, runtimeID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body prepareDebugWorkspaceBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	state, err := s.controlPanel.PrepareDebugWorkspace(
		r.Context(),
		uid,
		runtimeID,
		strings.TrimSpace(body.HostPath),
		strings.TrimSpace(body.ContainerPath),
	)
	if err != nil {
		writeControlPanelErr(w, err, "control-panel-service not configured")
		return
	}
	writeJSON(w, http.StatusOK, debugWorkspaceToJSON(&state))
}

func (s *server) handleRuntimeDebugDataset(w http.ResponseWriter, r *http.Request, uid int64, runtimeID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	state, err := s.controlPanel.GetRuntimeDebugDataset(r.Context(), uid, runtimeID)
	if err != nil {
		writeControlPanelErr(w, err, "control-panel-service not configured")
		return
	}
	writeJSON(w, http.StatusOK, debugDatasetToJSON(&state))
}

func (s *server) handleRuntimeAdmissionFailures(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	failures, err := s.controlPanel.ListRuntimeAdmissionFailures(r.Context(), uid, limit)
	if err != nil {
		writeControlPanelErr(w, err, "control-panel-service not configured")
		return
	}
	out := make([]runtimeAdmissionFailureJSON, 0, len(failures))
	for _, f := range failures {
		out = append(out, admissionFailureToJSON(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{"failures": out})
}

func (s *server) listRuntimes(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	q := r.URL.Query()
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			offset = n
		}
	}
	result, err := s.controlPanel.ListRuntimes(r.Context(), uid, q.Get("status"), q.Get("source"), limit, offset)
	if err != nil {
		writeControlPanelErr(w, err, "control-panel-service not configured")
		return
	}
	items := filterRuntimeListForSelection(result.Runtimes, q.Get("eligible"), q.Get("eligible_for"), q.Get("role"), q.Get("mode"))
	total := result.Total
	hasMore := result.HasMore
	if len(items) != len(result.Runtimes) {
		total = int64(len(items))
		hasMore = false
	}
	out := runtimeListJSON{
		Runtimes: make([]runtimeJSON, 0, len(items)),
		HasMore:  hasMore,
		Total:    total,
	}
	for _, rt := range items {
		out.Runtimes = append(out.Runtimes, runtimeToJSON(rt))
	}
	writeJSON(w, http.StatusOK, out)
}

func filterRuntimeListForSelection(runtimes []controlpanel.Runtime, eligible, eligibleFor, role, modeRaw string) []controlpanel.Runtime {
	eligibilityRequested := strings.TrimSpace(eligible) != "" || strings.TrimSpace(eligibleFor) != ""
	role = strings.ToLower(strings.TrimSpace(role))
	mode, hasMode := parseRuntimeEligibilityMode(modeRaw)
	if !eligibilityRequested && role == "" && !hasMode {
		return runtimes
	}
	if role == "" && eligibilityRequested && !hasMode {
		role = "executor"
	}
	out := make([]controlpanel.Runtime, 0, len(runtimes))
	for _, rt := range runtimes {
		if runtimeMatchesSelectionEligibility(rt, eligibilityRequested, role, mode, hasMode) {
			out = append(out, rt)
		}
	}
	return out
}

func parseRuntimeEligibilityMode(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	mode, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return mode, true
}

func runtimeMatchesSelectionEligibility(rt controlpanel.Runtime, requireHealthy bool, role string, mode int, hasMode bool) bool {
	if requireHealthy && !runtimeHealthyForSelection(rt) {
		return false
	}
	if role != "" && normalizedRuntimeRole(rt.Role) != role {
		return false
	}
	if !hasMode {
		return true
	}
	switch normalizedRuntimeRole(rt.Role) {
	case "executor":
		return mode == 0 || mode == 2
	case "debugger":
		return mode == 0
	default:
		return false
	}
}

func normalizedRuntimeRole(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return "executor"
	}
	return role
}

func runtimeHealthyForSelection(rt controlpanel.Runtime) bool {
	switch strings.ToLower(strings.TrimSpace(rt.Status)) {
	case "active", "running", "ready", "healthy", "online", "paired":
		return !rt.HeartbeatAt.IsZero() && strings.TrimSpace(rt.ConnectionOwnerInstanceID) != ""
	default:
		return false
	}
}

func (s *server) ensureHostedRuntime(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var body ensureHostedRuntimeBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := strings.TrimSpace(body.Name)
	resourceProfile := strings.TrimSpace(body.ResourceProfile)
	if resourceProfile == "" {
		resourceProfile = "small"
	}
	result, err := s.controlPanel.EnsureHostedRuntime(r.Context(), uid, name, resourceProfile)
	if err != nil {
		writeControlPanelErr(w, err, "control-panel-service not configured")
		return
	}
	rt, err := s.controlPanel.GetRuntime(r.Context(), uid, result.RuntimeID)
	if err != nil {
		writeControlPanelErr(w, err, "hosted runtime created but detail lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, ensureHostedRuntimeJSON{
		Runtime:     runtimeToJSON(rt),
		Provisioned: result.Provisioned,
	})
}

func writeControlPanelErr(w http.ResponseWriter, err error, notConfiguredMessage string) {
	if errors.Is(err, controlpanel.ErrNotConfigured) {
		writeErr(w, http.StatusServiceUnavailable, notConfiguredMessage)
		return
	}
	code, msg := grpcToHTTP(err)
	writeErr(w, code, msg)
}
