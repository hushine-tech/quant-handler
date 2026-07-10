package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeControlPanelStrategyProxy struct {
	controlpanelv1.ControlPanelServiceClient
	runReq                  *strategyv1.RunStrategyRequest
	runResp                 *strategyv1.RunStrategyResponse
	runErr                  error
	statusReq               *strategyv1.GetStrategyStatusRequest
	statusErr               error
	statusBlockUntilContext bool
	statusDeadlineSet       bool
	statusDeadlineRemaining time.Duration
	stopReq                 *strategyv1.StopStrategyRequest
	stopResp                *strategyv1.StopStrategyResponse
	stopErr                 error
	previewReq              *strategyv1.PreviewRunStrategyRequest
	previewResp             *strategyv1.PreviewRunStrategyResponse
	previewErr              error

	runDeadlineSet       bool
	runDeadlineRemaining time.Duration
	previewDeadlineSet   bool
	previewDeadlineUntil time.Duration
}

func (f *fakeControlPanelStrategyProxy) RunStrategy(ctx context.Context, in *strategyv1.RunStrategyRequest, _ ...grpc.CallOption) (*strategyv1.RunStrategyResponse, error) {
	f.runReq = in
	if deadline, ok := ctx.Deadline(); ok {
		f.runDeadlineSet = true
		f.runDeadlineRemaining = time.Until(deadline)
	}
	if f.runErr != nil {
		return nil, f.runErr
	}
	if f.runResp != nil {
		return f.runResp, nil
	}
	return &strategyv1.RunStrategyResponse{SessionId: "selfhosted-sess"}, nil
}

func (f *fakeControlPanelStrategyProxy) GetStrategyStatus(ctx context.Context, in *strategyv1.GetStrategyStatusRequest, _ ...grpc.CallOption) (*strategyv1.GetStrategyStatusResponse, error) {
	f.statusReq = in
	if deadline, ok := ctx.Deadline(); ok {
		f.statusDeadlineSet = true
		f.statusDeadlineRemaining = time.Until(deadline)
	}
	if f.statusBlockUntilContext {
		<-ctx.Done()
		return nil, status.Error(codes.DeadlineExceeded, ctx.Err().Error())
	}
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	return &strategyv1.GetStrategyStatusResponse{Status: "running"}, nil
}

func (f *fakeControlPanelStrategyProxy) StopStrategy(ctx context.Context, in *strategyv1.StopStrategyRequest, _ ...grpc.CallOption) (*strategyv1.StopStrategyResponse, error) {
	f.stopReq = in
	if f.stopErr != nil {
		return nil, f.stopErr
	}
	if f.stopResp != nil {
		return f.stopResp, nil
	}
	return &strategyv1.StopStrategyResponse{Stopped: true}, nil
}

func (f *fakeControlPanelStrategyProxy) PreviewRunStrategy(ctx context.Context, in *strategyv1.PreviewRunStrategyRequest, _ ...grpc.CallOption) (*strategyv1.PreviewRunStrategyResponse, error) {
	f.previewReq = in
	if deadline, ok := ctx.Deadline(); ok {
		f.previewDeadlineSet = true
		f.previewDeadlineUntil = time.Until(deadline)
	}
	if f.previewErr != nil {
		return nil, f.previewErr
	}
	if f.previewResp != nil {
		return f.previewResp, nil
	}
	return &strategyv1.PreviewRunStrategyResponse{Profile: "backtest", Supported: true, Ok: true}, nil
}

