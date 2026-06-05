package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	accountv1 "github.com/hushine-tech/core-service/gen/accountv1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
)

// ── Request / Response types ─────────────────────────────────────────────────

type runStrategyRequest struct {
	StrategyPath string `json:"strategy_path"`
	Interval     string `json:"interval"`
	StartTimeMs  int64  `json:"start_time_ms"`
	EndTimeMs    int64  `json:"end_time_ms"`
	RuntimeID    string `json:"runtime_id"`
}

type stopStrategyRequest struct {
	StopAction string `json:"stop_action"`
}

type previewRunStrategyRequest struct {
	StrategyPath string `json:"strategy_path"`
	StartTimeMs  int64  `json:"start_time_ms"`
	EndTimeMs    int64  `json:"end_time_ms"`
	RuntimeID    string `json:"runtime_id"`
}

// Shape of the preview-run JSON response — mirrors PreviewRunStrategyResponse.
// Exposed so UI surfaces render readiness using exactly the same model the
// backend evaluator produces (pre_C3 gate 2: gateway must not re-derive
// readiness from wallet state).
type preflightInputKeyJSON struct {
	Market   string `json:"market"`
	Symbol   string `json:"symbol"`
	Interval string `json:"interval"`
}

type preflightFailureJSON struct {
	Kind     string                 `json:"kind"`
	Reason   string                 `json:"reason"`
	InputKey *preflightInputKeyJSON `json:"input_key,omitempty"`
}

type previewRunStrategyResponse struct {
	Profile              string                    `json:"profile"`
	Supported            bool                      `json:"supported"`
	Ok                   bool                      `json:"ok"`
	Failures             []preflightFailureJSON    `json:"failures"`
	RequiredStreams      []streamKeyJSON           `json:"required_streams"`
	Inputs               []streamKeyJSON           `json:"inputs"`
	DeclaredInputs       []streamKeyJSON           `json:"declared_inputs"`
	OrderTargets         []strategyOrderTargetJSON `json:"order_targets"`
	DeclaredOrderTargets []strategyOrderTargetJSON `json:"declared_order_targets"`
	RequiredRoutes       []strategyRouteJSON       `json:"required_routes"`
}

type strategyOrderTargetJSON struct {
	Exchange string `json:"exchange"`
	Market   string `json:"market"`
	Symbol   string `json:"symbol"`
}

type strategyRouteJSON struct {
	Exchange string `json:"exchange"`
	Market   string `json:"market"`
}

func (s *server) strategyRoutePolicyForAccount(ctx context.Context, w http.ResponseWriter, userID int64, accountID int64, runtimeID string) (strategyRoutePolicy, bool) {
	environment := int32(0)
	if s.accounts != nil {
		resp, err := s.accounts.GetAccount(ctx, &accountv1.GetAccountRequest{
			AccountId: accountID,
			UserId:    userID,
		})
		if err != nil {
			code, msg := grpcToHTTP(err)
			writeErr(w, code, msg)
			return strategyRoutePolicy{}, false
		}
		account := resp.GetAccount()
		if account == nil {
			writeErr(w, http.StatusNotFound, "account not found")
			return strategyRoutePolicy{}, false
		}
		environment = account.GetEnvironment()
	}
	return s.strategyRoutePolicyForSelectedRuntime(ctx, w, userID, runtimeID, environment)
}

func (s *server) strategyRoutePolicyForSelectedRuntime(ctx context.Context, w http.ResponseWriter, userID int64, runtimeID string, environment int32) (strategyRoutePolicy, bool) {
	policy := strategyRoutePolicyForEnvironment(environment)
	if environment != 0 || runtimeID == "" {
		return policy, true
	}
	runtime, err := s.controlPanel.GetRuntime(ctx, userID, runtimeID)
	if err != nil {
		if errors.Is(err, controlpanel.ErrNotConfigured) {
			writeErr(w, http.StatusServiceUnavailable, "control-panel-service not configured")
			return strategyRoutePolicy{}, false
		}
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return strategyRoutePolicy{}, false
	}
	role := strings.ToLower(strings.TrimSpace(runtime.Role))
	if role == "" {
		role = "executor"
	}
	policy.role = role
	return policy, true
}

