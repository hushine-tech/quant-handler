package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	accountv1 "github.com/hushine-tech/core-service/gen/accountv1"
	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// callerTokenObservingClient records the metadata it receives so tests
// can assert that the handler attached x-caller-token to outgoing calls.
type callerTokenObservingClient struct {
	strategyv1.StrategyServiceClient
	receivedMD  metadata.MD
	runReq      *strategyv1.RunStrategyRequest
	runResp     *strategyv1.RunStrategyResponse
	stopReq     *strategyv1.StopStrategyRequest
	stopResp    *strategyv1.StopStrategyResponse
	statusReq   *strategyv1.GetStrategyStatusRequest
	statusResp  *strategyv1.GetStrategyStatusResponse
	previewReq  *strategyv1.PreviewRunStrategyRequest
	previewResp *strategyv1.PreviewRunStrategyResponse
	err         error
}

type fakeControlPanelStrategyProxy struct {
	controlpanelv1.ControlPanelServiceClient
	runReq     *strategyv1.RunStrategyRequest
	runResp    *strategyv1.RunStrategyResponse
	runErr     error
	statusReq  *strategyv1.GetStrategyStatusRequest
	statusErr  error
	stopReq    *strategyv1.StopStrategyRequest
	stopErr    error
	previewReq *strategyv1.PreviewRunStrategyRequest
	previewErr error
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
	return &strategyv1.PreviewRunStrategyResponse{Profile: "backtest", Supported: true, Ok: true}, nil
}

func (f *callerTokenObservingClient) RunStrategy(ctx context.Context, in *strategyv1.RunStrategyRequest, _ ...grpc.CallOption) (*strategyv1.RunStrategyResponse, error) {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		f.receivedMD = md
	}
	f.runReq = in
	if f.err != nil {
		return nil, f.err
	}
	if f.runResp == nil {
		return &strategyv1.RunStrategyResponse{SessionId: "sess_xyz"}, nil
	}
	return f.runResp, nil
}

func (f *callerTokenObservingClient) PreviewRunStrategy(ctx context.Context, in *strategyv1.PreviewRunStrategyRequest, _ ...grpc.CallOption) (*strategyv1.PreviewRunStrategyResponse, error) {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		f.receivedMD = md
	}
	f.previewReq = in
	if f.err != nil {
		return nil, f.err
	}
	if f.previewResp == nil {
		return &strategyv1.PreviewRunStrategyResponse{Profile: "live"}, nil
	}
	return f.previewResp, nil
}

func (f *callerTokenObservingClient) GetStrategyStatus(ctx context.Context, in *strategyv1.GetStrategyStatusRequest, _ ...grpc.CallOption) (*strategyv1.GetStrategyStatusResponse, error) {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		f.receivedMD = md
	}
	f.statusReq = in
	if f.err != nil {
		return nil, f.err
	}
	if f.statusResp == nil {
		return &strategyv1.GetStrategyStatusResponse{Status: "running"}, nil
	}
	return f.statusResp, nil
}

func (f *callerTokenObservingClient) StopStrategy(ctx context.Context, in *strategyv1.StopStrategyRequest, _ ...grpc.CallOption) (*strategyv1.StopStrategyResponse, error) {
	if md, ok := metadata.FromOutgoingContext(ctx); ok {
		f.receivedMD = md
	}
	f.stopReq = in
	if f.err != nil {
		return nil, f.err
	}
	if f.stopResp == nil {
		return &strategyv1.StopStrategyResponse{Stopped: true}, nil
	}
	return f.stopResp, nil
}

// stubRuntimeDialer implements the same surface as runtimeDialer but
// returns a caller-controlled strategy client without opening a real
// gRPC connection.
type stubRuntimeDialer struct {
	cli         strategyv1.StrategyServiceClient
	dialErr     error
	gotEndpoint string
}

// To make stubRuntimeDialer drop into the runtimeDialer slot we monkey-
// patch the server's runtimeDialer field to a real dialer whose dial map
// already contains a fake. Simpler: build the server with a real dialer
// preloaded with a fake conn — but we can't synthesize *grpc.ClientConn
// from a stub client. Instead, we replace handler dispatch by setting
// server.strategy directly when feature flag is OFF, and for feature-on
// tests we use an in-process bufconn server.
//
// For section 6 we exercise the routing decision rather than the dial
// wire. Tests that need to assert "RunStrategy was called" use the
// flag-off path with `server.strategy = fake`. Tests that need to
// assert "control-panel resolved correctly" use the flag-on path with
// the fakeResolver and a stub dialer that injects the strategy client
// directly. We add `server.runtimeDialerOverride` for the latter.