// TestRunStrategy_ControlPanelNotFound surfaces the gRPC NotFound from
// ResolveRuntimeRouteByID as HTTP 404.
func TestRunStrategy_ControlPanelNotFound(t *testing.T) {
	resolver := &fakeResolver{
		resolveByIDErr: status.Error(codes.NotFound, "runtime not found"),
	}
	s := &server{
		controlPanel: resolver,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_missing","start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 1 || resolver.ensureCalls != 0 {
		t.Errorf("route calls: resolveByID=%d ensure=%d, want 1/0", resolver.resolveByIDCalls, resolver.ensureCalls)
	}
}

// TestRunStrategy_ControlPanelResourceExhausted maps quota errors
// to HTTP 502 (Unavailable mapping in grpcToHTTP for non-typed) — the
// exact code matters less than "no silent fallback".
func TestRunStrategy_ControlPanelResourceExhausted(t *testing.T) {
	resolver := &fakeResolver{
		resolveByIDErr: status.Error(codes.ResourceExhausted, "plan caps hosted at 1"),
	}
	s := &server{
		controlPanel: resolver,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_quota","start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, expected non-2xx; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRunStrategy_HostedRouteUsesProxy: hosted runtime routeability is
// runtime_id + RuntimeChannel owner.
func TestRunStrategy_HostedRouteUsesProxy(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{RuntimeID: "rt_empty", Source: "hosted"},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_empty","start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.runReq == nil || proxy.runReq.GetRuntimeId() != "rt_empty" {
		t.Fatalf("proxy RunStrategy = %+v, want runtime rt_empty", proxy.runReq)
	}
}

func TestRunStrategy_ExplicitRuntimeIDRoutesByID(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_self",
			Name:      "self",
			Source:    "self_hosted",
		},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt_self","start_time_ms":1,"end_time_ms":2,"leverage":5}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 1 || resolver.gotRuntimeID != "rt_self" {
		t.Fatalf("ResolveRouteByID calls=%d runtime=%q, want 1/rt_self", resolver.resolveByIDCalls, resolver.gotRuntimeID)
	}
	if proxy.runReq == nil {
		t.Fatal("proxy RunStrategy was not called")
	}
	if proxy.runReq.GetRuntimeId() != "rt_self" {
		t.Fatalf("proxy RunStrategy runtime_id = %q", proxy.runReq.GetRuntimeId())
	}
	if proxy.runReq.GetLeverage() != 5 {
		t.Fatalf("proxy RunStrategy leverage = %v, want 5", proxy.runReq.GetLeverage())
	}
}

func TestRunStrategy_BacktestDebuggerRuntimeRoutesAsDebugger(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		runtimeResp: controlpanel.Runtime{
			RuntimeID: "rt_debug",
			Role:      "debugger",
			Status:    "active",
		},
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_debug",
			Name:      "debugger-runtime",
			Source:    "self_hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{portfolioEnvironment: 0}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt_debug","start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if portfolios.lastGetPortfolioReq == nil || portfolios.lastGetPortfolioReq.GetPortfolioId() != 7 || portfolios.lastGetPortfolioReq.GetUserId() != 42 {
		t.Fatalf("GetPortfolio request = %+v, want portfolio/user 7/42", portfolios.lastGetPortfolioReq)
	}
	if resolver.getRuntimeCalls != 1 || resolver.gotRuntimeID != "rt_debug" {
		t.Fatalf("GetRuntime calls=%d runtime=%q, want 1/rt_debug", resolver.getRuntimeCalls, resolver.gotRuntimeID)
	}
	if resolver.resolveByIDCalls != 1 || resolver.gotRole != "debugger" || resolver.gotEnvironment != 0 {
		t.Fatalf("ResolveRouteByID calls=%d role=%q environment=%d, want 1/debugger/0", resolver.resolveByIDCalls, resolver.gotRole, resolver.gotEnvironment)
	}
	if proxy.runReq == nil || proxy.runReq.GetRuntimeId() != "rt_debug" {
		t.Fatalf("proxy RunStrategy = %+v, want runtime rt_debug", proxy.runReq)
	}
}

func TestRunStrategy_DemoAlwaysRoutesAsExecutor(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_exec",
			Name:      "executor-runtime",
			Source:    "self_hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{portfolioEnvironment: 1}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt_exec","start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.getRuntimeCalls != 0 {
		t.Fatalf("GetRuntime calls = %d, want 0 for demo executor-only policy", resolver.getRuntimeCalls)
	}
	if resolver.resolveByIDCalls != 1 || resolver.gotRole != "executor" || resolver.gotEnvironment != 1 {
		t.Fatalf("ResolveRouteByID calls=%d role=%q environment=%d, want 1/executor/1", resolver.resolveByIDCalls, resolver.gotRole, resolver.gotEnvironment)
	}
}

