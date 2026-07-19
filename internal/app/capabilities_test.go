package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeProductCapabilitiesClient struct {
	portfoliov1.PortfolioServiceClient
	response    *portfoliov1.GetProductCapabilitiesResponse
	err         error
	calls       int
	environment int32
	session     *portfoliov1.StrategySessionEntry
}

func (f *fakeProductCapabilitiesClient) GetProductCapabilities(_ context.Context, _ *portfoliov1.GetProductCapabilitiesRequest, _ ...grpc.CallOption) (*portfoliov1.GetProductCapabilitiesResponse, error) {
	f.calls++
	return f.response, f.err
}

func (f *fakeProductCapabilitiesClient) GetPortfolio(_ context.Context, req *portfoliov1.GetPortfolioRequest, _ ...grpc.CallOption) (*portfoliov1.GetPortfolioResponse, error) {
	return &portfoliov1.GetPortfolioResponse{Portfolio: &portfoliov1.PortfolioRegistryEntry{
		PortfolioId: req.GetPortfolioId(), UserId: req.GetUserId(), Environment: f.environment,
	}}, nil
}

func (f *fakeProductCapabilitiesClient) GetSession(_ context.Context, _ *portfoliov1.GetSessionRequest, _ ...grpc.CallOption) (*portfoliov1.GetSessionResponse, error) {
	return &portfoliov1.GetSessionResponse{Session: f.session}, nil
}

func TestProductCapabilitiesProjectsCoreTruthExactly(t *testing.T) {
	states := []*portfoliov1.ProductCapabilityState{
		{Name: "backtest_spot_usdt", Configured: true, Effective: true},
		{Name: "demo_spot_usdt", Configured: true, Effective: false, Reason: "demo paused"},
		{Name: "live_spot_usdt", Configured: true, Effective: false, Reason: "SPOT_LIVE_ROLLOUT_GUARD"},
		{Name: "offline_spot_usdt", Configured: false, Effective: false, Reason: "SPOT_CAPABILITY_DISABLED"},
	}
	fake := &fakeProductCapabilitiesClient{response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: states}}
	s := &server{portfolios: fake}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/capabilities", nil), 42)
	rec := httptest.NewRecorder()

	s.handleProductCapabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Capabilities []productCapabilityJSON `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []productCapabilityJSON{
		{Name: "backtest_spot_usdt", Configured: true, Effective: true},
		{Name: "demo_spot_usdt", Configured: true, Effective: false, Reason: "demo paused"},
		{Name: "live_spot_usdt", Configured: true, Effective: false, Reason: "SPOT_LIVE_ROLLOUT_GUARD"},
		{Name: "offline_spot_usdt", Configured: false, Effective: false, Reason: "SPOT_CAPABILITY_DISABLED"},
	}
	if !reflect.DeepEqual(body.Capabilities, want) {
		t.Fatalf("capabilities=%#v want=%#v", body.Capabilities, want)
	}
	if fake.calls != 1 {
		t.Fatalf("GetProductCapabilities calls=%d want=1", fake.calls)
	}
}

func TestProductCapabilitiesAllFourDefaultFalseRemainFalse(t *testing.T) {
	states := []*portfoliov1.ProductCapabilityState{
		{Name: capabilityBacktestSpotUSDT, Reason: codeSpotCapabilityDisabled},
		{Name: capabilityDemoSpotUSDT, Reason: codeSpotCapabilityDisabled},
		{Name: capabilityOfflineSpotUSDT, Reason: codeSpotCapabilityDisabled},
		{Name: capabilityLiveSpotUSDT, Reason: codeSpotLiveRolloutGuard},
	}
	fake := &fakeProductCapabilitiesClient{response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: states}}
	s := &server{portfolios: fake}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/capabilities", nil), 42)
	rec := httptest.NewRecorder()

	s.handleProductCapabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Capabilities []productCapabilityJSON `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Capabilities) != 4 {
		t.Fatalf("capabilities=%#v", body.Capabilities)
	}
	for _, capability := range body.Capabilities {
		if capability.Configured || capability.Effective {
			t.Fatalf("default-false capability was upgraded: %#v", capability)
		}
	}
}

