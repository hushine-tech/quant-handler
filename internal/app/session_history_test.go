package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
	"github.com/hushine-tech/core-service/gen/portfoliov1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ── test doubles ──────────────────────────────────────────────────────────

type fakeSessionPortfoliosClient struct {
	portfoliov1.PortfolioServiceClient // unused methods panic on nil-interface call

	// Capture last request.
	lastSnapshotsReq              *portfoliov1.ListSessionSnapshotsRequest
	lastReconciliationReq         *portfoliov1.ListReconciliationRunsRequest
	lastReconciliationSummaryReq  *portfoliov1.GetSessionReconciliationSummaryRequest
	lastGetSessionReq             *portfoliov1.GetSessionRequest
	lastGetPortfolioReq           *portfoliov1.GetPortfolioRequest
	lastListSessionsReq           *portfoliov1.ListSessionsRequest
	lastUpdateSessionReq          *portfoliov1.UpdateSessionRequest
	lastIndicatorDefinitionsReq   *portfoliov1.ListStrategyIndicatorsRequest
	lastIndicatorChunksReq        *portfoliov1.ListStrategyIndicatorChunksRequest
	lastIndicatorDefinitionsV2Req *portfoliov1.ListStrategyIndicatorsV2Request
	lastIndicatorChunksV2Req      *portfoliov1.ListStrategyIndicatorChunksV2Request

	// Canned responses.
	snapshotsResp              *portfoliov1.ListSessionSnapshotsResponse
	reconciliationResp         *portfoliov1.ListReconciliationRunsResponse
	reconciliationSummaryResp  *portfoliov1.GetSessionReconciliationSummaryResponse
	getSessionResp             *portfoliov1.GetSessionResponse
	getSessionErr              error
	getPortfolioResp           *portfoliov1.GetPortfolioResponse
	getPortfolioErr            error
	listSessionsResp           *portfoliov1.ListSessionsResponse
	listSessionsErr            error
	updateSessionErr           error
	indicatorDefinitionsResp   *portfoliov1.ListStrategyIndicatorsResponse
	indicatorDefinitionsErr    error
	indicatorChunksResp        *portfoliov1.ListStrategyIndicatorChunksResponse
	indicatorChunksErr         error
	indicatorDefinitionsV2Resp *portfoliov1.ListStrategyIndicatorsV2Response
	indicatorDefinitionsV2Err  error
	indicatorChunksV2Resp      *portfoliov1.ListStrategyIndicatorChunksV2Response
	indicatorChunksV2Err       error
	portfolioEnvironment       int32
	reconciliationSummaryErr   error
}

func (f *fakeSessionPortfoliosClient) GetPortfolio(_ context.Context, in *portfoliov1.GetPortfolioRequest, _ ...grpc.CallOption) (*portfoliov1.GetPortfolioResponse, error) {
	f.lastGetPortfolioReq = in
	if f.getPortfolioErr != nil {
		return nil, f.getPortfolioErr
	}
	if f.getPortfolioResp != nil {
		return f.getPortfolioResp, nil
	}
	return &portfoliov1.GetPortfolioResponse{Portfolio: &portfoliov1.PortfolioRegistryEntry{
		PortfolioId: in.GetPortfolioId(),
		UserId:      in.GetUserId(),
		Environment: f.portfolioEnvironment,
	}}, nil
}

func (f *fakeSessionPortfoliosClient) GetSession(_ context.Context, in *portfoliov1.GetSessionRequest, _ ...grpc.CallOption) (*portfoliov1.GetSessionResponse, error) {
	f.lastGetSessionReq = in
	if f.getSessionErr != nil {
		return nil, f.getSessionErr
	}
	if f.getSessionResp != nil {
		return f.getSessionResp, nil
	}
	return &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
		SessionId: in.GetSessionId(),
		UserId:    in.GetUserId(),
		RuntimeId: "rt-default",
	}}, nil
}

func (f *fakeSessionPortfoliosClient) ListSessions(_ context.Context, in *portfoliov1.ListSessionsRequest, _ ...grpc.CallOption) (*portfoliov1.ListSessionsResponse, error) {
	f.lastListSessionsReq = in
	if f.listSessionsErr != nil {
		return nil, f.listSessionsErr
	}
	if f.listSessionsResp != nil {
		return f.listSessionsResp, nil
	}
	return &portfoliov1.ListSessionsResponse{}, nil
}

func (f *fakeSessionPortfoliosClient) UpdateSession(_ context.Context, in *portfoliov1.UpdateSessionRequest, _ ...grpc.CallOption) (*portfoliov1.UpdateSessionResponse, error) {
	f.lastUpdateSessionReq = in
	if f.updateSessionErr != nil {
		return nil, f.updateSessionErr
	}
	return &portfoliov1.UpdateSessionResponse{}, nil
}

func (f *fakeSessionPortfoliosClient) ListSessionSnapshots(_ context.Context, in *portfoliov1.ListSessionSnapshotsRequest, _ ...grpc.CallOption) (*portfoliov1.ListSessionSnapshotsResponse, error) {
	f.lastSnapshotsReq = in
	return f.snapshotsResp, nil
}

func (f *fakeSessionPortfoliosClient) ListReconciliationRuns(_ context.Context, in *portfoliov1.ListReconciliationRunsRequest, _ ...grpc.CallOption) (*portfoliov1.ListReconciliationRunsResponse, error) {
	f.lastReconciliationReq = in
	return f.reconciliationResp, nil
}