func TestRunStrategy_OmittedRuntimeIDWithMultipleRuntimesRequiresSelection(t *testing.T) {
	resolver := &fakeResolver{
		runtimeList: controlpanel.RuntimeList{
			Runtimes: []controlpanel.Runtime{
				{RuntimeID: "rt-1", Status: "active", Source: "hosted"},
				{RuntimeID: "rt-2", Status: "active", Source: "self_hosted"},
			},
			Total: 2,
		},
	}
	s := &server{
		controlPanel: resolver,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 0 || resolver.ensureCalls != 0 {
		t.Fatalf("ambiguous selection should not route: resolveByID=%d ensure=%d", resolver.resolveByIDCalls, resolver.ensureCalls)
	}
}

func TestRunStrategy_SingleRuntimeDoesNotAutoSelect(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		runtimeList: controlpanel.RuntimeList{
			Runtimes: []controlpanel.Runtime{{RuntimeID: "rt_only", Status: "active", Source: "self_hosted"}},
			Total:    1,
		},
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_only",
			Name:      "default",
			Source:    "self_hosted",
		},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.runReq != nil || resolver.listCalls != 0 || resolver.resolveByIDCalls != 0 {
		t.Fatalf("omitted runtime_id should not auto-select: proxy=%v list=%d resolveByID=%d", proxy.runReq != nil, resolver.listCalls, resolver.resolveByIDCalls)
	}
}

// TestRunStrategy_ControlPanelDisabled: control-panel resolver itself is the Disabled() fallback (operator forgot to wire
// dependencies.control_panel_service_grpc). Surface 503 — clear signal
// to operator vs 502 dial errors.
func TestRunStrategy_ControlPanelDisabled(t *testing.T) {
	s := &server{
		controlPanel: controlpanel.Disabled(),
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_disabled","start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestRunStrategy_SelfHostedUsesControlPanelProxy(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_self",
			Name:      "default",
			Source:    "self_hosted",
		},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_self","start_time_ms":1,"end_time_ms":2,"max_loss_close_pct":0.25}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.runReq == nil {
		t.Fatal("control-panel proxy RunStrategy was not called")
	}
	if proxy.runReq.GetPortfolioId() != 7 || proxy.runReq.GetUserId() != 42 {
		t.Fatalf("proxy request = %+v", proxy.runReq)
	}
	if got := proxy.runReq.GetMaxLossClosePct(); got != 0.25 {
		t.Fatalf("max_loss_close_pct = %v, want 0.25", got)
	}
	if resolver.ensureCalls != 0 {
		t.Fatalf("EnsureHostedRuntime calls = %d, want 0 for self-hosted route", resolver.ensureCalls)
	}
}

func TestRunStrategy_SelfHostedStreamDropSurfacesError(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		runErr: status.Error(codes.Unavailable, "runtime stream disconnected mid-call"),
	}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_self",
			Name:      "default",
			Source:    "self_hosted",
		},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_self","start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.runReq == nil {
		t.Fatal("control-panel proxy RunStrategy was not called")
	}
}

