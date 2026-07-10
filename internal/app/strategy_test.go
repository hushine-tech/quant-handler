package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPreviewRunStrategy_ForwardsBodyAndReturnsJSON(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		previewResp: &strategyv1.PreviewRunStrategyResponse{
			Profile:   "testnet",
			Supported: true,
			Ok:        false,
			Failures: []*strategyv1.PreflightFailureProto{
				{
					Kind:   "stream",
					Reason: "stream missing",
					InputKey: &strategyv1.PreflightInputKey{
						Market:   "futures",
						Symbol:   "BTCUSDT",
						Interval: "1m",
					},
				},
			},
			RequiredStreams: []*strategyv1.LiveStreamBinding{},
			DeclaredInputs: []*strategyv1.LiveStreamBinding{{
				Exchange: "binance",
				Market:   "perpetual_futures",
				Kind:     "kline",
				Symbol:   "ETHUSDT",
				Interval: "1m",
			}},
			DeclaredOrderTargets: []*strategyv1.StrategyOrderTargetBinding{{
				Exchange: "binance",
				Market:   "perpetual_futures",
				Symbol:   "ETHUSDT",
			}},
			RequiredRoutes: []*strategyv1.StrategyRouteBinding{{
				Exchange: "binance",
				Market:   "perpetual_futures",
			}},
			RiskControls: &strategyv1.RiskControls{
				MaxLossClosePct:    0.2,
				MaxLossCloseSource: "strategy",
				Leverage:           3,
				LeverageSource:     "request_default",
			},
		},
	}
	s := &server{
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-preview"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-preview", Role: "executor"},
		},
		cpRuntime:   proxy,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}

	body := `{"runtime_id":"rt-preview","strategy_path":"","start_time_ms":0,"end_time_ms":0,"max_loss_close_pct":0.25,"leverage":3}`
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/preview-run-strategy", bytes.NewBufferString(body)), 17)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.previewReq == nil {
		t.Fatal("PreviewRunStrategy gRPC was not called")
	}
	if got := proxy.previewReq.GetPortfolioId(); got != 7 {
		t.Errorf("portfolio_id forwarded = %d, want 7", got)
	}
	if got := proxy.previewReq.GetUserId(); got != 17 {
		t.Errorf("user_id forwarded = %d, want 17", got)
	}
	if got := proxy.previewReq.GetMaxLossClosePct(); got != 0.25 {
		t.Errorf("max_loss_close_pct forwarded = %v, want 0.25", got)
	}
	if got := proxy.previewReq.GetLeverage(); got != 3 {
		t.Errorf("leverage forwarded = %v, want 3", got)
	}

	var resp previewRunStrategyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Profile != "testnet" {
		t.Errorf("profile = %q, want testnet", resp.Profile)
	}
	if !resp.Supported {
		t.Error("supported = false, want true")
	}
	if resp.Ok {
		t.Error("ok = true, want false (preflight had failures)")
	}
	if len(resp.Failures) != 1 {
		t.Fatalf("got %d failures, want 1", len(resp.Failures))
	}
	f := resp.Failures[0]
	if f.Kind != "stream" {
		t.Errorf("failure kind = %q, want stream", f.Kind)
	}
	if f.InputKey == nil {
		t.Fatal("failure input_key is nil")
	}
	if f.InputKey.Symbol != "BTCUSDT" || f.InputKey.Market != "perpetual_futures" || f.InputKey.Interval != "1m" {
		t.Errorf("failure input_key = %+v", f.InputKey)
	}
	if len(resp.Inputs) != 1 || resp.Inputs[0].Market != "perpetual_futures" || resp.Inputs[0].Symbol != "ETHUSDT" {
		t.Fatalf("inputs = %+v, want ETHUSDT perpetual_futures", resp.Inputs)
	}
	if len(resp.OrderTargets) != 1 || resp.OrderTargets[0].Market != "perpetual_futures" || resp.OrderTargets[0].Symbol != "ETHUSDT" {
		t.Fatalf("order_targets = %+v, want ETHUSDT perpetual_futures", resp.OrderTargets)
	}
	if len(resp.RequiredRoutes) != 1 || resp.RequiredRoutes[0].Market != "perpetual_futures" {
		t.Fatalf("required_routes = %+v, want binance/perpetual_futures", resp.RequiredRoutes)
	}
	if resp.RiskControls.MaxLossClosePct != 0.2 || resp.RiskControls.MaxLossCloseSource != "strategy" {
		t.Fatalf("risk_controls = %+v, want strategy 0.2", resp.RiskControls)
	}
	if resp.RiskControls.Leverage != 3 || resp.RiskControls.LeverageSource != "request_default" {
		t.Fatalf("risk_controls leverage = %+v, want request_default 3", resp.RiskControls)
	}
}

func TestPreviewRunStrategy_UsesRuntimeProxyDeadline(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	s := &server{
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-preview"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-preview", Role: "executor"},
		},
		cpRuntime:   proxy,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/preview-run-strategy", bytes.NewBufferString(`{"runtime_id":"rt-preview"}`)), 17)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !proxy.previewDeadlineSet {
		t.Fatal("PreviewRunStrategy downstream call had no deadline")
	}
	if proxy.previewDeadlineUntil <= 0 || proxy.previewDeadlineUntil > previewRunStrategyRPCTimeout {
		t.Fatalf("PreviewRunStrategy deadline remaining = %v, want within %v", proxy.previewDeadlineUntil, previewRunStrategyRPCTimeout)
	}
}

func TestPreviewRunStrategy_RejectsInvalidJSON(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	s := &server{
		cpRuntime:   proxy,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/preview-run-strategy",
		bytes.NewBufferString(`{"runtime_id":"rt-preview","max_loss_close_pct":"bad"}`)), 17)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if proxy.previewReq != nil {
		t.Fatal("PreviewRunStrategy gRPC should not be called for invalid JSON")
	}
}

func TestPreviewRunStrategy_PropagatesFailedPreconditionFromBackend(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		previewErr: status.Error(codes.FailedPrecondition, "strategy input declaration invalid: missing INPUTS"),
	}
	s := &server{
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-preview"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-preview", Role: "executor"},
		},
		cpRuntime:   proxy,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/portfolios/7/preview-run-strategy", bytes.NewBufferString(`{"runtime_id":"rt-preview"}`)), 17)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	// FailedPrecondition → 412 in grpcToHTTP mapping.
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412 PreconditionFailed; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error == "" || !contains(body.Error, "strategy input declaration") {
		t.Errorf("error body = %q, want to contain 'strategy input declaration'", body.Error)
	}
}

func TestPreviewRunStrategy_RejectsGETMethod(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{}
	s := &server{
		cpRuntime:   proxy,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodGet,
		"/api/portfolios/7/preview-run-strategy", nil), 17)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if proxy.previewReq != nil {
		t.Fatal("gRPC must not be called for GET")
	}
}

func TestGrpcToHTTPMapsDeadlineExceededToGatewayTimeout(t *testing.T) {
	code, msg := grpcToHTTP(status.Error(codes.DeadlineExceeded, "runtime proxy deadline exceeded"))
	if code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", code)
	}
	if !contains(msg, "Runtime did not respond in time") {
		t.Fatalf("message = %q, want friendly runtime timeout", msg)
	}
}

// contains is a tiny helper to avoid pulling in strings.Contains above.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
