package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeOrdersClient records the last QueryOrders / QueryOrderFills request and
// returns a preset response. Other RPCs panic as nil-interface calls, which is
// the desired behavior — a wrong RPC choice becomes an immediate test failure.
type fakeOrdersClient struct {
	orderv1.OrderServiceClient // unused methods

	lastIntentsReq   *orderv1.QueryOrderIntentsRequest
	intentsResp      *orderv1.QueryOrderIntentsResponse
	lastAttemptsReq  *orderv1.QueryOrderAttemptsRequest
	attemptsResp     *orderv1.QueryOrderAttemptsResponse
	lastOrdersReq    *orderv1.QueryOrdersRequest
	ordersResp       *orderv1.QueryOrdersResponse
	lastFillsReq     *orderv1.QueryOrderFillsRequest
	fillsResp        *orderv1.QueryOrderFillsResponse
	lastLifecycleReq *orderv1.ListOrderLifecycleEventsRequest
	lifecycleResp    *orderv1.ListOrderLifecycleEventsResponse
	err              error
}

func (f *fakeOrdersClient) QueryOrderIntents(_ context.Context, in *orderv1.QueryOrderIntentsRequest, _ ...grpc.CallOption) (*orderv1.QueryOrderIntentsResponse, error) {
	f.lastIntentsReq = in
	return f.intentsResp, f.err
}

func (f *fakeOrdersClient) QueryOrderAttempts(_ context.Context, in *orderv1.QueryOrderAttemptsRequest, _ ...grpc.CallOption) (*orderv1.QueryOrderAttemptsResponse, error) {
	f.lastAttemptsReq = in
	return f.attemptsResp, f.err
}

func (f *fakeOrdersClient) QueryOrders(_ context.Context, in *orderv1.QueryOrdersRequest, _ ...grpc.CallOption) (*orderv1.QueryOrdersResponse, error) {
	f.lastOrdersReq = in
	return f.ordersResp, f.err
}

func (f *fakeOrdersClient) QueryOrderFills(_ context.Context, in *orderv1.QueryOrderFillsRequest, _ ...grpc.CallOption) (*orderv1.QueryOrderFillsResponse, error) {
	f.lastFillsReq = in
	return f.fillsResp, f.err
}

func (f *fakeOrdersClient) ListOrderLifecycleEvents(_ context.Context, in *orderv1.ListOrderLifecycleEventsRequest, _ ...grpc.CallOption) (*orderv1.ListOrderLifecycleEventsResponse, error) {
	f.lastLifecycleReq = in
	return f.lifecycleResp, f.err
}

func newOrderHistoryServer(fake *fakeOrdersClient) *server {
	return &server{
		orders:      fake,
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
	}
}

func withOrderUID(r *http.Request, uid int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userIDContextKey, uid))
}

// ────────────────────────────────────────────────────────────────────────────

func TestOptionalExactDecimalOrLegacyPreservesAbsentMarketPrice(t *testing.T) {
	if got := optionalExactDecimalOrLegacy(nil, 0); got != "" {
		t.Fatalf("absent optional price = %q, want empty", got)
	}
	if got := optionalExactDecimalOrLegacy(nil, 12.5); got != "12.5" {
		t.Fatalf("legacy LIMIT price = %q, want 12.5", got)
	}
	exact := "9007199254740993.00000000"
	if got := optionalExactDecimalOrLegacy(&exact, 0); got != exact {
		t.Fatalf("exact price = %q, want %q", got, exact)
	}
}