func TestStatus_RuntimeOfflineSurfacesProxyError(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		statusErr: status.Error(codes.Unavailable, "runtime stream disconnected"),
	}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_self",
			Name:      "default",
			Source:    "self_hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId:     "sess_abc",
			UserId:        42,
			RuntimeId:     "rt_self",
			Status:        "running",
			BarsProcessed: 12,
			Environment:   1,
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet,
		"/api/strategy-sessions/sess_abc", nil), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 stale fallback; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status             string `json:"status"`
		BarsProcessed      int32  `json:"bars_processed"`
		StatusStale        bool   `json:"status_stale"`
		StatusRefreshError string `json:"status_refresh_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "running" || body.BarsProcessed != 12 || !body.StatusStale {
		t.Fatalf("body = %+v, want persisted running status with stale marker", body)
	}
	if contains(body.StatusRefreshError, "runtime stream disconnected") || !contains(body.StatusRefreshError, "Runtime is temporarily unavailable") {
		t.Fatalf("status_refresh_error = %q, want friendly unavailable message", body.StatusRefreshError)
	}
	if proxy.statusReq == nil || proxy.statusReq.GetRuntimeId() != "rt_self" {
		t.Fatalf("proxy status req = %+v, want rt_self", proxy.statusReq)
	}
	if resolver.resolveByIDCalls != 1 || resolver.ensureCalls != 0 {
		t.Fatalf("resolver calls resolve=%d ensure=%d, want 1/0", resolver.resolveByIDCalls, resolver.ensureCalls)
	}
}

func TestStatus_BacktestUsesPersistedSessionWithoutRuntimeStatusRPC(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_backtest",
			Name:      "hosted",
			Source:    "hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId:     "sess_backtest",
			UserId:        42,
			RuntimeId:     "rt_backtest",
			Status:        "running",
			BarsProcessed: 2048,
			Environment:   0,
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/strategy-sessions/sess_backtest", nil), 42)
	rec := httptest.NewRecorder()

	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.statusReq != nil {
		t.Fatalf("GetStrategyStatus should not be called for backtest session: %+v", proxy.statusReq)
	}
	if resolver.resolveByIDCalls != 0 {
		t.Fatalf("ResolveRouteByID calls=%d, want 0 for persisted backtest status", resolver.resolveByIDCalls)
	}
	var body struct {
		Status        string `json:"status"`
		BarsProcessed int32  `json:"bars_processed"`
		StatusStale   bool   `json:"status_stale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "running" || body.BarsProcessed != 2048 || body.StatusStale {
		t.Fatalf("body = %+v, want persisted backtest status without stale marker", body)
	}
}

func TestStatus_UsesSessionRuntimeID(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_session",
			Name:      "default",
			Source:    "self_hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId:     "sess_abc",
			UserId:        42,
			RuntimeId:     "rt_session",
			Status:        "running",
			BarsProcessed: 9,
			Environment:   1,
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/strategy-sessions/sess_abc", nil), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 1 || resolver.gotRuntimeID != "rt_session" {
		t.Fatalf("ResolveRouteByID calls=%d runtime=%q, want 1/rt_session", resolver.resolveByIDCalls, resolver.gotRuntimeID)
	}
	if proxy.statusReq == nil || proxy.statusReq.GetRuntimeId() != "rt_session" {
		t.Fatalf("proxy status req = %+v, want rt_session", proxy.statusReq)
	}
}