func (f *fakeSessionPortfoliosClient) GetSessionReconciliationSummary(_ context.Context, in *portfoliov1.GetSessionReconciliationSummaryRequest, _ ...grpc.CallOption) (*portfoliov1.GetSessionReconciliationSummaryResponse, error) {
	f.lastReconciliationSummaryReq = in
	if f.reconciliationSummaryErr != nil {
		return nil, f.reconciliationSummaryErr
	}
	return f.reconciliationSummaryResp, nil
}

func (f *fakeSessionPortfoliosClient) ListStrategyIndicators(_ context.Context, in *portfoliov1.ListStrategyIndicatorsRequest, _ ...grpc.CallOption) (*portfoliov1.ListStrategyIndicatorsResponse, error) {
	f.lastIndicatorDefinitionsReq = in
	if f.indicatorDefinitionsErr != nil {
		return nil, f.indicatorDefinitionsErr
	}
	if f.indicatorDefinitionsResp != nil {
		return f.indicatorDefinitionsResp, nil
	}
	return &portfoliov1.ListStrategyIndicatorsResponse{}, nil
}

func (f *fakeSessionPortfoliosClient) ListStrategyIndicatorChunks(_ context.Context, in *portfoliov1.ListStrategyIndicatorChunksRequest, _ ...grpc.CallOption) (*portfoliov1.ListStrategyIndicatorChunksResponse, error) {
	f.lastIndicatorChunksReq = in
	if f.indicatorChunksErr != nil {
		return nil, f.indicatorChunksErr
	}
	if f.indicatorChunksResp != nil {
		return f.indicatorChunksResp, nil
	}
	return &portfoliov1.ListStrategyIndicatorChunksResponse{}, nil
}

func (f *fakeSessionPortfoliosClient) ListStrategyIndicatorsV2(_ context.Context, in *portfoliov1.ListStrategyIndicatorsV2Request, _ ...grpc.CallOption) (*portfoliov1.ListStrategyIndicatorsV2Response, error) {
	f.lastIndicatorDefinitionsV2Req = in
	if f.indicatorDefinitionsV2Err != nil {
		return nil, f.indicatorDefinitionsV2Err
	}
	if f.indicatorDefinitionsV2Resp != nil {
		return f.indicatorDefinitionsV2Resp, nil
	}
	return &portfoliov1.ListStrategyIndicatorsV2Response{}, nil
}

func (f *fakeSessionPortfoliosClient) ListStrategyIndicatorChunksV2(_ context.Context, in *portfoliov1.ListStrategyIndicatorChunksV2Request, _ ...grpc.CallOption) (*portfoliov1.ListStrategyIndicatorChunksV2Response, error) {
	f.lastIndicatorChunksV2Req = in
	if f.indicatorChunksV2Err != nil {
		return nil, f.indicatorChunksV2Err
	}
	if f.indicatorChunksV2Resp != nil {
		return f.indicatorChunksV2Resp, nil
	}
	return &portfoliov1.ListStrategyIndicatorChunksV2Response{}, nil
}

// Reuse ``fakeOrdersClient`` defined in order_history_test.go — it already
// captures ``lastReq`` + returns a canned ``resp``.

// ── session list handler ─────────────────────────────────────────────────

func TestListSessions_IncludesRuntimeAndDebugMetadata(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		listSessionsResp: &portfoliov1.ListSessionsResponse{
			Sessions: []*portfoliov1.StrategySessionEntry{
				{
					SessionId:      "debug-1",
					PortfolioId:    7,
					Environment:    0,
					Status:         "finished",
					Interval:       "1m",
					BarsProcessed:  10,
					RuntimeId:      "rt-debug",
					RuntimeSource:  "self_hosted",
					RuntimeName:    "debugger-box",
					SessionType:    "debugging",
					RuntimeVersion: "0.1.0",
					SessionName:    "debug-debugger-box-20260522-134717",
				},
			},
		},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions?portfolio_id=7&limit=5", nil), 42)
	rec := httptest.NewRecorder()
	s.listSessionsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := acct.lastListSessionsReq.GetUserId(); got != 42 {
		t.Errorf("user_id = %d, want 42", got)
	}

	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(body))
	}
	if got := body[0]["session_type"]; got != "debugging" {
		t.Errorf("session_type = %v, want debugging", got)
	}
	if got := body[0]["session_name"]; got != "debug-debugger-box-20260522-134717" {
		t.Errorf("session_name = %v, want debug-debugger-box-20260522-134717", got)
	}
	if got := body[0]["runtime_version"]; got != "0.1.0" {
		t.Errorf("runtime_version = %v, want 0.1.0", got)
	}
}