// ── Handlers ─────────────────────────────────────────────────────────────────

func (s *server) handleRunStrategy(w http.ResponseWriter, r *http.Request, accountID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body runStrategyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// strategy_path 可以为空：Phase 2 中 strategy-service 会通过 GetActiveStrategy 获取 DB 存储的策略
	interval := body.Interval
	if interval == "" {
		interval = "1m"
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	runtimeID := strings.TrimSpace(body.RuntimeID)
	if runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime selection required")
		return
	}
	policy, ok := s.strategyRoutePolicyForAccount(r.Context(), w, uid, accountID, runtimeID)
	if !ok {
		return
	}
	cli, _, ok := s.strategyClient(r.Context(), w, uid, routeEnsure, runtimeID, policy)
	if !ok {
		return
	}
	resp, err := cli.RunStrategy(r.Context(), &strategyv1.RunStrategyRequest{
		AccountId:    accountID,
		StrategyPath: body.StrategyPath,
		Interval:     interval,
		StartTimeMs:  body.StartTimeMs,
		EndTimeMs:    body.EndTimeMs,
		UserId:       uid,
		RuntimeId:    runtimeID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": resp.GetSessionId(),
	})
}

func (s *server) handlePreviewRunStrategy(w http.ResponseWriter, r *http.Request, accountID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body previewRunStrategyRequest
	// Optional body: empty body is valid (backtest with zero time range → the
	// evaluator returns an INVALID_REQUEST failure, which is the right signal).
	_ = json.NewDecoder(r.Body).Decode(&body)

	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	runtimeID := strings.TrimSpace(body.RuntimeID)
	if runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime selection required")
		return
	}
	policy, ok := s.strategyRoutePolicyForAccount(r.Context(), w, uid, accountID, runtimeID)
	if !ok {
		return
	}
	cli, _, ok := s.strategyClient(r.Context(), w, uid, routeEnsure, runtimeID, policy)
	if !ok {
		return
	}
	resp, err := cli.PreviewRunStrategy(r.Context(), &strategyv1.PreviewRunStrategyRequest{
		AccountId:    accountID,
		StrategyPath: body.StrategyPath,
		StartTimeMs:  body.StartTimeMs,
		EndTimeMs:    body.EndTimeMs,
		UserId:       uid,
		RuntimeId:    runtimeID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	failures := make([]preflightFailureJSON, 0, len(resp.GetFailures()))
	for _, f := range resp.GetFailures() {
		j := preflightFailureJSON{
			Kind:   f.GetKind(),
			Reason: f.GetReason(),
		}
		if k := f.GetInputKey(); k != nil && (k.GetMarket() != "" || k.GetSymbol() != "" || k.GetInterval() != "") {
			j.InputKey = &preflightInputKeyJSON{
				Market:   marketDataMarketToStrategyMarket(k.GetMarket()),
				Symbol:   k.GetSymbol(),
				Interval: k.GetInterval(),
			}
		}
		failures = append(failures, j)
	}
	required := make([]streamKeyJSON, 0, len(resp.GetRequiredStreams()))
	for _, b := range resp.GetRequiredStreams() {
		required = append(required, liveBindingToStreamKeyJSON(b))
	}
	declared := liveBindingsToStreamKeys(resp.GetDeclaredInputs())
	orderTargets := orderTargetBindingsToJSON(resp.GetDeclaredOrderTargets())
	routes := routeBindingsToJSON(resp.GetRequiredRoutes())

	writeJSON(w, http.StatusOK, previewRunStrategyResponse{
		Profile:              resp.GetProfile(),
		Supported:            resp.GetSupported(),
		Ok:                   resp.GetOk(),
		Failures:             failures,
		RequiredStreams:      required,
		Inputs:               declared,
		DeclaredInputs:       declared,
		OrderTargets:         orderTargets,
		DeclaredOrderTargets: orderTargets,
		RequiredRoutes:       routes,
	})
}

func liveBindingsToStreamKeys(bindings []*strategyv1.LiveStreamBinding) []streamKeyJSON {
	out := make([]streamKeyJSON, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, liveBindingToStreamKeyJSON(b))
	}
	return out
}

