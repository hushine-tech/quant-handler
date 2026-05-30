package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/core-service/gen/accountv1"
	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ── test doubles ──────────────────────────────────────────────────────────

type fakeSessionAccountsClient struct {
	accountv1.AccountServiceClient // unused methods panic on nil-interface call

	// Capture last request.
	lastSnapshotsReq             *accountv1.ListSessionSnapshotsRequest
	lastReconciliationReq        *accountv1.ListReconciliationRunsRequest
	lastReconciliationSummaryReq *accountv1.GetSessionReconciliationSummaryRequest
	lastGetSessionReq            *accountv1.GetSessionRequest
	lastGetAccountReq            *accountv1.GetAccountRequest
	lastListSessionsReq          *accountv1.ListSessionsRequest

	// Canned responses.
	snapshotsResp             *accountv1.ListSessionSnapshotsResponse
	reconciliationResp        *accountv1.ListReconciliationRunsResponse
	reconciliationSummaryResp *accountv1.GetSessionReconciliationSummaryResponse
	getSessionResp            *accountv1.GetSessionResponse
	getSessionErr             error
	getAccountResp            *accountv1.GetAccountResponse
	getAccountErr             error
	listSessionsResp          *accountv1.ListSessionsResponse
	listSessionsErr           error
	accountMode               int32
	reconciliationSummaryErr  error
}

func (f *fakeSessionAccountsClient) GetAccount(_ context.Context, in *accountv1.GetAccountRequest, _ ...grpc.CallOption) (*accountv1.GetAccountResponse, error) {
	f.lastGetAccountReq = in
	if f.getAccountErr != nil {
		return nil, f.getAccountErr
	}
	if f.getAccountResp != nil {
		return f.getAccountResp, nil
	}
	return &accountv1.GetAccountResponse{Account: &accountv1.AccountRegistryEntry{
		AccountId:   in.GetAccountId(),
		UserId:      in.GetUserId(),
		Environment: accountEnvironmentFromLegacyMode(f.accountMode),
	}}, nil
}

func (f *fakeSessionAccountsClient) GetSession(_ context.Context, in *accountv1.GetSessionRequest, _ ...grpc.CallOption) (*accountv1.GetSessionResponse, error) {
	f.lastGetSessionReq = in
	if f.getSessionErr != nil {
		return nil, f.getSessionErr
	}
	if f.getSessionResp != nil {
		return f.getSessionResp, nil
	}
	return &accountv1.GetSessionResponse{Session: &accountv1.StrategySessionEntry{
		SessionId: in.GetSessionId(),
		UserId:    in.GetUserId(),
		RuntimeId: "rt-default",
	}}, nil
}

func (f *fakeSessionAccountsClient) ListSessions(_ context.Context, in *accountv1.ListSessionsRequest, _ ...grpc.CallOption) (*accountv1.ListSessionsResponse, error) {
	f.lastListSessionsReq = in
	if f.listSessionsErr != nil {
		return nil, f.listSessionsErr
	}
	if f.listSessionsResp != nil {
		return f.listSessionsResp, nil
	}
	return &accountv1.ListSessionsResponse{}, nil
}

func (f *fakeSessionAccountsClient) ListSessionSnapshots(_ context.Context, in *accountv1.ListSessionSnapshotsRequest, _ ...grpc.CallOption) (*accountv1.ListSessionSnapshotsResponse, error) {
	f.lastSnapshotsReq = in
	return f.snapshotsResp, nil
}

func (f *fakeSessionAccountsClient) ListReconciliationRuns(_ context.Context, in *accountv1.ListReconciliationRunsRequest, _ ...grpc.CallOption) (*accountv1.ListReconciliationRunsResponse, error) {
	f.lastReconciliationReq = in
	return f.reconciliationResp, nil
}

func (f *fakeSessionAccountsClient) GetSessionReconciliationSummary(_ context.Context, in *accountv1.GetSessionReconciliationSummaryRequest, _ ...grpc.CallOption) (*accountv1.GetSessionReconciliationSummaryResponse, error) {
	f.lastReconciliationSummaryReq = in
	if f.reconciliationSummaryErr != nil {
		return nil, f.reconciliationSummaryErr
	}
	return f.reconciliationSummaryResp, nil
}