func TestProtoSessionToJSONPreservesStructuredErrorFacts(t *testing.T) {
	detail := `{"code":"SPOT_MIN_NOTIONAL","route":"binance/spot","symbol":"BTCUSDT","filter_type":"MIN_NOTIONAL","environment":1,"retryable":false,"source":"risk","message":"notional below minimum"}`
	encoded, err := json.Marshal(protoSessionToJSON(&portfoliov1.StrategySessionEntry{
		SessionId:                    "spot-preflight",
		Environment:                  1,
		Status:                       "preflight_failed",
		ErrorCode:                    "SPOT_MIN_NOTIONAL",
		ErrorMessage:                 "notional below minimum",
		ErrorDetailJson:              detail,
		IndicatorFinalizationPending: true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Environment                  int32           `json:"environment"`
		ErrorCode                    string          `json:"error_code"`
		ErrorMessage                 string          `json:"error_message"`
		ErrorDetail                  json.RawMessage `json:"error_detail"`
		ErrorDetailJSON              string          `json:"error_detail_json"`
		IndicatorFinalizationPending bool            `json:"indicator_finalization_pending"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if body.Environment != 1 || body.ErrorCode != "SPOT_MIN_NOTIONAL" || body.ErrorMessage != "notional below minimum" || body.ErrorDetailJSON != detail {
		t.Fatalf("session error=%#v JSON=%s", body, encoded)
	}
	if !body.IndicatorFinalizationPending {
		t.Fatalf("indicator_finalization_pending lost: %s", encoded)
	}
	var facts struct {
		Code        string `json:"code"`
		Route       string `json:"route"`
		Symbol      string `json:"symbol"`
		FilterType  string `json:"filter_type"`
		Environment int32  `json:"environment"`
		Retryable   bool   `json:"retryable"`
		Source      string `json:"source"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(body.ErrorDetail, &facts); err != nil {
		t.Fatalf("structured error=%s: %v", body.ErrorDetail, err)
	}
	if facts.Code != "SPOT_MIN_NOTIONAL" || facts.Route != "binance/spot" || facts.Symbol != "BTCUSDT" || facts.FilterType != "MIN_NOTIONAL" ||
		facts.Environment != 1 || facts.Retryable || facts.Source != "risk" || facts.Message != "notional below minimum" {
		t.Fatalf("structured facts=%#v", facts)
	}
}

func TestProtoSessionToJSONEmitsExplicitFalseIndicatorFinalizationPending(
	t *testing.T,
) {
	encoded, err := json.Marshal(protoSessionToJSON(
		&portfoliov1.StrategySessionEntry{
			SessionId: "finished-session",
			Status:    "finished",
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	value, exists := body["indicator_finalization_pending"]
	if !exists || value != false {
		t.Fatalf(
			"indicator_finalization_pending = %#v (exists=%t), JSON=%s",
			value,
			exists,
			encoded,
		)
	}
}

// ── snapshots handler ────────────────────────────────────────────────────

func TestGetSessionSnapshots_DefaultLimitOffsetAndPagedShape(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		snapshotsResp: &portfoliov1.ListSessionSnapshotsResponse{
			Items: []*portfoliov1.SnapshotEntry{
				{Time: timestamppb.Now(), PortfolioId: 1, SnapshotReason: 2},
			},
			NextOffset: 1,
			HasMore:    false,
		},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/snapshots", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionSnapshots(rec, req, "sess-1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// Default paging contract: limit=20, offset=0.
	if got := acct.lastSnapshotsReq.GetLimit(); got != 20 {
		t.Errorf("grpc request limit = %d, want 20 (default)", got)
	}
	if got := acct.lastSnapshotsReq.GetOffset(); got != 0 {
		t.Errorf("grpc request offset = %d, want 0 (default)", got)
	}

	var body struct {
		Items      []map[string]any `json:"items"`
		NextOffset int32            `json:"next_offset"`
		HasMore    bool             `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 {
		t.Errorf("items len = %d, want 1", len(body.Items))
	}
	if body.NextOffset != 1 {
		t.Errorf("next_offset = %d, want 1", body.NextOffset)
	}
	if body.HasMore {
		t.Errorf("has_more = true, want false")
	}
}

func TestGetSessionSnapshots_LimitOversizedIsClampedTo200(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		snapshotsResp: &portfoliov1.ListSessionSnapshotsResponse{},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/snapshots?limit=10000&offset=5", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionSnapshots(rec, req, "sess-1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := acct.lastSnapshotsReq.GetLimit(); got != 200 {
		t.Errorf("limit = %d, want clamped to 200", got)
	}
	if got := acct.lastSnapshotsReq.GetOffset(); got != 5 {
		t.Errorf("offset = %d, want 5", got)
	}
}

func TestGetSessionSnapshots_NegativeOrZeroLimitFallsBackToDefault(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		snapshotsResp: &portfoliov1.ListSessionSnapshotsResponse{},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	for _, raw := range []string{"0", "-5", "abc"} {
		req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/snapshots?limit="+raw, nil), 7)
		rec := httptest.NewRecorder()
		s.getSessionSnapshots(rec, req, "sess-1")
		if rec.Code != http.StatusOK {
			t.Fatalf("limit=%q: status = %d, want 200", raw, rec.Code)
		}
		if got := acct.lastSnapshotsReq.GetLimit(); got != 20 {
			t.Errorf("limit=%q: grpc limit = %d, want 20 (default fallback)", raw, got)
		}
	}
}

func TestSessionOrderLifecycleEndpointReturnsEvents(t *testing.T) {
	occurred := timestamppb.Now()
	acct := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId:   "sess-1",
			UserId:      7,
			PortfolioId: 42,
			RuntimeId:   "rt-1",
		}},
	}
	orders := &fakeOrdersClient{
		lifecycleResp: &orderv1.ListOrderLifecycleEventsResponse{
			Events: []*orderv1.OrderLifecycleEventEntry{
				{
					EventId:         101,
					SessionId:       "sess-1",
					PortfolioId:     42,
					VenueId:         88,
					IntentId:        "intent-1",
					AttemptId:       "attempt-1",
					OrderId:         "order-1",
					ExchangeOrderId: "ex-order-1",
					ExchangeTradeId: "trade-1",
					EventType:       "fill",
					OrderStatus:     "PARTIALLY_FILLED",
					Environment:     1,
					Exchange:        1,
					Market:          2,
					PositionSide:    1,
					Side:            "BUY",
					OccurredAt:      occurred,
					CreatedAt:       occurred,
					FillDelta: &orderv1.FillDeltaEntry{
						Symbol: "ETHUSDT", Qty: 0.25, FillPrice: 3100, Fee: 0.12, FeeAsset: "USDT", TradeTime: occurred,
						QtyDecimal: "0.25000000", FillPriceDecimal: "3100.00000001", FeeDecimal: "0.12000000", QuoteQtyDecimal: "775.00000000",
					},
					OrderState: &orderv1.OrderStateEntry{
						ExchangeOrderId: "ex-order-1",
						Symbol:          "ETHUSDT",
						Status:          "PARTIALLY_FILLED",
						OrigQty:         1,
						ExecutedQty:     0.25,
						RemainingQty:    0.75,
						AvgPrice:        3100,
						UpdatedAt:       occurred,
						OrigQtyDecimal:  "1.00000000", ExecutedQtyDecimal: "0.25000000", RemainingQtyDecimal: "0.75000000",
						AvgPriceDecimal: "3100.00000001", CumulativeQuoteQtyDecimal: "775.00000000",
					},
				},
			},
		},
	}
	s := &server{portfolios: acct, orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/lifecycle-events?after_event_id=99&limit=5", nil), 7)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if acct.lastGetSessionReq == nil || acct.lastGetSessionReq.GetSessionId() != "sess-1" || acct.lastGetSessionReq.GetUserId() != 7 {
		t.Fatalf("session authorization request = %+v", acct.lastGetSessionReq)
	}
	if orders.lastLifecycleReq == nil {
		t.Fatal("ListOrderLifecycleEvents was not called")
	}
	if orders.lastLifecycleReq.GetSessionId() != "sess-1" || orders.lastLifecycleReq.GetAfterEventId() != 99 || orders.lastLifecycleReq.GetLimit() != 5 {
		t.Fatalf("lifecycle request = %+v", orders.lastLifecycleReq)
	}

	var body struct {
		Items []struct {
			EventID       int64  `json:"event_id"`
			EventType     string `json:"event_type"`
			OrderStatus   string `json:"order_status"`
			ExchangeLabel string `json:"exchange_label"`
			MarketLabel   string `json:"market_label"`
			PositionSide  string `json:"position_side"`
			Side          string `json:"side"`
			FillDelta     struct {
				Symbol           string  `json:"symbol"`
				Qty              float64 `json:"qty"`
				QtyDecimal       string  `json:"qty_decimal"`
				FillPriceDecimal string  `json:"fill_price_decimal"`
				FeeDecimal       string  `json:"fee_decimal"`
				QuoteQtyDecimal  string  `json:"quote_qty_decimal"`
			} `json:"fill_delta"`
			OrderState struct {
				Status                    string  `json:"status"`
				ExecutedQty               float64 `json:"executed_qty"`
				RemainingQty              float64 `json:"remaining_qty"`
				ExecutedQtyDecimal        string  `json:"executed_qty_decimal"`
				RemainingQtyDecimal       string  `json:"remaining_qty_decimal"`
				CumulativeQuoteQtyDecimal string  `json:"cumulative_quote_qty_decimal"`
			} `json:"order_state"`
		} `json:"items"`
		NextEventID int64 `json:"next_event_id"`
		NextOffset  int64 `json:"next_offset"`
		HasMore     bool  `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(body.Items))
	}
	item := body.Items[0]
	if item.EventID != 101 || item.EventType != "fill" || item.OrderStatus != "PARTIALLY_FILLED" {
		t.Fatalf("item = %+v", item)
	}
	if item.FillDelta.QtyDecimal != "0.25000000" || item.FillDelta.FillPriceDecimal != "3100.00000001" || item.FillDelta.FeeDecimal != "0.12000000" || item.FillDelta.QuoteQtyDecimal != "775.00000000" {
		t.Fatalf("fill delta exact decimals = %+v", item.FillDelta)
	}
	if item.OrderState.ExecutedQtyDecimal != "0.25000000" || item.OrderState.RemainingQtyDecimal != "0.75000000" || item.OrderState.CumulativeQuoteQtyDecimal != "775.00000000" {
		t.Fatalf("order state exact decimals = %+v", item.OrderState)
	}
	if item.ExchangeLabel != "binance" || item.MarketLabel != "perpetual_futures" || item.PositionSide != "LONG" || item.Side != "BUY" {
		t.Fatalf("route facts = %+v", item)
	}
	if item.FillDelta.Symbol != "ETHUSDT" || item.FillDelta.Qty != 0.25 || item.OrderState.ExecutedQty != 0.25 || item.OrderState.RemainingQty != 0.75 {
		t.Fatalf("fill/state = %+v", item)
	}
	if body.NextEventID != 101 || body.NextOffset != 101 || body.HasMore {
		t.Fatalf("page = next_event_id:%d next_offset:%d has_more:%t", body.NextEventID, body.NextOffset, body.HasMore)
	}
}

func TestStoppingFailedStatusReturnedToFrontend(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		getSessionResp: &portfoliov1.GetSessionResponse{Session: &portfoliov1.StrategySessionEntry{
			SessionId:   "sess-stop-failed",
			UserId:      7,
			PortfolioId: 42,
			Status:      "stopping_failed",
			Error:       "manual exchange close required",
			RuntimeId:   "rt-1",
		}},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/sess-stop-failed", nil), 7)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "stopping_failed" || body.Error != "manual exchange close required" {
		t.Fatalf("session status/error = %+v", body)
	}
}

// ── reconciliation handler ──────────────────────────────────────────────

func TestGetSessionReconciliation_PagedShapeAndCustomPaging(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		reconciliationResp: &portfoliov1.ListReconciliationRunsResponse{
			Items: []*portfoliov1.ReconciliationRunEntry{
				{RunId: "r-1", HardPass: true, SoftPass: true},
				{RunId: "r-2", HardPass: false, SoftPass: true},
			},
			NextOffset: 12,
			HasMore:    true,
		},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-42/reconciliation?limit=10&offset=10", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionReconciliation(rec, req, "s-42")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := acct.lastReconciliationReq.GetLimit(); got != 10 {
		t.Errorf("grpc limit = %d, want 10", got)
	}
	if got := acct.lastReconciliationReq.GetOffset(); got != 10 {
		t.Errorf("grpc offset = %d, want 10", got)
	}

	var body struct {
		Items      []map[string]any `json:"items"`
		NextOffset int32            `json:"next_offset"`
		HasMore    bool             `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 {
		t.Errorf("items len = %d, want 2", len(body.Items))
	}
	if body.NextOffset != 12 {
		t.Errorf("next_offset = %d, want 12", body.NextOffset)
	}
	if !body.HasMore {
		t.Errorf("has_more = false, want true")
	}
}

// ── orders handler (option B: gateway computes has_more from total) ─────

func TestGetSessionOrders_HasMoreComputedFromTotal(t *testing.T) {
	orders := &fakeOrdersClient{
		ordersResp: &orderv1.QueryOrdersResponse{
			Orders: []*orderv1.ExchangeOrderEntry{
				{OrderId: "o-1", Symbol: "BTCUSDT"},
				{OrderId: "o-2", Symbol: "BTCUSDT"},
			},
			Total: 50, // 50 total rows matched, we're returning 2 from offset=0
		},
	}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-order/orders?limit=2&offset=0", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionOrders(rec, req, "s-order")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := orders.lastOrdersReq.GetLimit(); got != 2 {
		t.Errorf("grpc limit = %d, want 2", got)
	}
	if got := orders.lastOrdersReq.GetOffset(); got != 0 {
		t.Errorf("grpc offset = %d, want 0", got)
	}

	var body struct {
		Items      []map[string]any `json:"items"`
		NextOffset int32            `json:"next_offset"`
		HasMore    bool             `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 {
		t.Errorf("items len = %d, want 2", len(body.Items))
	}
	if body.NextOffset != 2 {
		t.Errorf("next_offset = %d, want 2", body.NextOffset)
	}
	// 2 returned + 0 offset < 50 total → has_more must be true.
	if !body.HasMore {
		t.Errorf("has_more = false, want true")
	}
}

func TestSessionOrderAuditHandlersPreserveExactDecimalFields(t *testing.T) {
	requestedPrice := "0.00000001"
	orderPrice := "50000.00000001"
	orders := &fakeOrdersClient{
		intentsResp: &orderv1.QueryOrderIntentsResponse{Intents: []*orderv1.OrderIntentEntry{{
			IntentId: "i-exact", Environment: 1, RequestedQtyDecimal: "9007199254740993.00000000", RequestedPriceDecimal: &requestedPrice,
		}}},
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{Attempts: []*orderv1.OrderAttemptEntry{{
			AttemptId: "a-exact", Environment: 1, RequestedQtyDecimal: "9007199254740993.00000000", RequestedPriceDecimal: &requestedPrice, MarkPriceDecimal: "50000.00000001",
		}}},
		ordersResp: &orderv1.QueryOrdersResponse{Orders: []*orderv1.ExchangeOrderEntry{{
			OrderId: "o-exact", Environment: 1, OrigQtyDecimal: "9007199254740993.00000000", ExecutedQtyDecimal: "0.00000001",
			RemainingQtyDecimal: "0.09999999", AvgPriceDecimal: "50000.00000001", PriceDecimal: &orderPrice,
			CumulativeQuoteQtyDecimal: "123456789.00000001",
		}}},
		fillsResp: &orderv1.QueryOrderFillsResponse{Fills: []*orderv1.OrderFillEntry{{
			FillId: "f-exact", Environment: 1, FeeAsset: "BNB", QtyDecimal: "0.00000001", FillPriceDecimal: "50000.00000001",
			FeeDecimal: "0.00100000", QuoteQtyDecimal: "0.00050000",
		}}},
	}
	s := &server{orders: orders}

	assert := func(path string, handler func(http.ResponseWriter, *http.Request, string), want map[string]string) {
		t.Helper()
		req := withUID(httptest.NewRequest(http.MethodGet, path, nil), 7)
		rec := httptest.NewRecorder()
		handler(rec, req, "sess-exact")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 1 {
			t.Fatalf("%s items=%#v", path, body.Items)
		}
		for field, value := range want {
			if got := body.Items[0][field]; got != value {
				t.Errorf("%s %s=%#v want %q", path, field, got, value)
			}
		}
		if got := body.Items[0]["environment"]; got != float64(1) {
			t.Errorf("%s environment=%#v want=1", path, got)
		}
	}

	assert("/api/sessions/sess-exact/intents", s.getSessionIntents, map[string]string{
		"requested_qty_decimal": "9007199254740993.00000000", "requested_price_decimal": requestedPrice,
	})
	assert("/api/sessions/sess-exact/attempts", s.getSessionAttempts, map[string]string{
		"requested_qty_decimal": "9007199254740993.00000000", "requested_price_decimal": requestedPrice, "mark_price_decimal": "50000.00000001",
	})
	assert("/api/sessions/sess-exact/orders", s.getSessionOrders, map[string]string{
		"orig_qty_decimal": "9007199254740993.00000000", "executed_qty_decimal": "0.00000001", "remaining_qty_decimal": "0.09999999",
		"avg_price_decimal": "50000.00000001", "price_decimal": orderPrice, "cumulative_quote_qty_decimal": "123456789.00000001",
	})
	assert("/api/sessions/sess-exact/fills", s.getSessionFills, map[string]string{
		"qty_decimal": "0.00000001", "fill_price_decimal": "50000.00000001", "fee_decimal": "0.00100000",
		"quote_qty_decimal": "0.00050000", "fee_asset": "BNB",
	})
}

func TestGetSessionOrders_LastPageReportsHasMoreFalse(t *testing.T) {
	orders := &fakeOrdersClient{
		ordersResp: &orderv1.QueryOrdersResponse{
			Orders: []*orderv1.ExchangeOrderEntry{{OrderId: "o-last"}},
			Total:  5,
		},
	}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	// Returning 1 item at offset=4 → 4+1=5=total, so no more pages.
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-order/orders?limit=20&offset=4", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionOrders(rec, req, "s-order")

	var body struct {
		NextOffset int32 `json:"next_offset"`
		HasMore    bool  `json:"has_more"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.NextOffset != 5 {
		t.Errorf("next_offset = %d, want 5", body.NextOffset)
	}
	if body.HasMore {
		t.Errorf("has_more = true, want false (next_offset == total)")
	}
}

func TestGetSessionAttempts_HasMoreComputedFromTotal(t *testing.T) {
	orders := &fakeOrdersClient{
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{
			Attempts: []*orderv1.OrderAttemptEntry{
				{AttemptId: "a-1", Symbol: "BTCUSDT"},
				{AttemptId: "a-2", Symbol: "BTCUSDT"},
			},
			Total: 50,
		},
	}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-order/attempts?limit=2&offset=0", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionAttempts(rec, req, "s-order")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := orders.lastAttemptsReq.GetLimit(); got != 2 {
		t.Errorf("grpc limit = %d, want 2", got)
	}
	if got := orders.lastAttemptsReq.GetOffset(); got != 0 {
		t.Errorf("grpc offset = %d, want 0", got)
	}

	var body struct {
		Items      []map[string]any `json:"items"`
		NextOffset int32            `json:"next_offset"`
		HasMore    bool             `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 || body.NextOffset != 2 || !body.HasMore {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestGetSessionFills_HasMoreComputedFromTotal(t *testing.T) {
	orders := &fakeOrdersClient{
		fillsResp: &orderv1.QueryOrderFillsResponse{
			Fills: []*orderv1.OrderFillEntry{
				{FillId: "f-1", Symbol: "BTCUSDT"},
				{FillId: "f-2", Symbol: "BTCUSDT"},
			},
			Total: 50,
		},
	}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-order/fills?limit=2&offset=0", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionFills(rec, req, "s-order")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := orders.lastFillsReq.GetLimit(); got != 2 {
		t.Errorf("grpc limit = %d, want 2", got)
	}
	if got := orders.lastFillsReq.GetOffset(); got != 0 {
		t.Errorf("grpc offset = %d, want 0", got)
	}

	var body struct {
		Items      []map[string]any `json:"items"`
		NextOffset int32            `json:"next_offset"`
		HasMore    bool             `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 || body.NextOffset != 2 || !body.HasMore {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestSessionOrderAuditHandlersExposeVenueRouteFacts(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*server, http.ResponseWriter, *http.Request, string)
		orders  *fakeOrdersClient
		path    string
	}{
		{
			name: "intents",
			handler: func(s *server, w http.ResponseWriter, r *http.Request, sessionID string) {
				s.getSessionIntents(w, r, sessionID)
			},
			orders: &fakeOrdersClient{intentsResp: &orderv1.QueryOrderIntentsResponse{
				Intents: []*orderv1.OrderIntentEntry{{
					IntentId: "intent-1", Symbol: "ETHUSDT", Market: 3,
					VenueId: 77, Exchange: 2, PositionSide: 2,
				}},
				Total: 1,
			}},
			path: "/api/sessions/s-route/intents",
		},
		{
			name: "attempts",
			handler: func(s *server, w http.ResponseWriter, r *http.Request, sessionID string) {
				s.getSessionAttempts(w, r, sessionID)
			},
			orders: &fakeOrdersClient{attemptsResp: &orderv1.QueryOrderAttemptsResponse{
				Attempts: []*orderv1.OrderAttemptEntry{{
					AttemptId: "attempt-1", Symbol: "ETHUSDT", Market: 3,
					VenueId: 77, Exchange: 2, PositionSide: 2,
				}},
				Total: 1,
			}},
			path: "/api/sessions/s-route/attempts",
		},
		{
			name: "orders",
			handler: func(s *server, w http.ResponseWriter, r *http.Request, sessionID string) {
				s.getSessionOrders(w, r, sessionID)
			},
			orders: &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{
				Orders: []*orderv1.ExchangeOrderEntry{{
					OrderId: "order-1", Symbol: "ETHUSDT", Market: 3,
					VenueId: 77, Exchange: 2, PositionSide: 2,
				}},
				Total: 1,
			}},
			path: "/api/sessions/s-route/orders",
		},
		{
			name: "fills",
			handler: func(s *server, w http.ResponseWriter, r *http.Request, sessionID string) {
				s.getSessionFills(w, r, sessionID)
			},
			orders: &fakeOrdersClient{fillsResp: &orderv1.QueryOrderFillsResponse{
				Fills: []*orderv1.OrderFillEntry{{
					FillId: "fill-1", OrderId: "order-1", Symbol: "ETHUSDT", Market: 3,
					VenueId: 77, Exchange: 2, PositionSide: 2,
				}},
				Total: 1,
			}},
			path: "/api/sessions/s-route/fills",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{orders: tt.orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}
			req := withUID(httptest.NewRequest(http.MethodGet, tt.path, nil), 7)
			rec := httptest.NewRecorder()

			tt.handler(s, rec, req, "s-route")

			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Items []struct {
					VenueID       int64  `json:"venue_id"`
					Exchange      int32  `json:"exchange"`
					ExchangeLabel string `json:"exchange_label"`
					Market        string `json:"market"`
					MarketLabel   string `json:"market_label"`
					PositionSide  string `json:"position_side"`
				} `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
			}
			if len(body.Items) != 1 {
				t.Fatalf("items len=%d, want 1", len(body.Items))
			}
			got := body.Items[0]
			if got.VenueID != 77 || got.Exchange != 2 || got.ExchangeLabel != "okx" ||
				got.Market != "delivery_futures" || got.MarketLabel != "delivery_futures" ||
				got.PositionSide != "SHORT" {
				t.Fatalf("route facts not exposed: %+v", got)
			}
		})
	}
}

// ── response shape invariant (spec §Paginated response SHALL be structurally distinguishable) ──

func TestAuditListHandlers_ReturnJSONObjectNotArray(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		snapshotsResp:      &portfoliov1.ListSessionSnapshotsResponse{},
		reconciliationResp: &portfoliov1.ListReconciliationRunsResponse{},
	}
	orders := &fakeOrdersClient{
		intentsResp:  &orderv1.QueryOrderIntentsResponse{},
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{},
		ordersResp:   &orderv1.QueryOrdersResponse{},
		fillsResp:    &orderv1.QueryOrderFillsResponse{},
	}
	s := &server{portfolios: acct, orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	type handler func(http.ResponseWriter, *http.Request, string)
	handlers := []struct {
		name string
		path string
		fn   handler
	}{
		{"snapshots", "/api/sessions/s/snapshots", s.getSessionSnapshots},
		{"reconciliation", "/api/sessions/s/reconciliation", s.getSessionReconciliation},
		{"intents", "/api/sessions/s/intents", s.getSessionIntents},
		{"attempts", "/api/sessions/s/attempts", s.getSessionAttempts},
		{"orders", "/api/sessions/s/orders", s.getSessionOrders},
		{"fills", "/api/sessions/s/fills", s.getSessionFills},
	}
	for _, h := range handlers {
		req := withUID(httptest.NewRequest(http.MethodGet, h.path, nil), 7)
		rec := httptest.NewRecorder()
		h.fn(rec, req, "s")

		// Response root MUST be a JSON object (map), never a bare array.
		var asMap map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &asMap); err != nil {
			t.Errorf("%s: response is not a JSON object: %v — body=%s", h.name, err, rec.Body.String())
			continue
		}
		for _, key := range []string{"items", "next_offset", "has_more", "total"} {
			if _, ok := asMap[key]; !ok {
				t.Errorf("%s: response missing required paged-contract key %q", h.name, key)
			}
		}
	}
}

// ── intents handler ─────────────────────────────────────────────────────

func TestGetSessionIntents_HasMoreComputedFromTotal(t *testing.T) {
	orders := &fakeOrdersClient{
		intentsResp: &orderv1.QueryOrderIntentsResponse{
			Intents: []*orderv1.OrderIntentEntry{
				{
					IntentId:      "i-1",
					Symbol:        "BTCUSDT",
					Side:          "BUY",
					RequestedQty:  1,
					Status:        "REJECTED",
					RejectCode:    "MIN_NOTIONAL_VIOLATION",
					RejectMessage: "notional 16.8809 is below min_notional 20",
				},
				{IntentId: "i-2", Symbol: "ETHUSDT", Side: "SELL", RequestedQty: 2},
			},
			Total: 50,
		},
	}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-x/intents?limit=2&offset=0", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionIntents(rec, req, "s-x")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := orders.lastIntentsReq.GetLimit(); got != 2 {
		t.Errorf("grpc limit = %d, want 2", got)
	}
	if got := orders.lastIntentsReq.GetSessionId(); got != "s-x" {
		t.Errorf("grpc session_id = %q, want s-x", got)
	}

	var body struct {
		Items      []map[string]any `json:"items"`
		NextOffset int32            `json:"next_offset"`
		HasMore    bool             `json:"has_more"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 2 || body.NextOffset != 2 || !body.HasMore {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Items[0]["intent_id"] != "i-1" {
		t.Errorf("first item intent_id = %v, want i-1", body.Items[0]["intent_id"])
	}
	if body.Items[0]["reject_code"] != "MIN_NOTIONAL_VIOLATION" || body.Items[0]["reject_message"] != "notional 16.8809 is below min_notional 20" {
		t.Errorf("first item reject fields = %+v", body.Items[0])
	}
}

// ── ancestor-id filters forwarded to RPC ─────────────────────────────────

func TestGetSessionAttempts_ForwardsIntentID(t *testing.T) {
	orders := &fakeOrdersClient{attemptsResp: &orderv1.QueryOrderAttemptsResponse{}}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-x/attempts?intent_id=I-7", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionAttempts(rec, req, "s-x")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := orders.lastAttemptsReq.GetIntentId(); got != "I-7" {
		t.Errorf("grpc intent_id = %q, want I-7", got)
	}
}

func TestGetSessionOrders_ForwardsAttemptID(t *testing.T) {
	orders := &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{}}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-x/orders?intent_id=I-7&attempt_id=A-3", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionOrders(rec, req, "s-x")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := orders.lastOrdersReq.GetIntentId(); got != "I-7" {
		t.Errorf("grpc intent_id = %q, want I-7", got)
	}
	if got := orders.lastOrdersReq.GetAttemptId(); got != "A-3" {
		t.Errorf("grpc attempt_id = %q, want A-3", got)
	}
}

func TestGetSessionFills_ForwardsOrderID(t *testing.T) {
	orders := &fakeOrdersClient{fillsResp: &orderv1.QueryOrderFillsResponse{}}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-x/fills?order_id=O-5", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionFills(rec, req, "s-x")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := orders.lastFillsReq.GetOrderId(); got != "O-5" {
		t.Errorf("grpc order_id = %q, want O-5", got)
	}
}

// ── routing through handleSessions ───────────────────────────────────────

func TestHandleSessions_RoutesIntents(t *testing.T) {
	orders := &fakeOrdersClient{intentsResp: &orderv1.QueryOrderIntentsResponse{}}
	s := &server{orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-x/intents", nil), 7)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if orders.lastIntentsReq == nil {
		t.Fatalf("expected QueryOrderIntents to be called")
	}
}

// ── total threading on existing list endpoints ─────────────────────────────

func TestGetSessionSnapshots_TotalThreaded(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		snapshotsResp: &portfoliov1.ListSessionSnapshotsResponse{
			Items:      []*portfoliov1.SnapshotEntry{{PortfolioId: 1}},
			NextOffset: 1,
			HasMore:    true,
			Total:      57,
		},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/snapshots", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionSnapshots(rec, req, "s-1")

	var body struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 57 {
		t.Errorf("total = %d, want 57", body.Total)
	}
}

func TestGetSessionReconciliation_TotalThreaded(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		reconciliationResp: &portfoliov1.ListReconciliationRunsResponse{
			Items:      []*portfoliov1.ReconciliationRunEntry{{RunId: "r-1"}},
			NextOffset: 1,
			HasMore:    true,
			Total:      99,
		},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/reconciliation", nil), 7)
	rec := httptest.NewRecorder()
	s.getSessionReconciliation(rec, req, "s-1")

	var body struct {
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 99 {
		t.Errorf("total = %d, want 99", body.Total)
	}
}

// ── reconciliation summary endpoint ────────────────────────────────────────

func TestGetSessionReconciliationSummary_HappyPath(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		reconciliationSummaryResp: &portfoliov1.GetSessionReconciliationSummaryResponse{
			TotalRuns:    53,
			HardFailRuns: 7,
			SoftFailRuns: 12,
		},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/reconciliation/summary", nil), 7)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if acct.lastReconciliationSummaryReq == nil {
		t.Fatalf("expected GetSessionReconciliationSummary RPC to be called")
	}
	if got := acct.lastReconciliationSummaryReq.GetSessionId(); got != "s-1" {
		t.Errorf("grpc session_id = %q, want s-1", got)
	}
	if got := acct.lastReconciliationSummaryReq.GetUserId(); got != 7 {
		t.Errorf("grpc user_id = %d, want 7", got)
	}

	var body struct {
		TotalRuns    int64 `json:"total_runs"`
		HardFailRuns int64 `json:"hard_fail_runs"`
		SoftFailRuns int64 `json:"soft_fail_runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TotalRuns != 53 || body.HardFailRuns != 7 || body.SoftFailRuns != 12 {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestGetSessionReconciliationSummary_RequiresAuth(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		reconciliationSummaryResp: &portfoliov1.GetSessionReconciliationSummaryResponse{},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	// No withUID → no user context → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/reconciliation/summary", nil)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetSessionReconciliationSummary_MethodNotAllowed(t *testing.T) {
	s := &server{portfolios: &fakeSessionPortfoliosClient{}, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := withUID(httptest.NewRequest(method, "/api/sessions/s-1/reconciliation/summary", nil), 7)
		rec := httptest.NewRecorder()
		s.handleSessions(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method=%s: status = %d, want 405", method, rec.Code)
		}
	}
}
