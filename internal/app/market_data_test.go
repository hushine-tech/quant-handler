package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ── fake MarketDataControlPlaneServiceClient ────────────────────────────────
//
// Embeds the generated client interface so unused methods are nil (calling
// one would NPE — fine, that's how a test catches "wrong RPC path"). We
// override only the RPCs the market-data handlers call.

type fakeMarketDataClient struct {
	mdv1.MarketDataControlPlaneServiceClient // unused methods panic as nil-interface calls

	// Capture last request seen for assertions.
	lastCreateReq   *mdv1.CreateMarketDataRequestRequest
	lastCancelReq   *mdv1.CancelMarketDataRequestRequest
	lastListReq     *mdv1.ListMarketDataRequestsRequest
	lastGetReq      *mdv1.GetMarketDataStreamStatusRequest
	lastHealthReq   *mdv1.ListSessionDeliveryHealthRequest
	lastCoverageReq *mdv1.QueryMarketDataCoverageRequest
	lastValidateReq *mdv1.ValidateMarketDataCoverageRequest
	lastKlinesReq   *mdv1.QueryMarketDataKlinesRequest

	// Canned return values / errors.
	createResp   *mdv1.CreateMarketDataRequestResponse
	createErr    error
	cancelErr    error
	listResp     *mdv1.ListMarketDataRequestsResponse
	listErr      error
	getResp      *mdv1.GetMarketDataStreamStatusResponse
	getErr       error
	healthResp   *mdv1.ListSessionDeliveryHealthResponse
	healthErr    error
	coverageResp *mdv1.QueryMarketDataCoverageResponse
	coverageErr  error
	validateResp *mdv1.ValidateMarketDataCoverageResponse
	validateErr  error
	klinesResp   *mdv1.QueryMarketDataKlinesResponse
	klinesErr    error
}

func (f *fakeMarketDataClient) CreateMarketDataRequest(_ context.Context, in *mdv1.CreateMarketDataRequestRequest, _ ...grpc.CallOption) (*mdv1.CreateMarketDataRequestResponse, error) {
	f.lastCreateReq = in
	return f.createResp, f.createErr
}

func (f *fakeMarketDataClient) CancelMarketDataRequest(_ context.Context, in *mdv1.CancelMarketDataRequestRequest, _ ...grpc.CallOption) (*mdv1.CancelMarketDataRequestResponse, error) {
	f.lastCancelReq = in
	if f.cancelErr != nil {
		return nil, f.cancelErr
	}
	return &mdv1.CancelMarketDataRequestResponse{}, nil
}

func (f *fakeMarketDataClient) ListMarketDataRequests(_ context.Context, in *mdv1.ListMarketDataRequestsRequest, _ ...grpc.CallOption) (*mdv1.ListMarketDataRequestsResponse, error) {
	f.lastListReq = in
	return f.listResp, f.listErr
}

func (f *fakeMarketDataClient) GetMarketDataStreamStatus(_ context.Context, in *mdv1.GetMarketDataStreamStatusRequest, _ ...grpc.CallOption) (*mdv1.GetMarketDataStreamStatusResponse, error) {
	f.lastGetReq = in
	return f.getResp, f.getErr
}

func (f *fakeMarketDataClient) ListSessionDeliveryHealth(_ context.Context, in *mdv1.ListSessionDeliveryHealthRequest, _ ...grpc.CallOption) (*mdv1.ListSessionDeliveryHealthResponse, error) {
	f.lastHealthReq = in
	return f.healthResp, f.healthErr
}

func (f *fakeMarketDataClient) QueryMarketDataCoverage(_ context.Context, in *mdv1.QueryMarketDataCoverageRequest, _ ...grpc.CallOption) (*mdv1.QueryMarketDataCoverageResponse, error) {
	f.lastCoverageReq = in
	if f.coverageResp != nil || f.coverageErr != nil {
		return f.coverageResp, f.coverageErr
	}
	return &mdv1.QueryMarketDataCoverageResponse{
		Key:              in.GetKey(),
		RequestedStartAt: in.GetStartAt(),
		RequestedEndAt:   in.GetEndAt(),
		Complete:         false,
		MissingSegments: []*mdv1.MarketDataTimeRange{{
			StartAt:       in.GetStartAt(),
			EndAt:         in.GetEndAt(),
			ExpectedCount: 60,
		}},
	}, nil
}