func TestProductCapabilitiesDoesNotInventMissingOrUpgradeFalseValues(t *testing.T) {
	fake := &fakeProductCapabilitiesClient{response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{
		{Name: "demo_spot_usdt", Configured: true, Effective: false, Reason: "disabled"},
	}}}
	s := &server{portfolios: fake}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/capabilities", nil), 42)
	rec := httptest.NewRecorder()

	s.handleProductCapabilities(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Capabilities []productCapabilityJSON `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []productCapabilityJSON{{Name: "demo_spot_usdt", Configured: true, Effective: false, Reason: "disabled"}}
	if !reflect.DeepEqual(body.Capabilities, want) {
		t.Fatalf("capabilities=%#v want exact core projection %#v", body.Capabilities, want)
	}
}

func TestProductCapabilitiesDiscoveryFailureIsServiceUnavailable(t *testing.T) {
	fake := &fakeProductCapabilitiesClient{err: status.Error(codes.Unavailable, "core unavailable")}
	s := &server{portfolios: fake}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/capabilities", nil), 42)
	rec := httptest.NewRecorder()

	s.handleProductCapabilities(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=503 body=%s", rec.Code, rec.Body.String())
	}
}

func TestProductCapabilitiesRejectsNonGET(t *testing.T) {
	s := &server{}
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/capabilities", nil), 42)
	rec := httptest.NewRecorder()

	s.handleProductCapabilities(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want=405", rec.Code)
	}
}

func TestPreviewRunStrategyRejectsDisabledBacktestSpot(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{
		environment: 0,
		response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: capabilityBacktestSpotUSDT, Configured: false, Effective: false, Reason: "SPOT_CAPABILITY_DISABLED",
		}}},
	}
	proxy := &fakeControlPanelStrategyProxy{previewResp: spotPreviewResponse()}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/preview-run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-spot"}`)), 42)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	assertSpotGuardResponse(t, rec, http.StatusPreconditionFailed, "SPOT_CAPABILITY_DISABLED", 0)
	if portfolio.calls != 1 {
		t.Fatalf("capability calls=%d want=1", portfolio.calls)
	}
}

func TestPreviewRunStrategyAdmitsEnabledDemoSpot(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{
		environment: 1,
		response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: capabilityDemoSpotUSDT, Configured: true, Effective: true,
		}}},
	}
	proxy := &fakeControlPanelStrategyProxy{previewResp: spotPreviewResponse()}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/preview-run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-spot"}`)), 42)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if portfolio.calls != 1 {
		t.Fatalf("capability calls=%d want=1", portfolio.calls)
	}
}

func TestStartSessionPassesEnabledDemoSpotThroughToRuntime(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{
		environment: 1,
		response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: capabilityDemoSpotUSDT, Configured: true, Effective: true,
		}}},
	}
	proxy := &fakeControlPanelStrategyProxy{previewResp: spotPreviewResponse()}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-spot","interval":"1m"}`)), 42)
	rec := httptest.NewRecorder()

	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if portfolio.calls != 1 || proxy.runRequest() == nil {
		t.Fatalf("capability calls=%d run request=%#v", portfolio.calls, proxy.runRequest())
	}
}