func TestMarketOrderHistoryOmitsAbsentOptionalPrices(t *testing.T) {
	fake := &fakeOrdersClient{
		intentsResp:  &orderv1.QueryOrderIntentsResponse{Intents: []*orderv1.OrderIntentEntry{{IntentId: "intent-market"}}, Total: 1},
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{Attempts: []*orderv1.OrderAttemptEntry{{AttemptId: "attempt-market"}}, Total: 1},
		ordersResp:   &orderv1.QueryOrdersResponse{Orders: []*orderv1.ExchangeOrderEntry{{OrderId: "order-market"}}, Total: 1},
	}
	s := newOrderHistoryServer(fake)
	cases := []struct {
		name string
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{name: "global intent", path: "/api/orders/intents", call: s.handleOrderIntents},
		{name: "global attempt", path: "/api/orders/attempts", call: s.handleOrderAttempts},
		{name: "global order", path: "/api/orders", call: s.handleOrderHistory},
		{name: "session intent", path: "/api/sessions/sess-market/intents", call: func(w http.ResponseWriter, r *http.Request) { s.getSessionIntents(w, r, "sess-market") }},
		{name: "session attempt", path: "/api/sessions/sess-market/attempts", call: func(w http.ResponseWriter, r *http.Request) { s.getSessionAttempts(w, r, "sess-market") }},
		{name: "session order", path: "/api/sessions/sess-market/orders", call: func(w http.ResponseWriter, r *http.Request) { s.getSessionOrders(w, r, "sess-market") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withOrderUID(httptest.NewRequest(http.MethodGet, tc.path, nil), 7)
			rec := httptest.NewRecorder()
			tc.call(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Items) != 1 {
				t.Fatalf("items=%#v", body.Items)
			}
			for _, field := range []string{"requested_price_decimal", "price_decimal"} {
				if value, exists := body.Items[0][field]; exists {
					t.Fatalf("absent MARKET %s leaked as %#v: %s", field, value, rec.Body.String())
				}
			}
		})
	}

	state := orderStateToJSON(&orderv1.OrderStateEntry{ExchangeOrderId: "market-state"})
	if value, exists := state["price_decimal"]; exists {
		t.Fatalf("absent lifecycle MARKET price_decimal leaked as %#v", value)
	}
}