func (f *fakeMarketDataClient) ValidateMarketDataCoverage(_ context.Context, in *mdv1.ValidateMarketDataCoverageRequest, _ ...grpc.CallOption) (*mdv1.ValidateMarketDataCoverageResponse, error) {
	f.lastValidateReq = in
	if f.validateResp != nil || f.validateErr != nil {
		return f.validateResp, f.validateErr
	}
	return &mdv1.ValidateMarketDataCoverageResponse{
		Key:              in.GetKey(),
		RequestedStartAt: in.GetStartAt(),
		RequestedEndAt:   in.GetEndAt(),
		Ok:               true,
	}, nil
}

func (f *fakeMarketDataClient) QueryMarketDataKlines(_ context.Context, in *mdv1.QueryMarketDataKlinesRequest, _ ...grpc.CallOption) (*mdv1.QueryMarketDataKlinesResponse, error) {
	f.lastKlinesReq = in
	if f.klinesResp != nil || f.klinesErr != nil {
		return f.klinesResp, f.klinesErr
	}
	return &mdv1.QueryMarketDataKlinesResponse{
		Key:              in.GetKey(),
		RequestedStartAt: in.GetStartAt(),
		RequestedEndAt:   in.GetEndAt(),
		Limit:            in.GetLimit(),
		RowCount:         1,
		Rows: []*mdv1.MarketDataKline{{
			OpenTime:  in.GetStartAt(),
			CloseTime: in.GetEndAt(),
			Open:      100,
			High:      101,
			Low:       99,
			Close:     100.5,
			Volume:    12,
		}},
	}, nil
}

func newServerWithFakeMarketData(t *testing.T, fake *fakeMarketDataClient) *server {
	t.Helper()
	return &server{
		marketData:  fake,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}
}

// ── request helper: attach a user-id context manually so we bypass JWT ─────

func withUID(r *http.Request, uid int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userIDContextKey, uid))
}

// ────────────────────────────────────────────────────────────────────────────
// createMarketDataRequest
// ────────────────────────────────────────────────────────────────────────────