func TestPreviewRunStrategyPreservesMixedSameSymbolRoutes(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{
		environment: 0,
		response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: capabilityBacktestSpotUSDT, Configured: true, Effective: true,
		}}},
	}
	proxy := &fakeControlPanelStrategyProxy{previewResp: &strategyv1.PreviewRunStrategyResponse{
		Profile: "backtest", Supported: true, Ok: true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{
			{Exchange: "binance", Market: "spot", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
			{Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "5m"},
		},
		DeclaredOrderTargets: []*strategyv1.StrategyOrderTargetBinding{
			{Exchange: "binance", Market: "spot", Symbol: "BTCUSDT"},
			{Exchange: "binance", Market: "perpetual_futures", Symbol: "BTCUSDT"},
		},
		RequiredRoutes: []*strategyv1.StrategyRouteBinding{
			{Exchange: "binance", Market: "spot"},
			{Exchange: "binance", Market: "perpetual_futures"},
		},
	}}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/preview-run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-spot"}`)), 42)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body previewRunStrategyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.DeclaredInputs) != 2 || body.DeclaredInputs[0].Market != "spot" || body.DeclaredInputs[0].Interval != "1m" ||
		body.DeclaredInputs[1].Market != "perpetual_futures" || body.DeclaredInputs[1].Interval != "5m" {
		t.Fatalf("declared inputs=%#v", body.DeclaredInputs)
	}
	if len(body.DeclaredOrderTargets) != 2 || body.DeclaredOrderTargets[0].Market != "spot" || body.DeclaredOrderTargets[1].Market != "perpetual_futures" {
		t.Fatalf("order targets=%#v", body.DeclaredOrderTargets)
	}
	if len(body.RequiredRoutes) != 2 || body.RequiredRoutes[0].Market != "spot" || body.RequiredRoutes[1].Market != "perpetual_futures" {
		t.Fatalf("routes=%#v", body.RequiredRoutes)
	}
	if portfolio.calls != 1 {
		t.Fatalf("capability calls=%d want=1", portfolio.calls)
	}
}

func TestPreviewAndStartPreserveStructuredPortfolioPreflightFailure(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{
		environment: 1,
		response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: capabilityDemoSpotUSDT, Configured: false, Effective: false, Reason: codeSpotCapabilityDisabled,
		}}},
	}
	preview := spotPreviewResponse()
	preview.Ok = false
	preview.Failures = []*strategyv1.PreflightFailureProto{{
		Kind: "portfolio", Reason: "notional below minimum", Code: "SPOT_MIN_NOTIONAL",
		Exchange: 1, Market: 1, Symbol: "BTCUSDT", VenueId: 77, FilterType: "MIN_NOTIONAL",
		Environment: 1, Retryable: false, Source: "preflight",
	}, {
		Kind: "portfolio", Reason: "Futures venue missing", Code: "VENUE_MISSING",
		Exchange: 1, Market: 2, Symbol: "BTCUSDT", Environment: 1, Retryable: false, Source: "preflight",
	}}
	proxy := &fakeControlPanelStrategyProxy{previewResp: preview}
	s := strategyCapabilityTestServer(portfolio, proxy)

	previewReq := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/preview-run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-spot"}`)), 42)
	previewRec := httptest.NewRecorder()
	s.handlePreviewRunStrategy(previewRec, previewReq, 7)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewRec.Code, previewRec.Body.String())
	}
	var previewBody struct {
		Failures []struct {
			Code        string `json:"code"`
			Route       string `json:"route"`
			Symbol      string `json:"symbol"`
			VenueID     int64  `json:"venue_id"`
			FilterType  string `json:"filter_type"`
			Environment int32  `json:"environment"`
			Retryable   bool   `json:"retryable"`
			Source      string `json:"source"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(previewRec.Body.Bytes(), &previewBody); err != nil {
		t.Fatal(err)
	}
	if len(previewBody.Failures) != 2 {
		t.Fatalf("preview failures=%#v body=%s", previewBody.Failures, previewRec.Body.String())
	}
	failure := previewBody.Failures[0]
	if failure.Code != "SPOT_MIN_NOTIONAL" || failure.Route != "binance/spot" || failure.Symbol != "BTCUSDT" ||
		failure.VenueID != 77 || failure.FilterType != "MIN_NOTIONAL" || failure.Environment != 1 || failure.Retryable || failure.Source != "preflight" {
		t.Fatalf("preview failure=%#v body=%s", failure, previewRec.Body.String())
	}
	if previewBody.Failures[1].Code != "VENUE_MISSING" || previewBody.Failures[1].Route != "binance/perpetual_futures" {
		t.Fatalf("second preview failure=%#v body=%s", previewBody.Failures[1], previewRec.Body.String())
	}

	runReq := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-spot","interval":"1m"}`)), 42)
	runRec := httptest.NewRecorder()
	s.handleRunStrategy(runRec, runReq, 7)
	assertSpotGuardResponse(t, runRec, http.StatusPreconditionFailed, "SPOT_MIN_NOTIONAL", 1)
	if proxy.runRequest() != nil {
		t.Fatal("structured preflight failure reached RunStrategy")
	}
	var runBody struct {
		Route      string `json:"route"`
		Symbol     string `json:"symbol"`
		FilterType string `json:"filter_type"`
		Retryable  bool   `json:"retryable"`
		Source     string `json:"source"`
		Failures   []struct {
			Code  string `json:"code"`
			Route string `json:"route"`
		} `json:"failures"`
	}
	if err := json.Unmarshal(runRec.Body.Bytes(), &runBody); err != nil {
		t.Fatal(err)
	}
	if runBody.Route != "binance/spot" || runBody.Symbol != "BTCUSDT" || runBody.FilterType != "MIN_NOTIONAL" || runBody.Retryable || runBody.Source != "preflight" {
		t.Fatalf("run preflight failure=%#v body=%s", runBody, runRec.Body.String())
	}
	if len(runBody.Failures) != 2 || runBody.Failures[1].Code != "VENUE_MISSING" || runBody.Failures[1].Route != "binance/perpetual_futures" {
		t.Fatalf("run failures=%#v body=%s", runBody.Failures, runRec.Body.String())
	}
}

