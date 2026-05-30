package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
	"google.golang.org/grpc"
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

func TestOrderHistory_omittedAccountIDIsAllowed(t *testing.T) {
	// Previously required — now account_id is optional and 0 means user-wide.
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
	if fake.lastOrdersReq.GetAccountId() != 0 {
		t.Errorf("forwarded account_id = %d, want 0", fake.lastOrdersReq.GetAccountId())
	}
	if fake.lastOrdersReq.GetUserId() != 7 {
		t.Errorf("forwarded user_id = %d, want 7", fake.lastOrdersReq.GetUserId())
	}
}

func TestOrderHistory_accountIDFilterForwarded(t *testing.T) {
	fake := &fakeOrdersClient{ordersResp: &orderv1.QueryOrdersResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders?account_id=9", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if fake.lastOrdersReq.GetAccountId() != 9 {
		t.Errorf("account_id = %d, want 9", fake.lastOrdersReq.GetAccountId())
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
	fake := &fakeOrdersClient{
		ordersResp: &orderv1.QueryOrdersResponse{
			Total: 42,
			Orders: []*orderv1.ExchangeOrderEntry{
				{OrderId: "o1", AccountId: 3, Symbol: "BTCUSDT", Side: "BUY", OrigQty: 0.1, ExecutedQty: 0.05, AvgPrice: 50000},
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
			OrderID     string  `json:"order_id"`
			AccountID   int64   `json:"account_id"`
			Symbol      string  `json:"symbol"`
			OrigQty     float64 `json:"orig_qty"`
			ExecutedQty float64 `json:"executed_qty"`
			AvgPrice    float64 `json:"avg_price"`
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

func TestOrderHistory_rejectsInvalidAccountID(t *testing.T) {
	fake := &fakeOrdersClient{}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders?account_id=abc", nil), 7)
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

func TestOrderAttempts_envelopeShape(t *testing.T) {
	fake := &fakeOrdersClient{
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{
			Total: 7,
			Attempts: []*orderv1.OrderAttemptEntry{
				{AttemptId: "a1", AccountId: 3, Symbol: "BTCUSDT", Side: "BUY", RequestedQty: 0.1, Status: "FAILED"},
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
			AccountID    int64   `json:"account_id"`
			RequestedQty float64 `json:"requested_qty"`
			Status       string  `json:"status"`
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

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders?account_id=42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderHistory(rec, req)

	if fake.lastOrdersReq.GetIntentId() != "" {
		t.Errorf("unexpected intent_id default = %q", fake.lastOrdersReq.GetIntentId())
	}
	if fake.lastOrdersReq.GetAttemptId() != "" {
		t.Errorf("unexpected attempt_id default = %q", fake.lastOrdersReq.GetAttemptId())
	}
	if fake.lastOrdersReq.GetAccountId() != 42 {
		t.Errorf("account_id = %d, want 42", fake.lastOrdersReq.GetAccountId())
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

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/attempts?account_id=42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderAttempts(rec, req)

	if fake.lastAttemptsReq.GetIntentId() != "" {
		t.Errorf("unexpected intent_id default = %q", fake.lastAttemptsReq.GetIntentId())
	}
	if fake.lastAttemptsReq.GetAccountId() != 42 {
		t.Errorf("account_id = %d, want 42", fake.lastAttemptsReq.GetAccountId())
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

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/fills?account_id=42", nil), 7)
	rec := httptest.NewRecorder()
	s.handleOrderFills(rec, req)

	if fake.lastFillsReq.GetOrderId() != "" || fake.lastFillsReq.GetAttemptId() != "" || fake.lastFillsReq.GetIntentId() != "" {
		t.Errorf("unexpected ancestor defaults: order_id=%q attempt_id=%q intent_id=%q",
			fake.lastFillsReq.GetOrderId(), fake.lastFillsReq.GetAttemptId(), fake.lastFillsReq.GetIntentId())
	}
}

func TestOrderIntents_envelopeShape(t *testing.T) {
	fake := &fakeOrdersClient{
		intentsResp: &orderv1.QueryOrderIntentsResponse{
			Total: 3,
			Intents: []*orderv1.OrderIntentEntry{
				{IntentId: "i1", AccountId: 9, Symbol: "BTCUSDT", Side: "BUY", RequestedQty: 0.1, RequestedPrice: 50000, StrategyId: 5, Market: 2},
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
	if fake.lastIntentsReq.GetAccountId() != 0 || fake.lastIntentsReq.GetStrategyId() != 0 || fake.lastIntentsReq.GetSessionId() != "" {
		t.Errorf("unexpected default filters: account_id=%d strategy_id=%d session_id=%q",
			fake.lastIntentsReq.GetAccountId(), fake.lastIntentsReq.GetStrategyId(), fake.lastIntentsReq.GetSessionId())
	}

	var body struct {
		Items []struct {
			IntentID       string  `json:"intent_id"`
			AccountID      int64   `json:"account_id"`
			Symbol         string  `json:"symbol"`
			Side           string  `json:"side"`
			RequestedQty   float64 `json:"requested_qty"`
			RequestedPrice float64 `json:"requested_price"`
			StrategyID     int64   `json:"strategy_id"`
			Market         string  `json:"market"`
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
	if got.IntentID != "i1" || got.AccountID != 9 || got.Symbol != "BTCUSDT" || got.Side != "BUY" {
		t.Errorf("unexpected item identity: %+v", got)
	}
	if got.RequestedQty != 0.1 || got.RequestedPrice != 50000 || got.StrategyID != 5 || got.Market != "perpetual_futures" {
		t.Errorf("unexpected item fields: %+v", got)
	}
}

func TestOrderIntents_accountAndStrategyForwarded(t *testing.T) {
	fake := &fakeOrdersClient{intentsResp: &orderv1.QueryOrderIntentsResponse{Total: 0}}
	s := newOrderHistoryServer(fake)

	req := withOrderUID(httptest.NewRequest(http.MethodGet, "/api/orders/intents?account_id=42&strategy_id=7", nil), 11)
	rec := httptest.NewRecorder()
	s.handleOrderIntents(rec, req)

	if fake.lastIntentsReq.GetAccountId() != 42 {
		t.Errorf("account_id = %d, want 42", fake.lastIntentsReq.GetAccountId())
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
				{FillId: "f1", OrderId: "o1", AccountId: 3, Symbol: "BTCUSDT", Qty: 0.1, FillPrice: 50000, Fee: 0.2},
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
			FillID    string  `json:"fill_id"`
			OrderID   string  `json:"order_id"`
			AccountID int64   `json:"account_id"`
			Qty       float64 `json:"qty"`
			FillPrice float64 `json:"fill_price"`
			Fee       float64 `json:"fee"`
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