func TestStatus_RuntimeStatusCallHasDeadline(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{statusBlockUntilContext: true}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_session",
			Name:      "default",
			Source:    "hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId:     "sess_abc",
			UserId:        42,
			RuntimeId:     "rt_session",
			Status:        "running",
			BarsProcessed: 9,
			Environment:   1,
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}

	start := time.Now()
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/strategy-sessions/sess_abc", nil), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 stale status fallback; body=%s", rec.Code, rec.Body.String())
	}
	if !proxy.statusDeadlineSet {
		t.Fatal("GetStrategyStatus downstream call had no deadline")
	}
	if proxy.statusDeadlineRemaining <= 0 || proxy.statusDeadlineRemaining > statusStrategyRPCTimeout {
		t.Fatalf("GetStrategyStatus deadline remaining = %v, want within %v", proxy.statusDeadlineRemaining, statusStrategyRPCTimeout)
	}
	if elapsed > statusStrategyRPCTimeout+time.Second {
		t.Fatalf("status call elapsed = %v, want bounded by %v", elapsed, statusStrategyRPCTimeout)
	}
	var body struct {
		Status             string `json:"status"`
		BarsProcessed      int64  `json:"bars_processed"`
		StatusStale        bool   `json:"status_stale"`
		StatusRefreshError string `json:"status_refresh_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "running" || body.BarsProcessed != 9 || !body.StatusStale || body.StatusRefreshError == "" {
		t.Fatalf("body = %+v, want stale persisted running status with refresh error", body)
	}
}

func TestStatus_UnboundSessionFailsExplicitly(t *testing.T) {
	resolver := &fakeResolver{}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId: "sess_unbound",
			UserId:    42,
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/strategy-sessions/sess_unbound", nil), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 0 {
		t.Fatalf("ResolveRouteByID calls = %d, want 0", resolver.resolveByIDCalls)
	}
}

func TestRunStrategy_HostedUsesControlPanelProxy(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_hosted",
			Name:      "default",
			Source:    "hosted",
		},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_hosted","start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.runReq == nil {
		t.Fatal("control-panel proxy RunStrategy was not called for hosted route")
	}
	if proxy.runReq.GetRuntimeId() != "rt_hosted" || proxy.runReq.GetUserId() != 42 {
		t.Fatalf("proxy request = %+v, want runtime/user rt_hosted/42", proxy.runReq)
	}
	if resolver.ensureCalls != 0 {
		t.Fatalf("EnsureHostedRuntime calls = %d, want 0 for healthy hosted route", resolver.ensureCalls)
	}
}

func TestRunStrategy_UsesRuntimeProxyDeadline(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_exec",
			Name:      "default",
			Source:    "hosted",
		},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_exec","start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !proxy.runDeadlineSet {
		t.Fatal("RunStrategy downstream call had no deadline")
	}
	if proxy.runDeadlineRemaining <= 0 || proxy.runDeadlineRemaining > runStrategyRPCTimeout {
		t.Fatalf("RunStrategy deadline remaining = %v, want within %v", proxy.runDeadlineRemaining, runStrategyRPCTimeout)
	}
}

func TestResolveStrategyRuntime_EnsureRequiresRuntimeID(t *testing.T) {
	resolver := &fakeResolver{}
	s := &server{
		controlPanel: resolver,
	}
	rec := httptest.NewRecorder()
	cli, _ := s.resolveStrategyRuntime(context.Background(), rec, 42, routeEnsure, "", defaultStrategyRoutePolicy())
	if cli != nil {
		t.Fatal("client returned without runtime_id")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 0 || resolver.ensureCalls != 0 {
		t.Fatalf("route calls: resolveByID=%d ensure=%d, want 0/0", resolver.resolveByIDCalls, resolver.ensureCalls)
	}
}

func TestResolveStrategyRuntime_RouteByIDErrorDoesNotProvisionHosted(t *testing.T) {
	resolver := &fakeResolver{
		resolveByIDErr: status.Error(codes.FailedPrecondition, "runtime unhealthy"),
	}
	s := &server{
		controlPanel: resolver,
	}
	rec := httptest.NewRecorder()
	cli, _ := s.resolveStrategyRuntime(context.Background(), rec, 42, routeEnsure, "rt_unhealthy", defaultStrategyRoutePolicy())
	if cli != nil {
		t.Fatal("client returned; route error must fail closed")
	}
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.ensureCalls != 0 {
		t.Fatalf("EnsureHostedRuntime calls = %d, want 0", resolver.ensureCalls)
	}
}

func TestRunStrategyWithoutRuntimeIDRejectsInsteadOfUsingLegacyClient(t *testing.T) {
	resolver := &fakeResolver{}
	s := &server{
		controlPanel: resolver,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/run-strategy", bytes.NewBufferString(`{"start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestStopWithoutRuntimeBindingRejectsInsteadOfUsingLegacyClient(t *testing.T) {
	s := &server{
		controlPanel: controlpanel.Disabled(),
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/strategy-sessions/sess_abc/stop", bytes.NewBufferString(`{}`)), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
}

// TestStop_UsesResolveNotEnsure: stop must NOT lazily provision
// a runtime — it goes through ResolveRoute (read-only). If the user's
// runtime is gone, the stop returns the gRPC error from control panel,
// not a fresh runtime.
func TestStop_UsesResolveNotEnsure(t *testing.T) {
	resolver := &fakeResolver{
		err: status.Error(codes.NotFound, "no runtime"),
	}
	s := &server{
		portfolios:     &fakeSessionPortfoliosClient{},
		controlPanel: resolver,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/strategy-sessions/sess_abc/stop", bytes.NewBufferString(`{}`)), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 1 {
		t.Errorf("ResolveRouteByID calls = %d, want 1", resolver.resolveByIDCalls)
	}
	if resolver.ensureCalls != 0 {
		t.Errorf("EnsureHostedRuntime calls = %d, want 0 (stop must NOT lazily provision)", resolver.ensureCalls)
	}
}

func TestStop_TerminalSessionDoesNotResolveRuntime(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		err: status.Error(codes.FailedPrecondition, "runtime ended"),
	}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId: "sess_abc",
			UserId:    42,
			Status:    "recoverable",
			RuntimeId: "rt_ended",
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/strategy-sessions/sess_abc/stop", bytes.NewBufferString(`{"stop_action":"FINISH"}`)), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 0 {
		t.Fatalf("ResolveRouteByID calls = %d, want 0", resolver.resolveByIDCalls)
	}
	if proxy.stopReq != nil {
		t.Fatalf("StopStrategy was called for terminal session: %+v", proxy.stopReq)
	}
}