// ─────────────────────────────────────────────────────────────────────
// 6.4 fail-closed semantics: feature flag on, control panel rejects
// ─────────────────────────────────────────────────────────────────────

// TestRunStrategy_FlagOn_ControlPanelNotFound surfaces the gRPC NotFound
// from ResolveRuntimeRouteByID as HTTP 404 and never falls back to the
// legacy fixed strategy-service even though it is configured.
func TestRunStrategy_FlagOn_ControlPanelNotFound(t *testing.T) {
	legacy := &callerTokenObservingClient{}
	resolver := &fakeResolver{
		resolveByIDErr: status.Error(codes.NotFound, "runtime not found"),
	}
	s := &server{
		strategy:                 legacy,
		controlPanel:             resolver,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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
	if legacy.runReq != nil {
		t.Error("legacy strategy.RunStrategy was called; expected fail-closed (no silent fallback)")
	}
}

// TestRunStrategy_FlagOn_ControlPanelResourceExhausted maps quota errors
// to HTTP 502 (Unavailable mapping in grpcToHTTP for non-typed) — the
// exact code matters less than "no silent fallback".
func TestRunStrategy_FlagOn_ControlPanelResourceExhausted(t *testing.T) {
	legacy := &callerTokenObservingClient{}
	resolver := &fakeResolver{
		resolveByIDErr: status.Error(codes.ResourceExhausted, "plan caps hosted at 1"),
	}
	s := &server{
		strategy:                 legacy,
		controlPanel:             resolver,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_quota","start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code == http.StatusOK {
		t.Fatalf("status = %d, expected non-2xx; body=%s", rec.Code, rec.Body.String())
	}
	if legacy.runReq != nil {
		t.Error("legacy strategy.RunStrategy was called; quota errors must surface")
	}
}

// TestRunStrategy_FlagOn_HostedEmptyEndpointUsesProxy: hosted runtime
// routeability is runtime_id + RuntimeChannel owner, not grpc_endpoint.
func TestRunStrategy_FlagOn_HostedEmptyEndpointUsesProxy(t *testing.T) {
	legacy := &callerTokenObservingClient{}
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{RuntimeID: "rt_empty", Source: "hosted", GRPCEndpoint: ""},
	}
	s := &server{
		strategy:                 legacy,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_empty","start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if legacy.runReq != nil {
		t.Fatal("legacy/direct strategy client was called for hosted route")
	}
	if proxy.runReq == nil || proxy.runReq.GetRuntimeId() != "rt_empty" {
		t.Fatalf("proxy RunStrategy = %+v, want runtime rt_empty", proxy.runReq)
	}
}

func TestRunStrategy_FlagOn_ExplicitRuntimeIDRoutesByID(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_self",
			Name:      "self",
			Source:    "self_hosted",
		},
	}
	s := &server{
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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

func TestRunStrategy_FlagOn_ModeZeroDebuggerRuntimeRoutesAsDebugger(t *testing.T) {
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
	accounts := &fakeSessionAccountsClient{accountMode: 0}
	s := &server{
		accounts:                 accounts,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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
	if resolver.resolveByIDCalls != 1 || resolver.gotRole != "debugger" || resolver.gotMode != 0 {
		t.Fatalf("ResolveRouteByID calls=%d role=%q mode=%d, want 1/debugger/0", resolver.resolveByIDCalls, resolver.gotRole, resolver.gotMode)
	}
	if proxy.runReq == nil || proxy.runReq.GetRuntimeId() != "rt_debug" {
		t.Fatalf("proxy RunStrategy = %+v, want runtime rt_debug", proxy.runReq)
	}
}

func TestRunStrategy_FlagOn_ModeTwoAlwaysRoutesAsExecutor(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	resolver := &fakeResolver{
		resolveByIDResp: controlpanel.Route{
			RuntimeID: "rt_exec",
			Name:      "executor-runtime",
			Source:    "self_hosted",
		},
	}
	accounts := &fakeSessionAccountsClient{accountMode: 2}
	s := &server{
		accounts:                 accounts,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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
		t.Fatalf("GetRuntime calls = %d, want 0 for mode=2 executor-only policy", resolver.getRuntimeCalls)
	}
	if resolver.resolveByIDCalls != 1 || resolver.gotRole != "executor" || resolver.gotMode != 2 {
		t.Fatalf("ResolveRouteByID calls=%d role=%q mode=%d, want 1/executor/2", resolver.resolveByIDCalls, resolver.gotRole, resolver.gotMode)
	}
}

func TestRunStrategy_FlagOn_OmittedRuntimeIDWithMultipleRuntimesRequiresSelection(t *testing.T) {
	legacy := &callerTokenObservingClient{}
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
		strategy:                 legacy,
		controlPanel:             resolver,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"start_time_ms":1,"end_time_ms":2}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 0 || resolver.ensureCalls != 0 || legacy.runReq != nil {
		t.Fatalf("ambiguous selection should not route: resolveByID=%d ensure=%d legacy=%v", resolver.resolveByIDCalls, resolver.ensureCalls, legacy.runReq != nil)
	}
}

func TestRunStrategy_FlagOn_SingleRuntimeDoesNotAutoSelect(t *testing.T) {
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
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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

// TestRunStrategy_FlagOn_ControlPanelDisabled: feature flag on but the
// resolver itself is the Disabled() fallback (operator forgot to wire
// dependencies.control_panel_service_grpc). Surface 503 — clear signal
// to operator vs 502 dial errors.
func TestRunStrategy_FlagOn_ControlPanelDisabled(t *testing.T) {
	legacy := &callerTokenObservingClient{}
	s := &server{
		strategy:                 legacy,
		controlPanel:             controlpanel.Disabled(),
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"runtime_id":"rt_disabled","start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if legacy.runReq != nil {
		t.Error("legacy strategy.RunStrategy was called; Disabled() must fail closed")
	}
}

func TestRunStrategy_FlagOn_SelfHostedUsesControlPanelProxy(t *testing.T) {
	legacy := &callerTokenObservingClient{}
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
		strategy:                 legacy,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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
	if legacy.runReq != nil {
		t.Fatal("legacy/direct strategy client was called for self-hosted route")
	}
	if resolver.ensureCalls != 0 {
		t.Fatalf("EnsureHostedRuntime calls = %d, want 0 for self-hosted route", resolver.ensureCalls)
	}
}

func TestRunStrategy_FlagOn_SelfHostedStreamDropSurfacesError(t *testing.T) {
	legacy := &callerTokenObservingClient{}
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
		strategy:                 legacy,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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
	if legacy.runReq != nil {
		t.Fatal("legacy/direct strategy client was called after self-hosted stream drop")
	}
}

func TestStatus_FlagOn_RuntimeOfflineSurfacesProxyError(t *testing.T) {
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
		accounts:                 accounts,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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

func TestStatus_FlagOn_UsesSessionRuntimeID(t *testing.T) {
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
		accounts:                 accounts,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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

func TestStatus_FlagOn_UnboundSessionFailsExplicitly(t *testing.T) {
	resolver := &fakeResolver{}
	accounts := &fakeSessionAccountsClient{
		getSessionResp: &accountv1.GetSessionResponse{Session: &accountv1.StrategySessionEntry{
			SessionId: "sess_legacy",
			UserId:    42,
		}},
	}
	s := &server{
		accounts:                 accounts,
		controlPanel:             resolver,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/strategy-sessions/sess_legacy", nil), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.resolveByIDCalls != 0 {
		t.Fatalf("ResolveRouteByID calls = %d, want 0", resolver.resolveByIDCalls)
	}
}

func TestRunStrategy_FlagOn_HostedUsesControlPanelProxy(t *testing.T) {
	legacy := &callerTokenObservingClient{}
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
		strategy:                 legacy,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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
	if legacy.runReq != nil {
		t.Fatal("legacy/direct strategy client was called for hosted route")
	}
	if resolver.ensureCalls != 0 {
		t.Fatalf("EnsureHostedRuntime calls = %d, want 0 for healthy hosted route", resolver.ensureCalls)
	}
}

func TestResolveStrategyRuntime_ModeEnsureRequiresRuntimeID(t *testing.T) {
	resolver := &fakeResolver{}
	s := &server{
		controlPanel:  resolver,
		runtimeDialer: newRuntimeDialer(),
	}
	rec := httptest.NewRecorder()
	cli, _, _ := s.resolveStrategyRuntime(context.Background(), rec, 42, modeEnsure, "", defaultStrategyRoutePolicy())
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
		controlPanel:  resolver,
		runtimeDialer: newRuntimeDialer(),
	}
	rec := httptest.NewRecorder()
	cli, _, _ := s.resolveStrategyRuntime(context.Background(), rec, 42, modeEnsure, "rt_unhealthy", defaultStrategyRoutePolicy())
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

// ─────────────────────────────────────────────────────────────────────
// 6.1 / 6.3 routing: feature flag off keeps legacy path unchanged
// ─────────────────────────────────────────────────────────────────────

// TestRunStrategy_FlagOff_UsesLegacyClient: feature flag off → handler
// calls s.strategy.RunStrategy as before. Asserts no regression for
// pre-cutover deployments.
func TestRunStrategy_FlagOff_UsesLegacyClient(t *testing.T) {
	legacy := &callerTokenObservingClient{}
	resolver := &fakeResolver{}
	s := &server{
		strategy:                 legacy,
		controlPanel:             resolver,
		controlPanelRouteFeature: false,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/run-strategy", bytes.NewBufferString(`{"start_time_ms":0,"end_time_ms":0}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if legacy.runReq == nil {
		t.Fatal("legacy strategy.RunStrategy was NOT called; feature-off should use legacy path")
	}
	if legacy.runReq.GetUserId() != 42 {
		t.Errorf("user_id = %d, want 42", legacy.runReq.GetUserId())
	}
	if resolver.ensureCalls != 0 {
		t.Errorf("EnsureHostedRuntime calls = %d, want 0 (feature flag off)", resolver.ensureCalls)
	}
	// Caller token MUST NOT be attached on the legacy path.
	if vals := legacy.receivedMD.Get(callerTokenMetadataKey); len(vals) != 0 {
		t.Errorf("legacy path should not attach caller-token metadata; got %v", vals)
	}
}

// TestStop_FlagOff_UsesLegacyClient mirrors the run path for stop.
func TestStop_FlagOff_UsesLegacyClient(t *testing.T) {
	legacy := &callerTokenObservingClient{}
	s := &server{
		strategy:                 legacy,
		controlPanel:             controlpanel.Disabled(),
		controlPanelRouteFeature: false,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/strategy-sessions/sess_abc/stop", bytes.NewBufferString(`{}`)), 42)
	rec := httptest.NewRecorder()
	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if legacy.stopReq == nil {
		t.Fatal("legacy StopStrategy was NOT called")
	}
	if legacy.stopReq.GetSessionId() != "sess_abc" {
		t.Errorf("session_id = %q, want sess_abc", legacy.stopReq.GetSessionId())
	}
}

// TestStop_FlagOn_UsesResolveNotEnsure: stop must NOT lazily provision
// a runtime — it goes through ResolveRoute (read-only). If the user's
// runtime is gone, the stop returns the gRPC error from control panel,
// not a fresh runtime.
func TestStop_FlagOn_UsesResolveNotEnsure(t *testing.T) {
	legacy := &callerTokenObservingClient{}
	resolver := &fakeResolver{
		err: status.Error(codes.NotFound, "no runtime"),
	}
	s := &server{
		strategy:                 legacy,
		accounts:                 &fakeSessionAccountsClient{},
		controlPanel:             resolver,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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
	if legacy.stopReq != nil {
		t.Error("legacy StopStrategy was called; fail-closed required")
	}
}

func TestStop_FlagOn_TerminalSessionDoesNotResolveRuntime(t *testing.T) {
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
		accounts:                 accounts,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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

func TestStop_FlagOn_UsesSessionRuntimeID(t *testing.T) {
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
		accounts:                 accounts,
		controlPanel:             resolver,
		cpRuntime:                proxy,
		controlPanelRouteFeature: true,
		runtimeDialer:            newRuntimeDialer(),
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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
