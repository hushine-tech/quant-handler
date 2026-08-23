package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	previewRunStrategyRPCTimeout = 15 * time.Second
	runStrategyRPCTimeout        = 30 * time.Second
	statusStrategyRPCTimeout     = 3 * time.Second
	maxStrategySourceBytes       = 1 << 20
)

// ── Request / Response types ─────────────────────────────────────────────────

type runStrategyRequest struct {
	StrategyPath    string  `json:"strategy_path"`
	Interval        string  `json:"interval"`
	StartTimeMs     int64   `json:"start_time_ms"`
	EndTimeMs       int64   `json:"end_time_ms"`
	RuntimeID       string  `json:"runtime_id"`
	MaxLossClosePct float64 `json:"max_loss_close_pct"`
	Leverage        float64 `json:"leverage"`
}

type stopStrategyRequest struct {
	StopAction  string `json:"stop_action"`
	OperationID string `json:"operation_id"`
}

type previewRunStrategyRequest struct {
	StrategyPath    string  `json:"strategy_path"`
	StartTimeMs     int64   `json:"start_time_ms"`
	EndTimeMs       int64   `json:"end_time_ms"`
	RuntimeID       string  `json:"runtime_id"`
	MaxLossClosePct float64 `json:"max_loss_close_pct"`
	Leverage        float64 `json:"leverage"`
}

type validateStrategySourceRequest struct {
	RuntimeID string `json:"runtime_id"`
	Source    string `json:"source"`
}

type strategyValidationIssueJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Module  string `json:"module"`
	Line    int32  `json:"line"`
	Symbol  string `json:"symbol"`
}

type runtimeDependencyProfileJSON struct {
	SchemaVersion         uint32   `json:"schema_version"`
	ProfileName           string   `json:"profile_name"`
	ProfileVersion        string   `json:"profile_version"`
	ContractSHA256        string   `json:"contract_sha256"`
	HostedPython          string   `json:"hosted_python"`
	PublicImportRoots     []string `json:"public_import_roots"`
	StrategyServiceCommit string   `json:"strategy_service_commit"`
	StrategyLibraryCommit string   `json:"strategy_library_commit"`
	ImageBuildID          string   `json:"image_build_id"`
}

type validateStrategySourceResponse struct {
	OK             bool                          `json:"ok"`
	Issues         []strategyValidationIssueJSON `json:"issues"`
	RuntimeProfile *runtimeDependencyProfileJSON `json:"runtime_profile"`
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
	Kind          string                 `json:"kind"`
	Reason        string                 `json:"reason"`
	InputKey      *preflightInputKeyJSON `json:"input_key,omitempty"`
	Code          string                 `json:"code,omitempty"`
	Route         string                 `json:"route,omitempty"`
	Exchange      int32                  `json:"exchange,omitempty"`
	ExchangeLabel string                 `json:"exchange_label,omitempty"`
	Market        int32                  `json:"market,omitempty"`
	MarketLabel   string                 `json:"market_label,omitempty"`
	Symbol        string                 `json:"symbol,omitempty"`
	VenueID       int64                  `json:"venue_id,omitempty"`
	FilterType    string                 `json:"filter_type,omitempty"`
	Environment   int32                  `json:"environment"`
	Retryable     bool                   `json:"retryable"`
	Source        string                 `json:"source,omitempty"`
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
	RiskControls         riskControlsJSON          `json:"risk_controls"`
}

type runStrategyResponseJSON struct {
	SessionID      string                             `json:"session_id"`
	OK             bool                               `json:"ok"`
	Failures       []preflightFailureJSON             `json:"failures"`
	TargetResults  []strategyLeverageTargetResultJSON `json:"target_results"`
	Code           string                             `json:"code"`
	RollbackFailed bool                               `json:"rollback_failed"`
}

type riskControlsJSON struct {
	MaxLossClosePct    float64 `json:"max_loss_close_pct"`
	MaxLossCloseSource string  `json:"max_loss_close_source"`
	Leverage           float64 `json:"leverage"`
	LeverageSource     string  `json:"leverage_source"`
}

type strategyOrderTargetJSON struct {
	Exchange          string  `json:"exchange"`
	Market            string  `json:"market"`
	Symbol            string  `json:"symbol"`
	EffectiveLeverage uint32  `json:"effective_leverage,omitempty"`
	LeverageSource    string  `json:"leverage_source,omitempty"`
	CurrentLeverage   *uint32 `json:"current_leverage,omitempty"`
	ChangeRequired    bool    `json:"change_required"`
	VenueID           int64   `json:"venue_id,omitempty"`
	LeverageStatus    string  `json:"leverage_status,omitempty"`
}