func TestLegacyFillQuoteFallbackUsesDecimalMultiplication(t *testing.T) {
	fake := &fakeOrdersClient{fillsResp: &orderv1.QueryOrderFillsResponse{
		Fills: []*orderv1.OrderFillEntry{{FillId: "fill-legacy", Qty: 0.1, FillPrice: 0.2}},
		Total: 1,
	}}
	s := newOrderHistoryServer(fake)
	cases := []struct {
		name string
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{name: "global", path: "/api/orders/fills", call: s.handleOrderFills},
		{name: "session", path: "/api/sessions/sess-legacy/fills", call: func(w http.ResponseWriter, r *http.Request) { s.getSessionFills(w, r, "sess-legacy") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withOrderUID(httptest.NewRequest(http.MethodGet, tc.path, nil), 7)
			rec := httptest.NewRecorder()
			tc.call(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var body struct {
				Items []struct {
					QuoteQtyDecimal string `json:"quote_qty_decimal"`
				} `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Items) != 1 || body.Items[0].QuoteQtyDecimal != "0.02" {
				t.Fatalf("quote fallback=%#v body=%s", body.Items, rec.Body.String())
			}
		})
	}

	delta := fillDeltaToJSON(&orderv1.FillDeltaEntry{Qty: 0.1, FillPrice: 0.2})
	if got := delta["quote_qty_decimal"]; got != "0.02" {
		t.Fatalf("lifecycle quote fallback=%#v", got)
	}
}

func TestLegacyOrderCumulativeQuoteFallbackUsesDecimalMultiplication(t *testing.T) {
	fake := &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{
		Orders: []*orderv1.ExchangeOrderEntry{{OrderId: "order-legacy", ExecutedQty: 0.1, AvgPrice: 0.2}},
		Total:  1,
	}}
	s := newOrderHistoryServer(fake)
	cases := []struct {
		name string
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{name: "global", path: "/api/orders", call: s.handleOrderHistory},
		{name: "session", path: "/api/sessions/sess-legacy/orders", call: func(w http.ResponseWriter, r *http.Request) { s.getSessionOrders(w, r, "sess-legacy") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withOrderUID(httptest.NewRequest(http.MethodGet, tc.path, nil), 7)
			rec := httptest.NewRecorder()
			tc.call(rec, req)
			var body struct {
				Items []struct {
					CumulativeQuoteQtyDecimal string `json:"cumulative_quote_qty_decimal"`
				} `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Items) != 1 || body.Items[0].CumulativeQuoteQtyDecimal != "0.02" {
				t.Fatalf("cumulative quote fallback=%#v body=%s", body.Items, rec.Body.String())
			}
		})
	}

	state := orderStateToJSON(&orderv1.OrderStateEntry{ExecutedQty: 0.1, AvgPrice: 0.2})
	if got := state["cumulative_quote_qty_decimal"]; got != "0.02" {
		t.Fatalf("lifecycle cumulative quote fallback=%#v", got)
	}
}

func TestMissingQuoteUsesExactQuantityAndPriceOperandsBeforeLegacyDoubles(t *testing.T) {
	fake := &fakeOrdersClient{fillsResp: &orderv1.QueryOrderFillsResponse{
		Fills: []*orderv1.OrderFillEntry{{
			FillId: "fill-exact-operands", Qty: 9007199254740992, FillPrice: 0.00000001,
			QtyDecimal: "9007199254740993.00000000", FillPriceDecimal: "0.00000001",
		}},
		Total: 1,
	}}
	s := newOrderHistoryServer(fake)
	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/fills", nil), 7)
	rec := httptest.NewRecorder()

	s.handleOrderFills(rec, req)

	var body struct {
		Items []struct {
			QuoteQtyDecimal string `json:"quote_qty_decimal"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].QuoteQtyDecimal != "90071992.54740993" {
		t.Fatalf("exact-operand quote=%#v body=%s", body.Items, rec.Body.String())
	}
}

func TestOrderHistory_omittedPortfolioIDIsAllowed(t *testing.T) {
	// Previously required — now portfolio_id is optional and 0 means user-wide.
	fake := &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastOrdersReq == nil {
		t.Fatal("gRPC was not called")
	}
	if fake.lastOrdersReq.GetPortfolioId() != 0 {
		t.Errorf("forwarded portfolio_id = %d, want 0", fake.lastOrdersReq.GetPortfolioId())
	}
	if fake.lastOrdersReq.GetUserId() != 7 {
		t.Errorf("forwarded user_id = %d, want 7", fake.lastOrdersReq.GetUserId())
	}
}

func TestOrderHistory_portfolioIDFilterForwarded(t *testing.T) {
	fake := &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders?portfolio_id=9", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if fake.lastOrdersReq.GetPortfolioId() != 9 {
		t.Errorf("portfolio_id = %d, want 9", fake.lastOrdersReq.GetPortfolioId())
	}
}

func TestOrderHistory_sessionIDFilterForwardedToAllQueryEndpoints(t *testing.T) {
	fake := &fakeOrdersClient{
		ordersResp:   &orderv1.QueryOrdersResponse{Total: 0},
		intentsResp:  &orderv1.QueryOrderIntentsResponse{Total: 0},
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{Total: 0},
		fillsResp:    &orderv1.QueryOrderFillsResponse{Total: 0},
	}
	s := newOrderHistoryServer(fake)

	cases := []struct {
		name string
		path string
		call func(http.ResponseWriter, *http.Request)
		got  func() string
	}{
		{
			name: "orders",
			path: "/api/orders?session_id=sess-abc",
			call: s.handleOrderHistory,
			got:  func() string { return fake.lastOrdersReq.GetSessionId() },
		},
		{
			name: "intents",
			path: "/api/orders/intents?session_id=sess-abc",
			call: s.handleOrderIntents,
			got:  func() string { return fake.lastIntentsReq.GetSessionId() },
		},
		{
			name: "attempts",
			path: "/api/orders/attempts?session_id=sess-abc",
			call: s.handleOrderAttempts,
			got:  func() string { return fake.lastAttemptsReq.GetSessionId() },
		},
		{
			name: "fills",
			path: "/api/orders/fills?session_id=sess-abc",
			call: s.handleOrderFills,
			got:  func() string { return fake.lastFillsReq.GetSessionId() },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := withOrderUID(httptest.NewRequest(http.MethodGet, tc.path, nil), 7)
			rec := httptest.NewRecorder()
			tc.call(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if got := tc.got(); got != "sess-abc" {
				t.Fatalf("session_id = %q, want sess-abc", got)
			}
		})
	}
}

func TestOrderHistory_offsetAndLimitForwarded(t *testing.T) {
	fake := &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{Total: 100}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders?limit=20&offset=40", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if fake.lastOrdersReq.GetLimit() != 20 {
		t.Errorf("limit = %d, want 20", fake.lastOrdersReq.GetLimit())
	}
	if fake.lastOrdersReq.GetOffset() != 40 {
		t.Errorf("offset = %d, want 40", fake.lastOrdersReq.GetOffset())
	}
}

func TestOrderHistory_envelopeShape(t *testing.T) {
	goodTillDate := timestamppb.New(time.Unix(1893456000, 0).UTC())
	nextCheckAt := timestamppb.New(time.Unix(1893456060, 0).UTC())
	recoveryDeadlineAt := timestamppb.New(time.Unix(1894665600, 0).UTC())
	forceClosedAt := timestamppb.New(time.Unix(1894665660, 0).UTC())
	fake := &fakeOrdersClient{
		ordersResp: &orderv1.QueryOrdersResponse{
			Total: 42,
			Orders: []*orderv1.ExchangeOrderEntry{
				{
					OrderId:            "o1",
					PortfolioId:        3,
					Symbol:             "BTCUSDT",
					Side:               "BUY",
					OrigQty:            0.1,
					ExecutedQty:        0.05,
					AvgPrice:           50000,
					PostOnly:           true,
					GoodTillDate:       goodTillDate,
					ReduceOnly:         true,
					RecoveryStatus:     "PARTIALLY_FILLED",
					NextCheckAt:        nextCheckAt,
					RecoveryDeadlineAt: recoveryDeadlineAt,
					LastRecoveryError:  "trade fee pending",
					ForceClosedAt:      forceClosedAt,
				},
			},
		},
	}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Items []struct {
			OrderID            string  `json:"order_id"`
			PortfolioID        int64   `json:"portfolio_id"`
			Symbol             string  `json:"symbol"`
			OrigQty            float64 `json:"orig_qty"`
			ExecutedQty        float64 `json:"executed_qty"`
			AvgPrice           float64 `json:"avg_price"`
			PostOnly           bool    `json:"post_only"`
			GoodTill           string  `json:"good_till_date"`
			ReduceOnly         bool    `json:"reduce_only"`
			RecoveryStatus     string  `json:"recovery_status"`
			NextCheckAt        string  `json:"next_check_at"`
			RecoveryDeadlineAt string  `json:"recovery_deadline_at"`
			LastRecoveryError  string  `json:"last_recovery_error"`
			ForceClosedAt      string  `json:"force_closed_at"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Total != 42 {
		t.Errorf("total = %d, want 42", body.Total)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(body.Items))
	}
	if body.Items[0].OrderID != "o1" || body.Items[0].Symbol != "BTCUSDT" {
		t.Errorf("unexpected item: %+v", body.Items[0])
	}
	if body.Items[0].OrigQty != 0.1 || body.Items[0].ExecutedQty != 0.05 || body.Items[0].AvgPrice != 50000 {
		t.Errorf("unexpected quantitative fields: %+v", body.Items[0])
	}
	if !body.Items[0].PostOnly || !body.Items[0].ReduceOnly || body.Items[0].GoodTill != "2030-01-01T00:00:00Z" {
		t.Errorf("unexpected order semantics: %+v", body.Items[0])
	}
	if body.Items[0].RecoveryStatus != "PARTIALLY_FILLED" ||
		body.Items[0].NextCheckAt != "2030-01-01T00:01:00Z" ||
		body.Items[0].RecoveryDeadlineAt != "2030-01-15T00:00:00Z" ||
		body.Items[0].LastRecoveryError != "trade fee pending" ||
		body.Items[0].ForceClosedAt != "2030-01-15T00:01:00Z" {
		t.Errorf("unexpected recovery fields: %+v", body.Items[0])
	}
}

func TestOrderHistory_rejectsMissingUser(t *testing.T) {
	fake := &fakeOrdersClient{}
	s := newOrderHistoryServer(fake)

	req := httptest.NewRequest(http.MethodGet, "/api/orders", nil) // no user context
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestOrderHistory_rejectsInvalidPortfolioID(t *testing.T) {
	fake := &fakeOrdersClient{}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders?portfolio_id=abc", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestOrderHistory_rejectsNonGET(t *testing.T) {
	fake := &fakeOrdersClient{}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodPost, "/api/orders", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestOrderHistoryPreservesExactDecimalFieldsAndFeeAsset(t *testing.T) {
	requestedPrice := "0.00000001"
	orderPrice := "50000.00000001"
	fake := &fakeOrdersClient{
		intentsResp: &orderv1.QueryOrderIntentsResponse{Intents: []*orderv1.OrderIntentEntry{{
			IntentId: "i-exact", Environment: 1, RequestedQty: 9007199254740992, RequestedPrice: 0.00000001,
			RequestedQtyDecimal: "9007199254740993.00000000", RequestedPriceDecimal: &requestedPrice,
		}}},
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{Attempts: []*orderv1.OrderAttemptEntry{{
			AttemptId: "a-exact", Environment: 1, RequestedQty: 9007199254740992, RequestedPrice: 0.00000001, MarkPrice: 50000,
			RequestedQtyDecimal: "9007199254740993.00000000", RequestedPriceDecimal: &requestedPrice, MarkPriceDecimal: "50000.00000001",
		}}},
		ordersResp: &orderv1.QueryOrdersResponse{Orders: []*orderv1.ExchangeOrderEntry{{
			OrderId: "o-exact", Environment: 1, OrigQty: 9007199254740992, ExecutedQty: 0.00000001, RemainingQty: 0.1, AvgPrice: 50000, Price: 50000,
			OrigQtyDecimal: "9007199254740993.00000000", ExecutedQtyDecimal: "0.00000001", RemainingQtyDecimal: "0.09999999",
			AvgPriceDecimal: "50000.00000001", PriceDecimal: &orderPrice, CumulativeQuoteQtyDecimal: "123456789.00000001",
		}}},
		fillsResp: &orderv1.QueryOrderFillsResponse{Fills: []*orderv1.OrderFillEntry{{
			FillId: "f-exact", Environment: 1, Qty: 0.00000001, FillPrice: 50000, Fee: 0.001, FeeAsset: "BNB",
			QtyDecimal: "0.00000001", FillPriceDecimal: "50000.00000001", FeeDecimal: "0.00100000", QuoteQtyDecimal: "0.00050000",
		}}},
	}
	s := newOrderHistoryServer(fake)

	assert := func(path string, handler func(http.ResponseWriter, *http.Request), want map[string]string) {
		t.Helper()
		req := withOrderUID(httptest.NewRequest(http.MethodGet, path, nil), 7)
		rec := httptest.NewRecorder()
		handler(rec, req)
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
				t.Errorf("%s %s=%#v want exact string %q", path, field, got, value)
			}
		}
		if got := body.Items[0]["environment"]; got != float64(1) {
			t.Errorf("%s environment=%#v want=1", path, got)
		}
	}

	assert("/api/orders/intents", s.handleOrderIntents, map[string]string{
		"requested_qty_decimal": "9007199254740993.00000000", "requested_price_decimal": requestedPrice,
	})
	assert("/api/orders/attempts", s.handleOrderAttempts, map[string]string{
		"requested_qty_decimal": "9007199254740993.00000000", "requested_price_decimal": requestedPrice, "mark_price_decimal": "50000.00000001",
	})
	assert("/api/orders", s.handleOrderHistory, map[string]string{
		"orig_qty_decimal": "9007199254740993.00000000", "executed_qty_decimal": "0.00000001", "remaining_qty_decimal": "0.09999999",
		"avg_price_decimal": "50000.00000001", "price_decimal": orderPrice, "cumulative_quote_qty_decimal": "123456789.00000001",
	})
	assert("/api/orders/fills", s.handleOrderFills, map[string]string{
		"qty_decimal": "0.00000001", "fill_price_decimal": "50000.00000001", "fee_decimal": "0.00100000",
		"quote_qty_decimal": "0.00050000", "fee_asset": "BNB",
	})
}

func TestExactDecimalFallbackNeverReformatsPresentExactValue(t *testing.T) {
	if got := exactDecimalOrLegacy("9007199254740993.00000000", 9007199254740992); got != "9007199254740993.00000000" {
		t.Fatalf("present exact value was reformatted: %q", got)
	}
	if got := exactDecimalOrLegacy("", 0.00000001); got != "0.00000001" {
		t.Fatalf("legacy fallback=%q want=0.00000001", got)
	}
}

func TestOrderAttempts_envelopeShape(t *testing.T) {
	goodTillDate := timestamppb.New(time.Unix(1893456000, 0).UTC())
	fake := &fakeOrdersClient{
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{
			Total: 7,
			Attempts: []*orderv1.OrderAttemptEntry{
				{
					AttemptId:       "a1",
					PortfolioId:     3,
					Symbol:          "BTCUSDT",
					Side:            "BUY",
					RequestedQty:    0.1,
					Status:          "FAILED",
					PostOnly:        true,
					GoodTillDate:    goodTillDate,
					ReduceOnly:      true,
					RiskStatus:      "REJECT",
					RiskReasonsJson: `[{"code":"ROUTE_PENDING_EXECUTION","message":"route has pending execution"}]`,
				},
			},
		},
	}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/attempts", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderAttempts(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Items []struct {
			AttemptID    string  `json:"attempt_id"`
			PortfolioID  int64   `json:"portfolio_id"`
			RequestedQty float64 `json:"requested_qty"`
			Status       string  `json:"status"`
			PostOnly     bool    `json:"post_only"`
			GoodTill     string  `json:"good_till_date"`
			ReduceOnly   bool    `json:"reduce_only"`
			RiskStatus   string  `json:"risk_status"`
			RiskReasons  []struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"risk_reasons"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Total != 7 || len(body.Items) != 1 {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Items[0].AttemptID != "a1" || body.Items[0].RequestedQty != 0.1 || body.Items[0].Status != "FAILED" {
		t.Errorf("unexpected attempt item: %+v", body.Items[0])
	}
	if !body.Items[0].PostOnly || !body.Items[0].ReduceOnly || body.Items[0].GoodTill != "2030-01-01T00:00:00Z" {
		t.Errorf("unexpected attempt semantics: %+v", body.Items[0])
	}
	if body.Items[0].RiskStatus != "REJECT" || len(body.Items[0].RiskReasons) != 1 ||
		body.Items[0].RiskReasons[0].Code != "ROUTE_PENDING_EXECUTION" ||
		body.Items[0].RiskReasons[0].Message != "route has pending execution" {
		t.Errorf("unexpected risk fields: %+v", body.Items[0])
	}
}

func TestOrderHistory_intentAndAttemptIDForwarded(t *testing.T) {
	fake := &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders?intent_id=intent-abc&attempt_id=attempt-xyz", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if fake.lastOrdersReq == nil {
		t.Fatal("gRPC was not called")
	}
	if fake.lastOrdersReq.GetIntentId() != "intent-abc" {
		t.Errorf("intent_id = %q, want intent-abc", fake.lastOrdersReq.GetIntentId())
	}
	if fake.lastOrdersReq.GetAttemptId() != "attempt-xyz" {
		t.Errorf("attempt_id = %q, want attempt-xyz", fake.lastOrdersReq.GetAttemptId())
	}
}

func TestOrderHistory_flatQueryDoesNotDefaultAncestorIDs(t *testing.T) {
	fake := &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders?portfolio_id=42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if fake.lastOrdersReq.GetIntentId() != "" {
		t.Errorf("unexpected intent_id default = %q", fake.lastOrdersReq.GetIntentId())
	}
	if fake.lastOrdersReq.GetAttemptId() != "" {
		t.Errorf("unexpected attempt_id default = %q", fake.lastOrdersReq.GetAttemptId())
	}
	if fake.lastOrdersReq.GetPortfolioId() != 42 {
		t.Errorf("portfolio_id = %d, want 42", fake.lastOrdersReq.GetPortfolioId())
	}
}

func TestOrderAttempts_intentIDForwarded(t *testing.T) {
	fake := &fakeOrdersClient{attemptsResp: &orderv1.QueryOrderAttemptsResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/attempts?intent_id=intent-abc", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderAttempts(rec, req)

	if fake.lastAttemptsReq == nil {
		t.Fatal("gRPC was not called")
	}
	if fake.lastAttemptsReq.GetIntentId() != "intent-abc" {
		t.Errorf("intent_id = %q, want intent-abc", fake.lastAttemptsReq.GetIntentId())
	}
	if fake.lastAttemptsReq.GetUserId() != 7 {
		t.Errorf("user_id = %d, want 7", fake.lastAttemptsReq.GetUserId())
	}
}

func TestOrderAttempts_flatQueryDoesNotDefaultIntentID(t *testing.T) {
	fake := &fakeOrdersClient{attemptsResp: &orderv1.QueryOrderAttemptsResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/attempts?portfolio_id=42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderAttempts(rec, req)

	if fake.lastAttemptsReq.GetIntentId() != "" {
		t.Errorf("unexpected intent_id default = %q", fake.lastAttemptsReq.GetIntentId())
	}
	if fake.lastAttemptsReq.GetPortfolioId() != 42 {
		t.Errorf("portfolio_id = %d, want 42", fake.lastAttemptsReq.GetPortfolioId())
	}
}

func TestOrderFills_orderIDForwarded(t *testing.T) {
	fake := &fakeOrdersClient{fillsResp: &orderv1.QueryOrderFillsResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/fills?order_id=order-1", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderFills(rec, req)

	if fake.lastFillsReq == nil {
		t.Fatal("gRPC was not called")
	}
	if fake.lastFillsReq.GetOrderId() != "order-1" {
		t.Errorf("order_id = %q, want order-1", fake.lastFillsReq.GetOrderId())
	}
}

func TestOrderFills_flatQueryDoesNotDefaultAncestorIDs(t *testing.T) {
	fake := &fakeOrdersClient{fillsResp: &orderv1.QueryOrderFillsResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/fills?portfolio_id=42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderFills(rec, req)

	if fake.lastFillsReq.GetOrderId() != "" || fake.lastFillsReq.GetAttemptId() != "" || fake.lastFillsReq.GetIntentId() != "" {
		t.Errorf("unexpected ancestor defaults: order_id=%q attempt_id=%q intent_id=%q",
			fake.lastFillsReq.GetOrderId(), fake.lastFillsReq.GetAttemptId(), fake.lastFillsReq.GetIntentId())
	}
}

func TestOrderIntents_envelopeShape(t *testing.T) {
	goodTillDate := timestamppb.New(time.Unix(1893456000, 0).UTC())
	fake := &fakeOrdersClient{
		intentsResp: &orderv1.QueryOrderIntentsResponse{
			Total: 3,
			Intents: []*orderv1.OrderIntentEntry{
				{
					IntentId:       "i1",
					PortfolioId:    9,
					Symbol:         "BTCUSDT",
					Side:           "BUY",
					RequestedQty:   0.1,
					RequestedPrice: 50000,
					StrategyId:     5,
					Market:         2,
					Status:         "REJECTED",
					RejectCode:     "MIN_NOTIONAL_VIOLATION",
					RejectMessage:  "notional 16.8809 is below min_notional 20",
					PostOnly:       true,
					GoodTillDate:   goodTillDate,
					ReduceOnly:     true,
				},
			},
		},
	}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/intents", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderIntents(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastIntentsReq == nil {
		t.Fatal("gRPC was not called")
	}
	if fake.lastIntentsReq.GetUserId() != 7 {
		t.Errorf("user_id = %d, want 7", fake.lastIntentsReq.GetUserId())
	}
	if fake.lastIntentsReq.GetPortfolioId() != 0 || fake.lastIntentsReq.GetStrategyId() != 0 || fake.lastIntentsReq.GetSessionId() != "" {
		t.Errorf("unexpected default filters: portfolio_id=%d strategy_id=%d session_id=%q",
			fake.lastIntentsReq.GetPortfolioId(), fake.lastIntentsReq.GetStrategyId(), fake.lastIntentsReq.GetSessionId())
	}

	var body struct {
		Items []struct {
			IntentID       string  `json:"intent_id"`
			PortfolioID    int64   `json:"portfolio_id"`
			Symbol         string  `json:"symbol"`
			Side           string  `json:"side"`
			RequestedQty   float64 `json:"requested_qty"`
			RequestedPrice float64 `json:"requested_price"`
			StrategyID     int64   `json:"strategy_id"`
			Market         string  `json:"market"`
			Status         string  `json:"status"`
			RejectCode     string  `json:"reject_code"`
			RejectMessage  string  `json:"reject_message"`
			PostOnly       bool    `json:"post_only"`
			GoodTill       string  `json:"good_till_date"`
			ReduceOnly     bool    `json:"reduce_only"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Total != 3 || len(body.Items) != 1 {
		t.Fatalf("unexpected body: %+v", body)
	}
	got := body.Items[0]
	if got.IntentID != "i1" || got.PortfolioID != 9 || got.Symbol != "BTCUSDT" || got.Side != "BUY" {
		t.Errorf("unexpected item identity: %+v", got)
	}
	if got.RequestedQty != 0.1 || got.RequestedPrice != 50000 || got.StrategyID != 5 || got.Market != "perpetual_futures" {
		t.Errorf("unexpected item fields: %+v", got)
	}
	if got.Status != "REJECTED" || got.RejectCode != "MIN_NOTIONAL_VIOLATION" || got.RejectMessage != "notional 16.8809 is below min_notional 20" {
		t.Errorf("unexpected reject fields: %+v", got)
	}
	if !got.PostOnly || !got.ReduceOnly || got.GoodTill != "2030-01-01T00:00:00Z" {
		t.Errorf("unexpected order semantics: %+v", got)
	}
}

func TestOrderIntents_portfolioAndStrategyForwarded(t *testing.T) {
	fake := &fakeOrdersClient{intentsResp: &orderv1.QueryOrderIntentsResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/intents?portfolio_id=42&strategy_id=7", nil), 11)
	rec := httptest.NewRecorder()
	s.handleOrderIntents(rec, req)

	if fake.lastIntentsReq.GetPortfolioId() != 42 {
		t.Errorf("portfolio_id = %d, want 42", fake.lastIntentsReq.GetPortfolioId())
	}
	if fake.lastIntentsReq.GetStrategyId() != 7 {
		t.Errorf("strategy_id = %d, want 7", fake.lastIntentsReq.GetStrategyId())
	}
	if fake.lastIntentsReq.GetUserId() != 11 {
		t.Errorf("user_id = %d, want 11", fake.lastIntentsReq.GetUserId())
	}
}

func TestOrderIntents_rejectsMissingUser(t *testing.T) {
	fake := &fakeOrdersClient{}
	s := newOrderHistoryServer(fake)

	req := httptest.NewRequest(http.MethodGet, "/api/orders/intents", nil) // no user context
	rec := httptest.NewRecorder()
	s.handleOrderIntents(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if fake.lastIntentsReq != nil {
		t.Error("gRPC must not be called without user context")
	}
}

func TestOrderIntents_rejectsNonGET(t *testing.T) {
	fake := &fakeOrdersClient{}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodPost, "/api/orders/intents", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderIntents(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestOrderFills_envelopeShape(t *testing.T) {
	fake := &fakeOrdersClient{
		fillsResp: &orderv1.QueryOrderFillsResponse{
			Total: 5,
			Fills: []*orderv1.OrderFillEntry{
				{FillId: "f1", OrderId: "o1", PortfolioId: 3, Symbol: "BTCUSDT", Qty: 0.1, FillPrice: 50000, Fee: 0.2},
			},
		},
	}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/fills", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderFills(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Items []struct {
			FillID      string  `json:"fill_id"`
			OrderID     string  `json:"order_id"`
			PortfolioID int64   `json:"portfolio_id"`
			Qty         float64 `json:"qty"`
			FillPrice   float64 `json:"fill_price"`
			Fee         float64 `json:"fee"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if body.Total != 5 || len(body.Items) != 1 {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.Items[0].FillID != "f1" || body.Items[0].Qty != 0.1 || body.Items[0].Fee != 0.2 {
		t.Errorf("unexpected fill item: %+v", body.Items[0])
	}
}