// Reuse ``fakeOrdersClient`` defined in order_history_test.go — it already
// captures ``lastReq`` + returns a canned ``resp``.

// ── session list handler ─────────────────────────────────────────────────

func TestListSessions_IncludesRuntimeAndDebugMetadata(t *testing.T) {
	acct := &fakeSessionAccountsClient{
		listSessionsResp: &accountv1.ListSessionsResponse{
			Sessions: []*accountv1.StrategySessionEntry{
				{
					SessionId:      "debug-1",
					AccountId:      7,
					Mode:           0,
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
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions?account_id=7&limit=5", nil), 42)
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

// ── snapshots handler ────────────────────────────────────────────────────

func TestGetSessionSnapshots_DefaultLimitOffsetAndPagedShape(t *testing.T) {
	acct := &fakeSessionAccountsClient{
		snapshotsResp: &accountv1.ListSessionSnapshotsResponse{
			Items: []*accountv1.SnapshotEntry{
				{Time: timestamppb.Now(), AccountId: 1, SnapshotReason: 2},
			},
			NextOffset: 1,
			HasMore:    false,
		},
	}
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

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
	acct := &fakeSessionAccountsClient{
		snapshotsResp: &accountv1.ListSessionSnapshotsResponse{},
	}
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

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
	acct := &fakeSessionAccountsClient{
		snapshotsResp: &accountv1.ListSessionSnapshotsResponse{},
	}
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

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

// ── reconciliation handler ──────────────────────────────────────────────

func TestGetSessionReconciliation_PagedShapeAndCustomPaging(t *testing.T) {
	acct := &fakeSessionAccountsClient{
		reconciliationResp: &accountv1.ListReconciliationRunsResponse{
			Items: []*accountv1.ReconciliationRunEntry{
				{RunId: "r-1", HardPass: true, SoftPass: true},
				{RunId: "r-2", HardPass: false, SoftPass: true},
			},
			NextOffset: 12,
			HasMore:    true,
		},
	}
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

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
	acct := &fakeSessionAccountsClient{
		snapshotsResp:      &accountv1.ListSessionSnapshotsResponse{},
		reconciliationResp: &accountv1.ListReconciliationRunsResponse{},
	}
	orders := &fakeOrdersClient{
		intentsResp:  &orderv1.QueryOrderIntentsResponse{},
		attemptsResp: &orderv1.QueryOrderAttemptsResponse{},
		ordersResp:   &orderv1.QueryOrdersResponse{},
		fillsResp:    &orderv1.QueryOrderFillsResponse{},
	}
	s := &server{accounts: acct, orders: orders, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

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
				{IntentId: "i-1", Symbol: "BTCUSDT", Side: "BUY", RequestedQty: 1},
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
	acct := &fakeSessionAccountsClient{
		snapshotsResp: &accountv1.ListSessionSnapshotsResponse{
			Items:      []*accountv1.SnapshotEntry{{AccountId: 1}},
			NextOffset: 1,
			HasMore:    true,
			Total:      57,
		},
	}
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

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
	acct := &fakeSessionAccountsClient{
		reconciliationResp: &accountv1.ListReconciliationRunsResponse{
			Items:      []*accountv1.ReconciliationRunEntry{{RunId: "r-1"}},
			NextOffset: 1,
			HasMore:    true,
			Total:      99,
		},
	}
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

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
	acct := &fakeSessionAccountsClient{
		reconciliationSummaryResp: &accountv1.GetSessionReconciliationSummaryResponse{
			TotalRuns:    53,
			HardFailRuns: 7,
			SoftFailRuns: 12,
		},
	}
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

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
	acct := &fakeSessionAccountsClient{
		reconciliationSummaryResp: &accountv1.GetSessionReconciliationSummaryResponse{},
	}
	s := &server{accounts: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	// No withUID → no user context → 401.
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/reconciliation/summary", nil)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetSessionReconciliationSummary_MethodNotAllowed(t *testing.T) {
	s := &server{accounts: &fakeSessionAccountsClient{}, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := withUID(httptest.NewRequest(method, "/api/sessions/s-1/reconciliation/summary", nil), 7)
		rec := httptest.NewRecorder()
		s.handleSessions(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("method=%s: status = %d, want 405", method, rec.Code)
		}
	}
}
