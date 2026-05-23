package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hushine-tech/core-service/gen/accountv1"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
)

// ── Request / Response types ─────────────────────────────────────────────────

type createStrategyRequest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Code        string `json:"code"`
}

type strategyJSON struct {
	StrategyID     int64  `json:"strategy_id"`
	Name           string `json:"name"`
	Version        string `json:"version"`
	Description    string `json:"description"`
	Code           string `json:"code,omitempty"`
	Archived       bool   `json:"archived"`
	CreatedAt      string `json:"created_at"`
	RuntimeVersion string `json:"runtime_version,omitempty"`
	RuntimeProfile string `json:"runtime_profile,omitempty"`
}

type strategyValidationIssueJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Module  string `json:"module,omitempty"`
	Line    int32  `json:"line,omitempty"`
}

// ── Route handler: /api/strategies and /api/strategies/{id} ─────────────────

func (s *server) handleStrategiesCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listStrategies(w, r)
	case http.MethodPost:
		s.createStrategy(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleStrategiesByID(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/strategies/")
	suffix = strings.Trim(suffix, "/")
	parts := strings.Split(suffix, "/")

	rawID := strings.TrimSpace(parts[0])
	if rawID == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "strategy_id must be an integer")
		return
	}

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.getStrategy(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "archive" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.archiveStrategy(w, r, id)
		return
	}
	http.NotFound(w, r)
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (s *server) createStrategy(w http.ResponseWriter, r *http.Request) {
	var body createStrategyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Name == "" || body.Version == "" || body.Code == "" {
		writeErr(w, http.StatusBadRequest, "name, version, and code are required")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	if s.strategy == nil {
		writeErr(w, http.StatusServiceUnavailable, "strategy-service is not configured")
		return
	}
	validation, err := s.strategy.ValidateStrategyCode(r.Context(), &strategyv1.ValidateStrategyCodeRequest{
		UserId: uid,
		Code:   body.Code,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	if !validation.GetOk() {
		issues := make([]strategyValidationIssueJSON, 0, len(validation.GetIssues()))
		for _, issue := range validation.GetIssues() {
			issues = append(issues, strategyValidationIssueJSON{
				Code:    issue.GetCode(),
				Message: issue.GetMessage(),
				Module:  issue.GetModule(),
				Line:    issue.GetLine(),
			})
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":                       "strategy validation failed",
			"issues":                      issues,
			"runtime_version":             validation.GetRuntimeVersion(),
			"runtime_profile":             validation.GetRuntimeProfile(),
			"allowed_third_party_modules": validation.GetAllowedThirdPartyModules(),
		})
		return
	}

	resp, err := s.accounts.CreateStrategy(r.Context(), &accountv1.CreateStrategyRequest{
		Name:           body.Name,
		Version:        body.Version,
		Description:    body.Description,
		Code:           body.Code,
		UserId:         uid,
		RuntimeVersion: validation.GetRuntimeVersion(),
		RuntimeProfile: validation.GetRuntimeProfile(),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusCreated, protoStrategyToJSON(resp.GetStrategy(), true))
}

func (s *server) listStrategies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	namePrefix := q.Get("name_prefix")
	activeOnly := q.Get("active_only") == "true"
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	resp, err := s.accounts.ListStrategies(r.Context(), &accountv1.ListStrategiesRequest{
		NamePrefix: namePrefix,
		ActiveOnly: activeOnly,
		UserId:     uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	out := make([]strategyJSON, 0, len(resp.GetStrategies()))
	for _, st := range resp.GetStrategies() {
		out = append(out, protoStrategyToJSON(st, false))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getStrategy(w http.ResponseWriter, r *http.Request, id int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	resp, err := s.accounts.GetStrategy(r.Context(), &accountv1.GetStrategyRequest{StrategyId: id, UserId: uid})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, protoStrategyToJSON(resp.GetStrategy(), true))
}

func (s *server) archiveStrategy(w http.ResponseWriter, r *http.Request, id int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.accounts.ArchiveStrategy(r.Context(), &accountv1.ArchiveStrategyRequest{StrategyId: id, UserId: uid})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"archived": true})
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func protoStrategyToJSON(st *accountv1.StrategyEntry, includeCode bool) strategyJSON {
	if st == nil {
		return strategyJSON{}
	}
	j := strategyJSON{
		StrategyID:     st.GetStrategyId(),
		Name:           st.GetName(),
		Version:        st.GetVersion(),
		Description:    st.GetDescription(),
		Archived:       st.GetArchived(),
		CreatedAt:      st.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
		RuntimeVersion: st.GetRuntimeVersion(),
		RuntimeProfile: st.GetRuntimeProfile(),
	}
	if includeCode {
		j.Code = st.GetCode()
	}
	return j
}
