package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
	accountv1 "github.com/hushine-tech/core-service/gen/accountv1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeControlPanelStrategyProxy struct {
	controlpanelv1.ControlPanelServiceClient
	runReq      *strategyv1.RunStrategyRequest
	runResp     *strategyv1.RunStrategyResponse
	runErr      error
	statusReq   *strategyv1.GetStrategyStatusRequest
	statusErr   error
	stopReq     *strategyv1.StopStrategyRequest
	stopErr     error
	previewReq  *strategyv1.PreviewRunStrategyRequest
	previewResp *strategyv1.PreviewRunStrategyResponse
	previewErr  error
}

func (f *fakeControlPanelStrategyProxy) RunStrategy(ctx context.Context, in *strategyv1.RunStrategyRequest, _ ...grpc.CallOption) (*strategyv1.RunStrategyResponse, error) {
	f.runReq = in
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
	return &strategyv1.StopStrategyResponse{Stopped: true}, nil
}

func (f *fakeControlPanelStrategyProxy) PreviewRunStrategy(ctx context.Context, in *strategyv1.PreviewRunStrategyRequest, _ ...grpc.CallOption) (*strategyv1.PreviewRunStrategyResponse, error) {
	f.previewReq = in
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
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_missing","start_time_ms":0,"end_time_ms":0}`)), 42)
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
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_quota","start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, expected non-2xx; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRunStrategy_HostedEmptyEndpointUsesProxy: hosted runtime
// routeability is runtime_id + RuntimeChannel owner, not grpc_endpoint.
func TestRunStrategy_HostedEmptyEndpointUsesProxy(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{RuntimeID: "rt_empty", Source: "hosted", GRPCEndpoint: ""},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_empty","start_time_ms":0,"end_time_ms":0}`)), 42)
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
		"/api/accounts/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt_self","start_time_ms":1,"end_time_ms":2}`)), 42)
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
	accounts := &fakeSessionAccountsClient{accountEnvironment: 0}
	s := &server{
		accounts:     accounts,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt_debug","start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if accounts.lastGetAccountReq == nil || accounts.lastGetAccountReq.GetAccountId() != 7 || accounts.lastGetAccountReq.GetUserId() != 42 {
		t.Fatalf("GetAccount request = %+v, want account/user 7/42", accounts.lastGetAccountReq)
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
	accounts := &fakeSessionAccountsClient{accountEnvironment: 1}
	s := &server{
		accounts:     accounts,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy",
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
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"start_time_ms":1,"end_time_ms":2}`)), 42)
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
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"start_time_ms":1,"end_time_ms":2}`)), 42)
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
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_disabled","start_time_ms":0,"end_time_ms":0}`)), 42)
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
			RuntimeID:    "rt_self",
			Name:         "default",
			Source:       "self_hosted",
			GRPCEndpoint: "",
		},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_self","start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.runReq == nil {
		t.Fatal("control-panel proxy RunStrategy was not called")
	}
	if proxy.runReq.GetAccountId() != 7 || proxy.runReq.GetUserId() != 42 {
		t.Fatalf("proxy request = %+v", proxy.runReq)
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
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_self","start_time_ms":1,"end_time_ms":2}`)), 42)
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
	accounts := &fakeSessionAccountsClient{
		getSessionResp: &accountv1.GetSessionResponse{Session: &accountv1.StrategySessionEntry{
			SessionId:     "sess_abc",
			UserId:        42,
			RuntimeId:     "rt_self",
			Status:        "running",
			BarsProcessed: 12,
		}},
	}
	s := &server{
		accounts:     accounts,
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet,
		"/api/strategy-sessions/sess_abc", nil), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.statusReq == nil || proxy.statusReq.GetRuntimeId() != "rt_self" {
		t.Fatalf("proxy status req = %+v, want rt_self", proxy.statusReq)
	}
	if resolver.resolveByIDCalls != 1 || resolver.ensureCalls != 0 {
		t.Fatalf("resolver calls resolve=%d ensure=%d, want 1/0", resolver.resolveByIDCalls, resolver.ensureCalls)
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
	accounts := &fakeSessionAccountsClient{
		getSessionResp: &accountv1.GetSessionResponse{Session: &accountv1.StrategySessionEntry{
			SessionId:     "sess_abc",
			UserId:        42,
			RuntimeId:     "rt_session",
			Status:        "running",
			BarsProcessed: 9,
		}},
	}
	s := &server{
		accounts:     accounts,
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

func TestStatus_UnboundSessionFailsExplicitly(t *testing.T) {
	resolver := &fakeResolver{}
	accounts := &fakeSessionAccountsClient{
		getSessionResp: &accountv1.GetSessionResponse{Session: &accountv1.StrategySessionEntry{
			SessionId: "sess_unbound",
			UserId:    42,
		}},
	}
	s := &server{
		accounts:     accounts,
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
			RuntimeID:    "rt_hosted",
			Name:         "default",
			Source:       "hosted",
			GRPCEndpoint: "",
			CallerToken:  "compat-debug-only",
		},
	}
	s := &server{
		controlPanel: resolver,
		cpRuntime:    proxy,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_hosted","start_time_ms":1,"end_time_ms":2}`)), 42)
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
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"start_time_ms":0,"end_time_ms":0}`)), 42)
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
		accounts:     &fakeSessionAccountsClient{},
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
	accounts := &fakeSessionAccountsClient{
		getSessionResp: &accountv1.GetSessionResponse{Session: &accountv1.StrategySessionEntry{
			SessionId: "sess_abc",
			UserId:    42,
			Status:    "recoverable",
			RuntimeId: "rt_ended",
		}},
	}
	s := &server{
		accounts:     accounts,
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
	accounts := &fakeSessionAccountsClient{
		getSessionResp: &accountv1.GetSessionResponse{Session: &accountv1.StrategySessionEntry{
			SessionId: "sess_abc",
			UserId:    42,
			RuntimeId: "rt_session",
		}},
	}
	s := &server{
		accounts:     accounts,
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