type strategyLeverageTargetResultJSON struct {
	VenueID           int64   `json:"venue_id"`
	Exchange          int32   `json:"exchange"`
	ExchangeLabel     string  `json:"exchange_label,omitempty"`
	Market            int32   `json:"market"`
	MarketLabel       string  `json:"market_label,omitempty"`
	Symbol            string  `json:"symbol"`
	EffectiveLeverage uint32  `json:"effective_leverage"`
	LeverageSource    string  `json:"leverage_source"`
	PreviousLeverage  *uint32 `json:"previous_leverage,omitempty"`
	CurrentLeverage   *uint32 `json:"current_leverage,omitempty"`
	ConfirmedLeverage *uint32 `json:"confirmed_leverage,omitempty"`
	ChangeRequired    bool    `json:"change_required"`
	Status            string  `json:"status"`
	ErrorCode         string  `json:"error_code,omitempty"`
	ErrorMessage      string  `json:"error_message,omitempty"`
	Retryable         bool    `json:"retryable"`
}

type strategyRouteJSON struct {
	Exchange string `json:"exchange"`
	Market   string `json:"market"`
}

func (s *server) strategyRoutePolicyForPortfolio(ctx context.Context, w http.ResponseWriter, userID int64, portfolioID int64, runtimeID string) (strategyRoutePolicy, bool) {
	environment := int32(0)
	if s.portfolios != nil {
		resp, err := s.portfolios.GetPortfolio(ctx, &portfoliov1.GetPortfolioRequest{
			PortfolioId: portfolioID,
			UserId:      userID,
		})
		if err != nil {
			code, msg := grpcToHTTP(err)
			writeErr(w, code, msg)
			return strategyRoutePolicy{}, false
		}
		portfolio := resp.GetPortfolio()
		if portfolio == nil {
			writeErr(w, http.StatusNotFound, "portfolio not found")
			return strategyRoutePolicy{}, false
		}
		environment = portfolio.GetEnvironment()
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

func (s *server) handleRunStrategy(w http.ResponseWriter, r *http.Request, portfolioID int64) {
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
	policy, ok := s.strategyRoutePolicyForPortfolio(r.Context(), w, uid, portfolioID, runtimeID)
	if !ok {
		return
	}
	cli, _, ok := s.strategyClient(r.Context(), w, uid, routeEnsure, runtimeID, policy)
	if !ok {
		return
	}
	previewCtx, previewCancel := context.WithTimeout(r.Context(), previewRunStrategyRPCTimeout)
	preview, err := cli.PreviewRunStrategy(previewCtx, &strategyv1.PreviewRunStrategyRequest{
		PortfolioId:     portfolioID,
		StrategyPath:    body.StrategyPath,
		StartTimeMs:     body.StartTimeMs,
		EndTimeMs:       body.EndTimeMs,
		UserId:          uid,
		RuntimeId:       runtimeID,
		MaxLossClosePct: body.MaxLossClosePct,
	})
	previewCancel()
	if err != nil {
		if writeRuntimeDependencyError(w, err) {
			return
		}
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	if preview == nil {
		writeErr(w, http.StatusBadGateway, "runtime returned an empty strategy preview")
		return
	}
	if !preview.GetOk() {
		failures := preview.GetFailures()
		if len(failures) == 0 {
			writeStructuredError(w, http.StatusPreconditionFailed, structuredErrorJSON{
				Error: "strategy preflight failed", Code: "STRATEGY_PREFLIGHT_FAILED",
				Environment: int32(policy.environment), Source: "strategy-service",
			})
			return
		}
		failure := failures[0]
		failureDetails := make([]preflightFailureJSON, 0, len(failures))
		for _, candidate := range failures {
			failureDetails = append(failureDetails, preflightFailureToJSON(candidate))
		}
		code := strings.TrimSpace(failure.GetCode())
		if code == "" {
			code = "STRATEGY_PREFLIGHT_FAILED"
		}
		source := strings.TrimSpace(failure.GetSource())
		if source == "" {
			source = "strategy-service"
		}
		writeStructuredError(w, http.StatusPreconditionFailed, structuredErrorJSON{
			Error:       failure.GetReason(),
			Code:        code,
			Route:       preflightFailureRoute(failure),
			Exchange:    failure.GetExchange(),
			Market:      failure.GetMarket(),
			VenueID:     failure.GetVenueId(),
			Symbol:      failure.GetSymbol(),
			FilterType:  failure.GetFilterType(),
			Environment: failure.GetEnvironment(),
			Retryable:   failure.GetRetryable(),
			Source:      source,
			Failures:    failureDetails,
		})
		return
	}
	if previewDeclaresSpot(preview) && !s.requireSpotStartCapability(r.Context(), w, int32(policy.environment)) {
		return
	}
	rpcCtx, cancel := context.WithTimeout(r.Context(), runStrategyRPCTimeout)
	defer cancel()
	resp, err := cli.RunStrategy(rpcCtx, &strategyv1.RunStrategyRequest{
		PortfolioId:     portfolioID,
		StrategyPath:    body.StrategyPath,
		Interval:        interval,
		StartTimeMs:     body.StartTimeMs,
		EndTimeMs:       body.EndTimeMs,
		UserId:          uid,
		RuntimeId:       runtimeID,
		MaxLossClosePct: body.MaxLossClosePct,
	})
	if err != nil {
		if writeRuntimeDependencyError(w, err) {
			return
		}
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	failures := make([]preflightFailureJSON, 0, len(resp.GetFailures()))
	for _, failure := range resp.GetFailures() {
		failures = append(failures, preflightFailureToJSON(failure))
	}
	writeJSON(w, http.StatusOK, runStrategyResponseJSON{
		SessionID: resp.GetSessionId(), OK: resp.GetOk(), Failures: failures,
		TargetResults: strategyLeverageTargetResultsToJSON(resp.GetTargetResults()),
		Code:          resp.GetCode(), RollbackFailed: resp.GetRollbackFailed(),
	})
}

func (s *server) handleValidateStrategySource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body validateStrategySourceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	runtimeID := strings.TrimSpace(body.RuntimeID)
	if runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	if strings.TrimSpace(body.Source) == "" {
		writeErr(w, http.StatusBadRequest, "source is required")
		return
	}
	if len([]byte(body.Source)) > maxStrategySourceBytes {
		writeErr(w, http.StatusBadRequest, "source exceeds 1 MiB limit")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	policy, ok := s.strategyRoutePolicyForSelectedRuntime(r.Context(), w, uid, runtimeID, 0)
	if !ok {
		return
	}
	cli, selectedRuntimeID, ok := s.strategyClient(r.Context(), w, uid, routeEnsure, runtimeID, policy)
	if !ok {
		return
	}
	rpcCtx, cancel := context.WithTimeout(r.Context(), previewRunStrategyRPCTimeout)
	defer cancel()
	resp, err := cli.ValidateStrategySource(rpcCtx, &strategyv1.ValidateStrategySourceRequest{
		Source:    body.Source,
		UserId:    uid,
		RuntimeId: selectedRuntimeID,
	})
	if err != nil {
		if writeRuntimeDependencyError(w, err) {
			return
		}
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	issues := make([]strategyValidationIssueJSON, 0, len(resp.GetIssues()))
	for _, issue := range resp.GetIssues() {
		if issue == nil {
			continue
		}
		issues = append(issues, strategyValidationIssueJSON{
			Code: issue.GetCode(), Message: issue.GetMessage(), Module: issue.GetModule(), Line: issue.GetLine(), Symbol: issue.GetSymbol(),
		})
	}
	writeJSON(w, http.StatusOK, validateStrategySourceResponse{
		OK:             resp.GetOk(),
		Issues:         issues,
		RuntimeProfile: runtimeDependencyProfileToJSON(resp.GetRuntimeProfile()),
	})
}

func runtimeDependencyProfileToJSON(profile *strategyv1.RuntimeDependencyProfile) *runtimeDependencyProfileJSON {
	if profile == nil {
		return nil
	}
	return &runtimeDependencyProfileJSON{
		SchemaVersion:         profile.GetSchemaVersion(),
		ProfileName:           profile.GetProfileName(),
		ProfileVersion:        profile.GetProfileVersion(),
		ContractSHA256:        profile.GetContractSha256(),
		HostedPython:          profile.GetHostedPython(),
		PublicImportRoots:     append([]string(nil), profile.GetPublicImportRoots()...),
		StrategyServiceCommit: profile.GetStrategyServiceCommit(),
		StrategyLibraryCommit: profile.GetStrategyLibraryCommit(),
		ImageBuildID:          profile.GetImageBuildId(),
	}
}

func (s *server) handlePreviewRunStrategy(w http.ResponseWriter, r *http.Request, portfolioID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body previewRunStrategyRequest
	// Optional body: empty body is valid (backtest with zero time range → the
	// evaluator returns an INVALID_REQUEST failure, which is the right signal).
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
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
	policy, ok := s.strategyRoutePolicyForPortfolio(r.Context(), w, uid, portfolioID, runtimeID)
	if !ok {
		return
	}
	cli, _, ok := s.strategyClient(r.Context(), w, uid, routeEnsure, runtimeID, policy)
	if !ok {
		return
	}
	rpcCtx, cancel := context.WithTimeout(r.Context(), previewRunStrategyRPCTimeout)
	defer cancel()
	resp, err := cli.PreviewRunStrategy(rpcCtx, &strategyv1.PreviewRunStrategyRequest{
		PortfolioId:     portfolioID,
		StrategyPath:    body.StrategyPath,
		StartTimeMs:     body.StartTimeMs,
		EndTimeMs:       body.EndTimeMs,
		UserId:          uid,
		RuntimeId:       runtimeID,
		MaxLossClosePct: body.MaxLossClosePct,
	})
	if err != nil {
		if writeRuntimeDependencyError(w, err) {
			return
		}
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	if resp.GetOk() && previewDeclaresSpot(resp) && !s.requireSpotStartCapability(r.Context(), w, int32(policy.environment)) {
		return
	}

	failures := make([]preflightFailureJSON, 0, len(resp.GetFailures()))
	for _, f := range resp.GetFailures() {
		failures = append(failures, preflightFailureToJSON(f))
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
		RiskControls: riskControlsJSON{
			MaxLossClosePct:    resp.GetRiskControls().GetMaxLossClosePct(),
			MaxLossCloseSource: resp.GetRiskControls().GetMaxLossCloseSource(),
			Leverage:           resp.GetRiskControls().GetLeverage(),
			LeverageSource:     resp.GetRiskControls().GetLeverageSource(),
		},
	})
}

func preflightFailureToJSON(failure *strategyv1.PreflightFailureProto) preflightFailureJSON {
	if failure == nil {
		return preflightFailureJSON{}
	}
	exchangeLabel := orderExchangeLabel(failure.GetExchange())
	if exchangeLabel == "unknown" {
		exchangeLabel = ""
	}
	marketLabel := orderMarketLabel(failure.GetMarket())
	if marketLabel == "unknown" {
		marketLabel = ""
	}
	out := preflightFailureJSON{
		Kind:          failure.GetKind(),
		Reason:        failure.GetReason(),
		Code:          failure.GetCode(),
		Route:         preflightFailureRoute(failure),
		Exchange:      failure.GetExchange(),
		ExchangeLabel: exchangeLabel,
		Market:        failure.GetMarket(),
		MarketLabel:   marketLabel,
		Symbol:        failure.GetSymbol(),
		VenueID:       failure.GetVenueId(),
		FilterType:    failure.GetFilterType(),
		Environment:   failure.GetEnvironment(),
		Retryable:     failure.GetRetryable(),
		Source:        failure.GetSource(),
	}
	if key := failure.GetInputKey(); key != nil && (key.GetMarket() != "" || key.GetSymbol() != "" || key.GetInterval() != "") {
		out.InputKey = &preflightInputKeyJSON{
			Market: marketDataMarketToStrategyMarket(key.GetMarket()), Symbol: key.GetSymbol(), Interval: key.GetInterval(),
		}
	}
	return out
}

func preflightFailureRoute(failure *strategyv1.PreflightFailureProto) string {
	if failure == nil {
		return ""
	}
	exchange := orderExchangeLabel(failure.GetExchange())
	market := orderMarketLabel(failure.GetMarket())
	if exchange == "unknown" || market == "unknown" {
		return ""
	}
	return exchange + "/" + market
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
			Exchange:          strings.ToLower(strings.TrimSpace(b.GetExchange())),
			Market:            marketDataMarketToStrategyMarket(b.GetMarket()),
			Symbol:            strings.ToUpper(strings.TrimSpace(b.GetSymbol())),
			EffectiveLeverage: b.GetEffectiveLeverage(),
			LeverageSource:    b.GetLeverageSource(),
			CurrentLeverage:   cloneUint32(b.CurrentLeverage),
			ChangeRequired:    b.GetChangeRequired(),
			VenueID:           b.GetVenueId(),
			LeverageStatus:    b.GetLeverageStatus(),
		})
	}
	return out
}

func strategyLeverageTargetResultsToJSON(results []*strategyv1.StrategyLeverageTargetResult) []strategyLeverageTargetResultJSON {
	out := make([]strategyLeverageTargetResultJSON, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		exchangeLabel := orderExchangeLabel(result.GetExchange())
		if exchangeLabel == "unknown" {
			exchangeLabel = ""
		}
		marketLabel := orderMarketLabel(result.GetMarket())
		if marketLabel == "unknown" {
			marketLabel = ""
		}
		out = append(out, strategyLeverageTargetResultJSON{
			VenueID: result.GetVenueId(), Exchange: result.GetExchange(), ExchangeLabel: exchangeLabel,
			Market: result.GetMarket(), MarketLabel: marketLabel, Symbol: strings.ToUpper(strings.TrimSpace(result.GetSymbol())),
			EffectiveLeverage: result.GetEffectiveLeverage(), LeverageSource: result.GetLeverageSource(),
			PreviousLeverage: cloneUint32(result.PreviousLeverage), CurrentLeverage: cloneUint32(result.CurrentLeverage),
			ConfirmedLeverage: cloneUint32(result.ConfirmedLeverage), ChangeRequired: result.GetChangeRequired(),
			Status: result.GetStatus(), ErrorCode: result.GetErrorCode(), ErrorMessage: result.GetErrorMessage(), Retryable: result.GetRetryable(),
		})
	}
	return out
}

func cloneUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
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
		writeSessionStatusJSON(w, session, runtimeID, "")
		return
	}
	if session.GetEnvironment() == 0 {
		writeSessionStatusJSON(w, session, runtimeID, "")
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
	rpcCtx, cancel := context.WithTimeout(r.Context(), statusStrategyRPCTimeout)
	defer cancel()
	resp, err := cli.GetStrategyStatus(rpcCtx, &strategyv1.GetStrategyStatusRequest{
		SessionId: sessionID,
		UserId:    uid,
		RuntimeId: runtimeID,
	})
	if err != nil {
		if shouldServePersistedSessionStatus(err) {
			_, msg := grpcToHTTPRuntime(err)
			writeSessionStatusJSON(w, session, runtimeID, msg)
			return
		}
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

func shouldServePersistedSessionStatus(err error) bool {
	switch status.Code(err) {
	case codes.DeadlineExceeded, codes.Unavailable:
		return true
	default:
		return false
	}
}

func writeSessionStatusJSON(w http.ResponseWriter, session *portfoliov1.StrategySessionEntry, runtimeID string, refreshErr string) {
	payload := map[string]any{
		"status":         session.GetStatus(),
		"bars_processed": session.GetBarsProcessed(),
		"error":          session.GetError(),
		"runtime_id":     runtimeID,
		"runtime_source": session.GetRuntimeSource(),
		"runtime_name":   session.GetRuntimeName(),
	}
	if refreshErr != "" {
		payload["status_stale"] = true
		payload["status_refresh_error"] = refreshErr
	}
	writeJSON(w, http.StatusOK, payload)
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
		SessionId:   sessionID,
		StopAction:  action,
		OperationId: strings.TrimSpace(body.OperationID),
		UserId:      uid,
		RuntimeId:   runtimeID,
	})
	if err != nil {
		if reason, ok := s.markRecoverableForStaleRuntimeStop(r.Context(), session, runtimeID, err); ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"stopped":     true,
				"stop_action": action.String(),
				"runtime_id":  runtimeID,
				"status":      "recoverable",
				"error":       reason,
			})
			return
		}
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	if !resp.GetStopped() && action != strategyv1.StopAction_STOP_ACTION_CANCEL &&
		strings.TrimSpace(resp.GetStatus()) == "" && strings.TrimSpace(resp.GetCode()) == "" && len(resp.GetTargetResults()) == 0 {
		if reason, ok := s.markRecoverableForRejectedRuntimeStop(r.Context(), session, runtimeID); ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"stopped":     true,
				"stop_action": action.String(),
				"runtime_id":  runtimeID,
				"status":      "recoverable",
				"error":       reason,
			})
			return
		}
	}
	out := map[string]any{
		"stopped":     resp.GetStopped(),
		"stop_action": action.String(),
		"runtime_id":  runtimeID,
	}
	if value := strings.TrimSpace(resp.GetStatus()); value != "" {
		out["status"] = value
	}
	if value := strings.TrimSpace(resp.GetCode()); value != "" {
		out["code"] = value
	}
	if value := strings.TrimSpace(resp.GetReconciliationRunId()); value != "" {
		out["reconciliation_run_id"] = value
	}
	if value := strings.TrimSpace(resp.GetOperationId()); value != "" {
		out["operation_id"] = value
	}
	if targets := resp.GetTargetResults(); len(targets) > 0 {
		results := make([]map[string]any, 0, len(targets))
		for _, target := range targets {
			if target == nil {
				continue
			}
			results = append(results, map[string]any{
				"exchange":       target.GetExchange(),
				"exchange_label": orderExchangeLabel(target.GetExchange()),
				"market":         target.GetMarket(),
				"market_label":   orderMarketLabel(target.GetMarket()),
				"symbol":         target.GetSymbol(),
				"status":         target.GetStatus(),
				"code":           target.GetCode(),
				"message":        target.GetMessage(),
			})
		}
		out["target_results"] = results
	}
	if s.portfolios != nil {
		if current, err := s.portfolios.GetSession(r.Context(), &portfoliov1.GetSessionRequest{SessionId: sessionID, UserId: uid}); err == nil {
			if session := current.GetSession(); session != nil {
				if _, exists := out["status"]; !exists && strings.TrimSpace(session.GetStatus()) != "" {
					out["status"] = session.GetStatus()
				}
				if strings.TrimSpace(session.GetError()) != "" {
					out["error"] = session.GetError()
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) markRecoverableForStaleRuntimeStop(ctx context.Context, session *portfoliov1.StrategySessionEntry, runtimeID string, stopErr error) (string, bool) {
	if s.portfolios == nil || session == nil || !isStaleRuntimeStopError(stopErr) {
		return "", false
	}
	reason := "stop_recovered:runtime_session_missing: runtime no longer owns this session; marked recoverable"
	if msg := strings.TrimSpace(status.Convert(stopErr).Message()); msg != "" {
		reason += ": " + msg
	}
	return reason, s.markSessionRecoverableForStop(ctx, session, runtimeID, reason)
}

func (s *server) markRecoverableForRejectedRuntimeStop(ctx context.Context, session *portfoliov1.StrategySessionEntry, runtimeID string) (string, bool) {
	if s.portfolios == nil || session == nil {
		return "", false
	}
	reason := "stop_recovered:runtime_stop_not_accepted: runtime returned stopped=false without a terminal DB update; marked recoverable"
	return reason, s.markSessionRecoverableForStop(ctx, session, runtimeID, reason)
}

func (s *server) markSessionRecoverableForStop(ctx context.Context, session *portfoliov1.StrategySessionEntry, runtimeID string, reason string) bool {
	if runtimeID == "" {
		runtimeID = session.GetRuntimeId()
	}
	updateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := s.portfolios.UpdateSession(updateCtx, &portfoliov1.UpdateSessionRequest{
		SessionId:     session.GetSessionId(),
		Status:        "recoverable",
		BarsProcessed: session.GetBarsProcessed(),
		Error:         reason,
		RuntimeId:     runtimeID,
	})
	return err == nil
}

func isStaleRuntimeStopError(err error) bool {
	code := status.Code(err)
	if code == codes.NotFound {
		return true
	}
	if code != codes.FailedPrecondition && code != codes.Unavailable {
		return false
	}
	msg := strings.ToLower(status.Convert(err).Message())
	return strings.Contains(msg, "session") && strings.Contains(msg, "not found") ||
		strings.Contains(msg, "runtime already ended")
}

func (s *server) loadSessionForRuntimeRoute(w http.ResponseWriter, r *http.Request, sessionID string, userID int64) (*portfoliov1.StrategySessionEntry, bool) {
	if s.portfolios == nil {
		writeErr(w, http.StatusServiceUnavailable, "core-service not configured")
		return nil, false
	}
	resp, err := s.portfolios.GetSession(r.Context(), &portfoliov1.GetSessionRequest{
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