func TestMarketData_Create_acceptsFlatBody(t *testing.T) {
	fake := &fakeMarketDataClient{
		createResp: &mdv1.CreateMarketDataRequestResponse{
			Request: &mdv1.MarketDataRequest{RequestId: 42, UserId: 7, Status: "active", Scope: "live"},
			Stream:  &mdv1.MarketDataStream{StreamId: 100, ActualState: "pending"},
		},
	}
	s := newServerWithFakeMarketData(t, fake)

	body := `{"exchange":"binance","market":"futures","symbol":"BTCUSDT","interval":"1m","needs_live_delivery":true}`
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/market-data/requests", bytes.NewBufferString(body)), 7)
	rec := httptest.NewRecorder()

	s.handleMarketData(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastCreateReq == nil {
		t.Fatal("gRPC CreateMarketDataRequest was not called")
	}
	if got := fake.lastCreateReq.GetKey().GetSymbol(); got != "BTCUSDT" {
		t.Errorf("symbol forwarded = %q, want BTCUSDT", got)
	}
	if !fake.lastCreateReq.GetNeedsLiveDelivery() {
		t.Error("needs_live_delivery not propagated")
	}
	if fake.lastCreateReq.GetScope() != "live" {
		t.Errorf("scope = %q, want live", fake.lastCreateReq.GetScope())
	}
	if fake.lastCreateReq.GetUserId() != 7 {
		t.Errorf("user_id = %d, want 7", fake.lastCreateReq.GetUserId())
	}
}

func TestMarketData_Create_defaultsKindToKline(t *testing.T) {
	fake := &fakeMarketDataClient{
		createResp: &mdv1.CreateMarketDataRequestResponse{
			Request: &mdv1.MarketDataRequest{RequestId: 1, UserId: 1, Scope: "live"},
			Stream:  &mdv1.MarketDataStream{},
		},
	}
	s := newServerWithFakeMarketData(t, fake)

	body := `{"exchange":"binance","market":"spot","symbol":"ETHUSDT","interval":"5m"}`
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/market-data/requests", bytes.NewBufferString(body)), 1)
	rec := httptest.NewRecorder()

	s.handleMarketData(rec, req)

	if fake.lastCreateReq == nil || fake.lastCreateReq.GetKey().GetKind() != "kline" {
		t.Errorf("kind default = %q, want kline", fake.lastCreateReq.GetKey().GetKind())
	}
}

func TestMarketData_Klines_forwardsQueryAndReturnsRows(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/market-data/klines?exchange=binance&market=futures&kind=kline&symbol=BTCUSDT&interval=1m&start_time_ms=1779033600000&end_time_ms=1779037200000&limit=25", nil), 7)
	rec := httptest.NewRecorder()

	s.handleMarketData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastKlinesReq == nil {
		t.Fatal("gRPC QueryMarketDataKlines was not called")
	}
	if got := fake.lastKlinesReq.GetKey().GetSymbol(); got != "BTCUSDT" {
		t.Fatalf("symbol = %q, want BTCUSDT", got)
	}
	if got := fake.lastKlinesReq.GetLimit(); got != 25 {
		t.Fatalf("limit = %d, want 25", got)
	}
	var out marketDataKlinesJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.RowCount != 1 || len(out.Rows) != 1 {
		t.Fatalf("rows = count:%d len:%d, want 1", out.RowCount, len(out.Rows))
	}
	if out.Rows[0].Open != 100 || out.Rows[0].Close != 100.5 {
		t.Fatalf("row = %#v, want OHLC values", out.Rows[0])
	}
}

func TestMarketData_Create_acceptsNestedKey(t *testing.T) {
	fake := &fakeMarketDataClient{
		createResp: &mdv1.CreateMarketDataRequestResponse{
			Request: &mdv1.MarketDataRequest{RequestId: 2, UserId: 1, Scope: "live"},
			Stream:  &mdv1.MarketDataStream{},
		},
	}
	s := newServerWithFakeMarketData(t, fake)

	body := `{"key":{"exchange":"binance","market":"futures","kind":"kline","symbol":"SOLUSDT","interval":"15m"}}`
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/market-data/requests", bytes.NewBufferString(body)), 1)
	rec := httptest.NewRecorder()

	s.handleMarketData(rec, req)

	if fake.lastCreateReq == nil || fake.lastCreateReq.GetKey().GetSymbol() != "SOLUSDT" {
		t.Errorf("nested key not parsed; got %+v", fake.lastCreateReq)
	}
}

func TestMarketData_Create_historicalForwardsRangeAndScope(t *testing.T) {
	fake := &fakeMarketDataClient{
		createResp: &mdv1.CreateMarketDataRequestResponse{
			Request: &mdv1.MarketDataRequest{RequestId: 3, UserId: 1, Scope: "historical"},
		},
	}
	s := newServerWithFakeMarketData(t, fake)

	body := `{"scope":"historical","exchange":"binance","market":"futures","symbol":"ETHUSDT","interval":"1m","start_time_ms":1774972800000,"end_time_ms":1775059200000,"needs_live_delivery":false}`
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/market-data/requests", bytes.NewBufferString(body)), 1)
	rec := httptest.NewRecorder()

	s.handleMarketData(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastCreateReq.GetScope() != "historical" {
		t.Fatalf("scope = %q, want historical", fake.lastCreateReq.GetScope())
	}
	if fake.lastCreateReq.GetNeedsLiveDelivery() {
		t.Fatal("historical request should not enable needs_live_delivery")
	}
	if fake.lastCreateReq.GetRequestedStartAt() == nil || fake.lastCreateReq.GetRequestedEndAt() == nil {
		t.Fatal("historical range was not forwarded")
	}
}

func TestMarketData_Create_historicalRejectsMissingRange(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)

	body := `{"scope":"historical","exchange":"binance","market":"futures","symbol":"ETHUSDT","interval":"1m"}`
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/market-data/requests", bytes.NewBufferString(body)), 1)
	rec := httptest.NewRecorder()

	s.handleMarketData(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMarketData_Create_rejectsMissingUser(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)

	body := `{"exchange":"binance","market":"futures","symbol":"BTCUSDT","interval":"1m"}`
	req := httptest.NewRequest(http.MethodPost, "/api/market-data/requests", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.handleMarketData(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMarketData_Create_rejectsInvalidJSON(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/market-data/requests", bytes.NewBufferString("{malformed")), 1)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMarketData_Create_mapsGrpcInvalidToBadRequest(t *testing.T) {
	fake := &fakeMarketDataClient{
		createErr: status.Error(codes.InvalidArgument, "invalid market"),
	}
	s := newServerWithFakeMarketData(t, fake)
	body := `{"exchange":"binance","market":"futures","symbol":"BTCUSDT","interval":"1m"}`
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/market-data/requests", bytes.NewBufferString(body)), 1)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// cancelMarketDataRequest
// ────────────────────────────────────────────────────────────────────────────

func TestMarketData_Cancel_successWithOwner(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodDelete, "/api/market-data/requests/42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastCancelReq.GetRequestId() != 42 {
		t.Errorf("forwarded id = %d, want 42", fake.lastCancelReq.GetRequestId())
	}
	if fake.lastCancelReq.GetUserId() != 7 {
		t.Errorf("forwarded user_id = %d, want 7", fake.lastCancelReq.GetUserId())
	}
}

func TestMarketData_Cancel_permissionDeniedMapsTo403(t *testing.T) {
	fake := &fakeMarketDataClient{
		cancelErr: status.Error(codes.PermissionDenied, "not yours"),
	}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodDelete, "/api/market-data/requests/42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestMarketData_Cancel_notFoundMapsTo404(t *testing.T) {
	fake := &fakeMarketDataClient{
		cancelErr: status.Error(codes.NotFound, "nope"),
	}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodDelete, "/api/market-data/requests/42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestMarketData_Cancel_rejectsNonIntID(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodDelete, "/api/market-data/requests/abc", nil), 7)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// list / get
// ────────────────────────────────────────────────────────────────────────────

func TestMarketData_List_returnsFlattenedEntries(t *testing.T) {
	fake := &fakeMarketDataClient{
		listResp: &mdv1.ListMarketDataRequestsResponse{
			Entries: []*mdv1.MarketDataRequestWithStream{
				{
					Request: &mdv1.MarketDataRequest{RequestId: 1, UserId: 7, Status: "active", Scope: "live", Key: &mdv1.StreamKey{Symbol: "BTCUSDT"}},
					Stream:  &mdv1.MarketDataStream{StreamId: 10, ActualState: "running"},
				},
			},
		},
	}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/market-data/requests", nil), 7)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out []marketDataEntryJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].Request.RequestID != 1 || out[0].Stream.ActualState != "running" {
		t.Errorf("unexpected response: %+v", out)
	}
	if fake.lastListReq.GetUserId() != 7 {
		t.Errorf("forwarded user_id = %d, want 7", fake.lastListReq.GetUserId())
	}
}

func TestMarketData_List_handlesHistoricalEntryWithoutStream(t *testing.T) {
	fake := &fakeMarketDataClient{
		listResp: &mdv1.ListMarketDataRequestsResponse{
			Entries: []*mdv1.MarketDataRequestWithStream{
				{
					Request: &mdv1.MarketDataRequest{
						RequestId: 2, UserId: 7, Status: "pending", Scope: "historical",
						Key: &mdv1.StreamKey{Symbol: "ETHUSDT"},
					},
				},
			},
		},
	}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/market-data/requests", nil), 7)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out []marketDataEntryJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0].Request.Scope != "historical" {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestMarketData_Stream_byID(t *testing.T) {
	fake := &fakeMarketDataClient{
		getResp: &mdv1.GetMarketDataStreamStatusResponse{
			Stream: &mdv1.MarketDataStream{StreamId: 9, ActualState: "running"},
		},
	}
	s := newServerWithFakeMarketData(t, fake)
	req := httptest.NewRequest(http.MethodGet, "/api/market-data/streams/9", nil)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if fake.lastGetReq.GetStreamId() != 9 {
		t.Errorf("forwarded stream_id = %d, want 9", fake.lastGetReq.GetStreamId())
	}
}

func TestMarketData_Stream_byKey(t *testing.T) {
	fake := &fakeMarketDataClient{
		getResp: &mdv1.GetMarketDataStreamStatusResponse{
			Stream: &mdv1.MarketDataStream{StreamId: 11},
		},
	}
	s := newServerWithFakeMarketData(t, fake)
	req := httptest.NewRequest(http.MethodGet,
		"/api/market-data/streams?exchange=binance&market=futures&symbol=BTCUSDT&interval=1m", nil)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if fake.lastGetReq.GetKey().GetSymbol() != "BTCUSDT" {
		t.Errorf("forwarded key.symbol = %q, want BTCUSDT", fake.lastGetReq.GetKey().GetSymbol())
	}
	if fake.lastGetReq.GetKey().GetKind() != "kline" {
		t.Errorf("forwarded key.kind = %q, want kline (default)", fake.lastGetReq.GetKey().GetKind())
	}
}

func TestMarketData_Stream_byKey_missingQueryRejected(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)
	req := httptest.NewRequest(http.MethodGet, "/api/market-data/streams?exchange=binance", nil)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMarketData_DeliveryHealth(t *testing.T) {
	fake := &fakeMarketDataClient{
		healthResp: &mdv1.ListSessionDeliveryHealthResponse{Items: []*mdv1.SessionDeliveryHealth{{
			Subscription: &mdv1.SessionMarketDataSubscription{
				SubscriptionId: 7,
				UserId:         42,
				SessionId:      "sess-1",
				RuntimeId:      "rt-1",
				Key:            &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
				Mode:           2,
				Status:         "active",
			},
			Lease: &mdv1.StreamDeliveryLease{
				LeaseId:        "sdl-7",
				SubscriptionId: 7,
				Status:         "active",
				LastTopic:      "md.kline.binance.futures.1m",
				LastPartition:  3,
				LastOffset:     99,
			},
			HealthStatus: "delivering",
		}}},
	}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/market-data/delivery-health?session_id=sess-1&runtime_id=rt-1", nil), 42)
	rec := httptest.NewRecorder()
	s.handleMarketData(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastHealthReq.GetUserId() != 42 || fake.lastHealthReq.GetSessionId() != "sess-1" || fake.lastHealthReq.GetRuntimeId() != "rt-1" {
		t.Fatalf("health request = %+v", fake.lastHealthReq)
	}
	var body sessionDeliveryHealthListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].HealthStatus != "delivering" || body.Items[0].Lease.LastOffset != 99 {
		t.Fatalf("body = %+v, want delivering offset 99", body)
	}
}

// ────────────────────────────────────────────────────────────────────────────
// grpcToHTTP additions
// ────────────────────────────────────────────────────────────────────────────

func TestGrpcToHTTPMapsPermissionDenied(t *testing.T) {
	code, _ := grpcToHTTP(status.Error(codes.PermissionDenied, "x"))
	if code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", code)
	}
}

func TestGrpcToHTTPMapsFailedPrecondition(t *testing.T) {
	code, _ := grpcToHTTP(status.Error(codes.FailedPrecondition, "not ready"))
	if code != http.StatusPreconditionFailed {
		t.Errorf("code = %d, want 412", code)
	}
}

func TestGrpcToHTTPMapsAlreadyExists(t *testing.T) {
	code, _ := grpcToHTTP(status.Error(codes.AlreadyExists, "dup"))
	if code != http.StatusConflict {
		t.Errorf("code = %d, want 409", code)
	}
}

// Also ensure a non-mapped code still falls back to BadGateway (existing behavior).
func TestGrpcToHTTPUnknownFallsBackTo502(t *testing.T) {
	code, _ := grpcToHTTP(errors.New("plain error")) // not a grpc status
	if code != http.StatusBadGateway {
		t.Errorf("code = %d, want 502", code)
	}
}
