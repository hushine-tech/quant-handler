package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeStrategyClient is a minimal gRPC client double for testing the
// preview-run-strategy HTTP handler. It only implements the RPC methods
// the handler actually calls, matching the interface on `server.strategy`.
type fakeStrategyClient struct {
	strategyv1.StrategyServiceClient

	previewReq  *strategyv1.PreviewRunStrategyRequest
	previewResp *strategyv1.PreviewRunStrategyResponse
	previewErr  error
	runReq      *strategyv1.RunStrategyRequest
	runResp     *strategyv1.RunStrategyResponse
	runErr      error
}

func (f *fakeStrategyClient) PreviewRunStrategy(_ context.Context, in *strategyv1.PreviewRunStrategyRequest, _ ...grpc.CallOption) (*strategyv1.PreviewRunStrategyResponse, error) {
	f.previewReq = in
	return f.previewResp, f.previewErr
}

func (f *fakeStrategyClient) RunStrategy(_ context.Context, in *strategyv1.RunStrategyRequest, _ ...grpc.CallOption) (*strategyv1.RunStrategyResponse, error) {
	f.runReq = in
	if f.runResp != nil || f.runErr != nil {
		return f.runResp, f.runErr
	}
	return &strategyv1.RunStrategyResponse{SessionId: "sess-test"}, nil
}

func TestPreviewRunStrategy_ForwardsBodyAndReturnsJSON(t *testing.T) {
	fake := &fakeStrategyClient{
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
		},
	}
	s := &server{
		strategy:    fake,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}

	body := `{"strategy_path":"","start_time_ms":0,"end_time_ms":0}`
	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/preview-run-strategy", bytes.NewBufferString(body)), 17)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.previewReq == nil {
		t.Fatal("PreviewRunStrategy gRPC was not called")
	}
	if got := fake.previewReq.GetAccountId(); got != 7 {
		t.Errorf("account_id forwarded = %d, want 7", got)
	}
	if got := fake.previewReq.GetUserId(); got != 17 {
		t.Errorf("user_id forwarded = %d, want 17", got)
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
	if f.InputKey.Symbol != "BTCUSDT" || f.InputKey.Market != "futures" || f.InputKey.Interval != "1m" {
		t.Errorf("failure input_key = %+v", f.InputKey)
	}
}

func TestPreviewRunStrategy_PropagatesFailedPreconditionFromBackend(t *testing.T) {
	fake := &fakeStrategyClient{
		previewErr: status.Error(codes.FailedPrecondition, "strategy input declaration invalid: missing INPUTS"),
	}
	s := &server{
		strategy:    fake,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodPost,
		"/api/accounts/7/preview-run-strategy", bytes.NewBufferString("{}")), 17)
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
	fake := &fakeStrategyClient{}
	s := &server{
		strategy:    fake,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodGet,
		"/api/accounts/7/preview-run-strategy", nil), 17)
	rec := httptest.NewRecorder()

	s.handlePreviewRunStrategy(rec, req, 7)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if fake.previewReq != nil {
		t.Fatal("gRPC must not be called for GET")
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