func TestPreviewRunStrategyFuturesMakesNoCapabilityCall(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{environment: 1}
	proxy := &fakeControlPanelStrategyProxy{previewResp: futuresPreviewResponse()}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/preview-run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-futures"}`)), 42)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if portfolio.calls != 0 {
		t.Fatalf("Futures preview capability calls=%d want=0", portfolio.calls)
	}
}

func TestStartSessionFuturesMakesNoSpotCapabilityCall(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{environment: 1}
	proxy := &fakeControlPanelStrategyProxy{previewResp: futuresPreviewResponse()}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-futures","interval":"1m"}`)), 42)
	rec := httptest.NewRecorder()

	s.handleRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if portfolio.calls != 0 || proxy.runRequest() == nil {
		t.Fatalf("Spot capability calls=%d run request=%#v", portfolio.calls, proxy.runRequest())
	}
}

func TestStartSessionRejectsLiveSpotAfterAuthoritativePreviewBeforeRunProxy(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{
		environment: 2,
		response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: capabilityLiveSpotUSDT, Configured: true, Effective: true,
		}}},
	}
	proxy := &fakeControlPanelStrategyProxy{previewResp: spotPreviewResponse()}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-spot","interval":"1m"}`)), 42)
	rec := httptest.NewRecorder()

	s.handleRunStrategy(rec, req, 7)

	assertSpotGuardResponse(t, rec, http.StatusPreconditionFailed, "SPOT_LIVE_ROLLOUT_GUARD", 2)
	previewRequest, runRequest := proxy.strategyRequests()
	if previewRequest == nil {
		t.Fatal("Live Spot route was not discovered through the side-effect-free authoritative preview")
	}
	if runRequest != nil {
		t.Fatal("Live Spot request reached RunStrategy proxy")
	}
	if portfolio.calls != 0 {
		t.Fatalf("Live guard must not trust discovery: calls=%d want=0", portfolio.calls)
	}
}

func TestStartSessionRejectsSpotWhenCapabilityDiscoveryFails(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{environment: 1, err: status.Error(codes.Unavailable, "core unavailable")}
	proxy := &fakeControlPanelStrategyProxy{previewResp: spotPreviewResponse()}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-spot","interval":"1m"}`)), 42)
	rec := httptest.NewRecorder()

	s.handleRunStrategy(rec, req, 7)

	assertSpotGuardResponse(t, rec, http.StatusServiceUnavailable, "SPOT_CAPABILITY_DISCOVERY_UNAVAILABLE", 1)
	if proxy.runRequest() != nil {
		t.Fatal("Spot request reached RunStrategy after failed capability discovery")
	}
}