func TestStop_UsesSessionRuntimeID(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_session",
			Name:      "default",
			Source:    "self_hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId: "sess_abc",
			UserId:    42,
			RuntimeId: "rt_session",
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/strategy-sessions/sess_abc/stop", bytes.NewBufferString(`{}`)), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 1 || resolver.gotRuntimeID != "rt_session" {
		t.Fatalf("ResolveRouteByID calls=%d runtime=%q, want 1/rt_session", resolver.resolveByIDCalls, resolver.gotRuntimeID)
	}
	if proxy.stopReq == nil || proxy.stopReq.GetRuntimeId() != "rt_session" {
		t.Fatalf("proxy stop req = %+v", proxy.stopReq)
	}
}

func TestStop_StaleRuntimeSessionMarksRecoverable(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		stopErr: status.Error(codes.NotFound, "session sess_abc not found"),
	}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_session",
			Name:      "default",
			Source:    "hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId:     "sess_abc",
			UserId:        42,
			Status:        "running",
			RuntimeId:     "rt_session",
			BarsProcessed: 17,
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/strategy-sessions/sess_abc/stop", bytes.NewBufferString(`{}`)), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if portfolios.lastUpdateSessionReq == nil {
		t.Fatal("UpdateSession was not called")
	}
	if got := portfolios.lastUpdateSessionReq.GetStatus(); got != "recoverable" {
		t.Fatalf("UpdateSession status = %q, want recoverable", got)
	}
	if got := portfolios.lastUpdateSessionReq.GetRuntimeId(); got != "rt_session" {
		t.Fatalf("UpdateSession runtime_id = %q, want rt_session", got)
	}
	if got := portfolios.lastUpdateSessionReq.GetBarsProcessed(); got != 17 {
		t.Fatalf("UpdateSession bars_processed = %d, want 17", got)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response json: %v; body=%s", err, rec.Body.String())
	}
	if stopped, _ := out["stopped"].(bool); !stopped {
		t.Fatalf("stopped = %v, want true; body=%s", out["stopped"], rec.Body.String())
	}
	if status, _ := out["status"].(string); status != "recoverable" {
		t.Fatalf("status = %q, want recoverable; body=%s", status, rec.Body.String())
	}
}

func TestStop_RuntimeRejectsWithoutErrorMarksRecoverable(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		stopResp: &strategyv1.StopStrategyResponse{Stopped: false},
	}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_session",
			Name:      "default",
			Source:    "hosted",
		},
	}
	portfolios := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId:     "sess_abc",
			UserId:        42,
			Status:        "running",
			RuntimeId:     "rt_session",
			BarsProcessed: 17,
		}},
	}
	s := &server{
		portfolios:     portfolios,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/strategy-sessions/sess_abc/stop", bytes.NewBufferString(`{}`)), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if portfolios.lastUpdateSessionReq == nil {
		t.Fatal("UpdateSession was not called")
	}
	if got := portfolios.lastUpdateSessionReq.GetStatus(); got != "recoverable" {
		t.Fatalf("UpdateSession status = %q, want recoverable", got)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response json: %v; body=%s", err, rec.Body.String())
	}
	if stopped, _ := out["stopped"].(bool); !stopped {
		t.Fatalf("stopped = %v, want true; body=%s", out["stopped"], rec.Body.String())
	}
	if status, _ := out["status"].(string); status != "recoverable" {
		t.Fatalf("status = %q, want recoverable; body=%s", status, rec.Body.String())
	}
}