func liveBindingToStreamKeyJSON(b *strategyv1.LiveStreamBinding) streamKeyJSON {
	if b == nil {
		return streamKeyJSON{}
	}
	exchange := strings.ToLower(strings.TrimSpace(b.GetExchange()))
	if exchange == "" {
		exchange = "binance"
	}
	kind := strings.ToLower(strings.TrimSpace(b.GetKind()))
	if kind == "" {
		kind = "kline"
	}
	return streamKeyJSON{
		Exchange: exchange,
		Market:   marketDataMarketToStrategyMarket(b.GetMarket()),
		Kind:     kind,
		Symbol:   strings.ToUpper(strings.TrimSpace(b.GetSymbol())),
		Interval: strings.TrimSpace(b.GetInterval()),
	}
}

func orderTargetBindingsToJSON(bindings []*strategyv1.StrategyOrderTargetBinding) []strategyOrderTargetJSON {
	out := make([]strategyOrderTargetJSON, 0, len(bindings))
	for _, b := range bindings {
		if b == nil {
			continue
		}
		out = append(out, strategyOrderTargetJSON{
			Exchange: strings.ToLower(strings.TrimSpace(b.GetExchange())),
			Market:   marketDataMarketToStrategyMarket(b.GetMarket()),
			Symbol:   strings.ToUpper(strings.TrimSpace(b.GetSymbol())),
		})
	}
	return out
}

func routeBindingsToJSON(bindings []*strategyv1.StrategyRouteBinding) []strategyRouteJSON {
	out := make([]strategyRouteJSON, 0, len(bindings))
	for _, b := range bindings {
		if b == nil {
			continue
		}
		out = append(out, strategyRouteJSON{
			Exchange: strings.ToLower(strings.TrimSpace(b.GetExchange())),
			Market:   marketDataMarketToStrategyMarket(b.GetMarket()),
		})
	}
	return out
}

func (s *server) handleStrategySession(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/strategy-sessions/")
	// Strip /stop suffix if present
	isStop := false
	if strings.HasSuffix(sessionID, "/stop") {
		sessionID = strings.TrimSuffix(sessionID, "/stop")
		isStop = true
	}
	sessionID = strings.Trim(sessionID, "/")
	if sessionID == "" {
		writeErr(w, http.StatusBadRequest, "session_id is required")
		return
	}

	if isStop && r.Method == http.MethodPost {
		s.handleStopStrategy(w, r, sessionID)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	// Status / Stop go through ResolveRoute (read-only): the session
	// already exists somewhere; we MUST NOT lazily provision a new
	// runtime as a side effect of looking up a session. In D1's
	// hosted-only single-default-runtime model, session ownership is
	// implicit — the user's default runtime owns all their sessions.
	session, ok := s.loadSessionForRuntimeRoute(w, r, sessionID, uid)
	if !ok {
		return
	}
	runtimeID := session.GetRuntimeId()
	if strategySessionTerminal(session.GetStatus()) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":         session.GetStatus(),
			"bars_processed": session.GetBarsProcessed(),
			"error":          session.GetError(),
			"runtime_id":     runtimeID,
			"runtime_source": session.GetRuntimeSource(),
			"runtime_name":   session.GetRuntimeName(),
		})
		return
	}
	policy, ok := s.strategyRoutePolicyForSelectedRuntime(r.Context(), w, uid, runtimeID, session.GetEnvironment())
	if !ok {
		return
	}
	cli, selectedRuntimeID, ok := s.strategyClient(r.Context(), w, uid, routeResolve, runtimeID, policy)
	if !ok {
		return
	}
	if runtimeID == "" {
		runtimeID = selectedRuntimeID
	}
	resp, err := cli.GetStrategyStatus(r.Context(), &strategyv1.GetStrategyStatusRequest{
		SessionId: sessionID,
		UserId:    uid,
		RuntimeId: runtimeID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         resp.GetStatus(),
		"bars_processed": resp.GetBarsProcessed(),
		"error":          resp.GetError(),
		"runtime_id":     runtimeID,
	})
}