func TestRunningSpotSessionCanStopWhenCapabilityIsDisabled(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{
		response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: capabilityDemoSpotUSDT, Configured: false, Effective: false, Reason: "disabled after start",
		}}},
		session: &portfoliov1.StrategySessionEntry{
			SessionId: "spot-running", UserId: 42, Environment: 1, Status: "running", RuntimeId: "rt-spot",
		},
	}
	proxy := &fakeControlPanelStrategyProxy{stopResp: &strategyv1.StopStrategyResponse{Stopped: true}}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/strategy-sessions/spot-running/stop",
		bytes.NewBufferString(`{"stop_action":"STOP_ONLY"}`)), 42)
	rec := httptest.NewRecorder()

	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if portfolio.calls != 0 {
		t.Fatalf("running Session stop queried start capability %d times", portfolio.calls)
	}
	if proxy.stopReq == nil || proxy.stopReq.GetStopAction() != strategyv1.StopAction_STOP_ACTION_STOP_ONLY {
		t.Fatalf("stop request=%#v", proxy.stopReq)
	}
}

func TestRunningSpotSessionCanDrainCloseWhenCapabilityIsDisabled(t *testing.T) {
	portfolio := &fakeProductCapabilitiesClient{
		response: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: capabilityDemoSpotUSDT, Configured: false, Effective: false, Reason: "disabled after start",
		}}},
		session: &portfoliov1.StrategySessionEntry{
			SessionId: "spot-running", UserId: 42, Environment: 1, Status: "running", RuntimeId: "rt-spot",
		},
	}
	proxy := &fakeControlPanelStrategyProxy{stopResp: &strategyv1.StopStrategyResponse{Stopped: true}}
	s := strategyCapabilityTestServer(portfolio, proxy)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/strategy-sessions/spot-running/stop",
		bytes.NewBufferString(`{"stop_action":"STOP_AND_CLOSE_POSITIONS"}`)), 42)
	rec := httptest.NewRecorder()

	s.handleStrategySession(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if portfolio.calls != 0 {
		t.Fatalf("running Session drain queried start capability %d times", portfolio.calls)
	}
	if proxy.stopReq == nil || proxy.stopReq.GetStopAction() != strategyv1.StopAction_STOP_ACTION_STOP_AND_CLOSE_POSITIONS {
		t.Fatalf("stop request=%#v", proxy.stopReq)
	}
}

func strategyCapabilityTestServer(portfolio portfoliov1.PortfolioServiceClient, proxy *fakeControlPanelStrategyProxy) *server {
	return &server{
		portfolios: portfolio,
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-spot"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-spot", Role: "executor"},
		},
		cpRuntime: proxy,
	}
}

func spotPreviewResponse() *strategyv1.PreviewRunStrategyResponse {
	return &strategyv1.PreviewRunStrategyResponse{
		Profile: "backtest", Supported: true, Ok: true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{{
			Exchange: "binance", Market: "spot", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m",
		}},
		DeclaredOrderTargets: []*strategyv1.StrategyOrderTargetBinding{{Exchange: "binance", Market: "spot", Symbol: "BTCUSDT"}},
		RequiredRoutes:       []*strategyv1.StrategyRouteBinding{{Exchange: "binance", Market: "spot"}},
	}
}

func futuresPreviewResponse() *strategyv1.PreviewRunStrategyResponse {
	return &strategyv1.PreviewRunStrategyResponse{
		Profile: "backtest", Supported: true, Ok: true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{{
			Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m",
		}},
		DeclaredOrderTargets: []*strategyv1.StrategyOrderTargetBinding{{Exchange: "binance", Market: "perpetual_futures", Symbol: "BTCUSDT"}},
		RequiredRoutes:       []*strategyv1.StrategyRouteBinding{{Exchange: "binance", Market: "perpetual_futures"}},
	}
}

func assertSpotGuardResponse(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string, wantEnvironment int32) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, wantStatus, rec.Body.String())
	}
	var body struct {
		Code        string `json:"code"`
		Environment int32  `json:"environment"`
		Retryable   bool   `json:"retryable"`
		Source      string `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != wantCode || body.Environment != wantEnvironment || body.Source == "" {
		t.Fatalf("guard response=%#v want code=%s environment=%d", body, wantCode, wantEnvironment)
	}
}