func (s *server) handleStopStrategy(w http.ResponseWriter, r *http.Request, sessionID string) {
	var body stopStrategyRequest
	_ = json.NewDecoder(r.Body).Decode(&body) // optional body; defaults handled below
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	action, err := normalizeStopAction(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	session, ok := s.loadSessionForRuntimeRoute(w, r, sessionID, uid)
	if !ok {
		return
	}
	if strategySessionTerminal(session.GetStatus()) {
		writeJSON(w, http.StatusOK, map[string]any{
			"stopped": true,
			"status":  session.GetStatus(),
		})
		return
	}
	runtimeID := session.GetRuntimeId()
	policy, ok := s.strategyRoutePolicyForSelectedRuntime(r.Context(), w, uid, runtimeID, session.GetEnvironment())
	if !ok {
		return
	}
	cli, selectedRuntimeID, ok := s.strategyClient(r.Context(), w, uid, routeResolve, runtimeID, policy)
	if !ok {
		return
	}
	if runtimeID == "" {
		runtimeID = selectedRuntimeID
	}
	resp, err := cli.StopStrategy(r.Context(), &strategyv1.StopStrategyRequest{
		SessionId:  sessionID,
		StopAction: action,
		UserId:     uid,
		RuntimeId:  runtimeID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	out := map[string]any{
		"stopped":     resp.GetStopped(),
		"stop_action": action.String(),
		"runtime_id":  runtimeID,
	}
	if s.accounts != nil {
		if current, err := s.accounts.GetSession(r.Context(), &accountv1.GetSessionRequest{SessionId: sessionID, UserId: uid}); err == nil {
			if session := current.GetSession(); session != nil {
				out["status"] = session.GetStatus()
				out["error"] = session.GetError()
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) loadSessionForRuntimeRoute(w http.ResponseWriter, r *http.Request, sessionID string, userID int64) (*accountv1.StrategySessionEntry, bool) {
	if s.accounts == nil {
		writeErr(w, http.StatusServiceUnavailable, "core-service not configured")
		return nil, false
	}
	resp, err := s.accounts.GetSession(r.Context(), &accountv1.GetSessionRequest{
		SessionId: sessionID,
		UserId:    userID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return nil, false
	}
	session := resp.GetSession()
	if session == nil || session.GetRuntimeId() == "" {
		writeErr(w, http.StatusConflict, "session is not bound to a runtime")
		return nil, false
	}
	return session, true
}

func strategySessionTerminal(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "finished", "stopped", "failed", "stop_failed", "recoverable":
		return true
	default:
		return false
	}
}

func normalizeStopAction(body stopStrategyRequest) (strategyv1.StopAction, error) {
	raw := strings.ToUpper(strings.TrimSpace(body.StopAction))
	if raw == "" {
		return strategyv1.StopAction_STOP_ACTION_STOP_ONLY, nil
	}

	switch raw {
	case "STOP_ACTION_CANCEL", "CANCEL":
		return strategyv1.StopAction_STOP_ACTION_CANCEL, nil
	case "STOP_ACTION_FINISH", "FINISH":
		return strategyv1.StopAction_STOP_ACTION_FINISH, nil
	case "STOP_ACTION_STOP_ONLY", "STOP_ONLY":
		return strategyv1.StopAction_STOP_ACTION_STOP_ONLY, nil
	case "STOP_ACTION_STOP_AND_CLOSE_POSITIONS", "STOP_AND_CLOSE_POSITIONS":
		return strategyv1.StopAction_STOP_ACTION_STOP_AND_CLOSE_POSITIONS, nil
	default:
		return strategyv1.StopAction_STOP_ACTION_UNSPECIFIED, errors.New("invalid stop_action")
	}
}
