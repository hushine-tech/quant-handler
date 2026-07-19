package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
	"github.com/hushine-tech/core-service/gen/portfoliov1"
)

// ── Session-scoped audit list pagination contract ─────────────────────────
//
// See “paginate-session-detail-lists“ and “paged-list-jump-and-totals“:
// every session-scoped list endpoint accepts “?limit=“ + “?offset=“ and
// returns “{items, next_offset, has_more, total}“. “total“ is the
// session-wide count regardless of page — the frontend pager uses it to
// drive First / Last / jump-to-page controls. Defaults and clamp bounds are
// fixed here so all handlers stay in lockstep.
const (
	auditListDefaultLimit = 20
	auditListMaxLimit     = 200
)

// pagedResponse is the wire shape every audit list handler returns. Using a
// JSON object root (rather than a bare array) lets callers structurally
// distinguish the paged contract from legacy flat-array responses.
type pagedResponse struct {
	Items      any   `json:"items"`
	NextOffset int32 `json:"next_offset"`
	HasMore    bool  `json:"has_more"`
	Total      int64 `json:"total"`
}

func collectionPageRequested(r *http.Request) bool {
	return r.URL.Query().Get("page") == "true"
}

func parseCollectionPaging(r *http.Request) (limit, offset int32) {
	limit = 50
	offset = 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			if n > auditListMaxLimit {
				n = auditListMaxLimit
			}
			limit = int32(n)
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n >= 0 {
			offset = int32(n)
		}
	}
	return limit, offset
}

// parseAuditListPaging extracts `limit` + `offset` from the query string,
// applying the shared audit-list paging contract.
func parseAuditListPaging(r *http.Request) (limit, offset int32) {
	limit = auditListDefaultLimit
	offset = 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			if n > auditListMaxLimit {
				n = auditListMaxLimit
			}
			limit = int32(n)
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n >= 0 {
			offset = int32(n)
		}
	}
	return limit, offset
}

func (s *server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// /api/sessions/{session_id}/snapshots
	// /api/sessions/{session_id}/reconciliation
	// /api/sessions/{session_id}/orders
	// /api/sessions/{session_id}
	// /api/sessions?portfolio_id=X
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions")
	path = strings.Trim(path, "/")

	if path == "" {
		s.listSessionsHandler(w, r)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	sessionID := parts[0]

	if len(parts) == 2 {
		switch parts[1] {
		case "snapshots":
			s.getSessionSnapshots(w, r, sessionID)
			return
		case "reconciliation":
			s.getSessionReconciliation(w, r, sessionID)
			return
		case "reconciliation/summary":
			s.getSessionReconciliationSummary(w, r, sessionID)
			return
		case "intents":
			s.getSessionIntents(w, r, sessionID)
			return
		case "attempts":
			s.getSessionAttempts(w, r, sessionID)
			return
		case "orders":
			s.getSessionOrders(w, r, sessionID)
			return
		case "fills":
			s.getSessionFills(w, r, sessionID)
			return
		case "lifecycle-events":
			s.getSessionLifecycleEvents(w, r, sessionID)
			return
		case "indicators":
			s.getSessionIndicators(w, r, sessionID)
			return
		case "indicators/chunks":
			s.getSessionIndicatorChunks(w, r, sessionID)
			return
		}
	}

	s.getSession(w, r, sessionID)
}

func (s *server) listSessionsHandler(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	q := r.URL.Query()
	aidStr := q.Get("portfolio_id")
	page := collectionPageRequested(r)
	if aidStr == "" && !page {
		writeErr(w, http.StatusBadRequest, "portfolio_id is required")
		return
	}
	var aid int64
	if aidStr != "" {
		var err error
		aid, err = strconv.ParseInt(aidStr, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid portfolio_id")
			return
		}
	}
	limit, offset := parseCollectionPaging(r)
	if !page {
		limit = 20
		if v := q.Get("limit"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
				limit = int32(n)
			}
		}
	}

	req := &portfoliov1.ListSessionsRequest{
		PortfolioId: aid,
		Limit:       limit,
		Offset:      offset,
		UserId:      uid,
	}
	if v := strings.TrimSpace(q.Get("runtime_id")); v != "" {
		req.RuntimeId = v
	}
	if v := strings.TrimSpace(q.Get("strategy_id")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			req.StrategyId = n
		}
	}
	if v := strings.TrimSpace(q.Get("environment")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil {
			req.Environment = int32(n)
			req.EnvironmentSet = true
		}
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		req.Status = v
	}
	if v := strings.TrimSpace(q.Get("session_id")); v != "" {
		req.SessionIdContains = v
	}
	if v := strings.TrimSpace(q.Get("started_after_ms")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			req.StartedAfterMs = n
		}
	}
	if v := strings.TrimSpace(q.Get("started_before_ms")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			req.StartedBeforeMs = n
		}
	}
	resp, err := s.portfolios.ListSessions(r.Context(), req)
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	out := make([]sessionJSON, 0, len(resp.GetSessions()))
	for _, se := range resp.GetSessions() {
		out = append(out, protoSessionToJSON(se))
	}
	if page {
		writeJSON(w, http.StatusOK, pagedResponse{
			Items:      out,
			NextOffset: offset + int32(len(out)),
			HasMore:    resp.GetHasMore(),
			Total:      resp.GetTotal(),
		})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	resp, err := s.portfolios.GetSession(r.Context(), &portfoliov1.GetSessionRequest{SessionId: sessionID, UserId: uid})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, protoSessionToJSON(resp.GetSession()))
}

func (s *server) getSessionSnapshots(w http.ResponseWriter, r *http.Request, sessionID string) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	limit, offset := parseAuditListPaging(r)
	resp, err := s.portfolios.ListSessionSnapshots(r.Context(), &portfoliov1.ListSessionSnapshotsRequest{
		SessionId: sessionID,
		Limit:     limit,
		Offset:    offset,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type snapshotJSON struct {
		Time             string  `json:"time"`
		PortfolioID      int64   `json:"portfolio_id"`
		SnapshotReason   int32   `json:"snapshot_reason"`
		TotalValue       float64 `json:"total_value"`
		WalletBalance    float64 `json:"wallet_balance"`
		AvailableBalance float64 `json:"available_balance"`
		FuturesJSON      string  `json:"futures_json"`
		SpotJSON         string  `json:"spot_json"`
		StrategyID       int64   `json:"strategy_id"`
	}

	out := make([]snapshotJSON, 0, len(resp.GetItems()))
	for _, snap := range resp.GetItems() {
		t := ""
		if ts := snap.GetTime(); ts != nil && ts.IsValid() {
			t = ts.AsTime().UTC().Format(time.RFC3339Nano)
		}
		out = append(out, snapshotJSON{
			Time:             t,
			PortfolioID:      snap.GetPortfolioId(),
			SnapshotReason:   snap.GetSnapshotReason(),
			TotalValue:       snap.GetTotalValue(),
			WalletBalance:    snap.GetWalletBalance(),
			AvailableBalance: snap.GetAvailableBalance(),
			FuturesJSON:      snap.GetFuturesJson(),
			SpotJSON:         snap.GetSpotJson(),
			StrategyID:       snap.GetStrategyId(),
		})
	}
	writeJSON(w, http.StatusOK, pagedResponse{
		Items:      out,
		NextOffset: resp.GetNextOffset(),
		HasMore:    resp.GetHasMore(),
		Total:      resp.GetTotal(),
	})
}

func (s *server) getSessionIntents(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s.orders == nil {
		writeErr(w, http.StatusServiceUnavailable, "order API not configured")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	limit, offset := parseAuditListPaging(r)
	resp, err := s.orders.QueryOrderIntents(r.Context(), &orderv1.QueryOrderIntentsRequest{
		SessionId: sessionID,
		Limit:     limit,
		Offset:    offset,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type intentJSON struct {
		Time                  string  `json:"time"`
		IntentID              string  `json:"intent_id"`
		PortfolioID           int64   `json:"portfolio_id"`
		Symbol                string  `json:"symbol"`
		Side                  string  `json:"side"`
		RequestedQty          float64 `json:"requested_qty"`
		RequestedPrice        float64 `json:"requested_price"`
		RequestedQtyDecimal   string  `json:"requested_qty_decimal"`
		RequestedPriceDecimal string  `json:"requested_price_decimal,omitempty"`
		StrategyID            int64   `json:"strategy_id"`
		Environment           int32   `json:"environment"`
		EnvironmentLabel      string  `json:"environment_label"`
		Market                string  `json:"market"`
		MarketLabel           string  `json:"market_label"`
		VenueID               int64   `json:"venue_id,omitempty"`
		Exchange              int32   `json:"exchange"`
		ExchangeLabel         string  `json:"exchange_label"`
		PositionSide          string  `json:"position_side"`
		SessionID             string  `json:"session_id,omitempty"`
		Status                string  `json:"status,omitempty"`
		RejectCode            string  `json:"reject_code,omitempty"`
		RejectMessage         string  `json:"reject_message,omitempty"`
	}

	intents := resp.GetIntents()
	out := make([]intentJSON, 0, len(intents))
	for _, it := range intents {
		out = append(out, intentJSON{
			Time:                  protoTime(it.GetTime()),
			IntentID:              it.GetIntentId(),
			PortfolioID:           it.GetPortfolioId(),
			Symbol:                it.GetSymbol(),
			Side:                  it.GetSide(),
			RequestedQty:          it.GetRequestedQty(),
			RequestedPrice:        it.GetRequestedPrice(),
			RequestedQtyDecimal:   exactDecimalOrLegacy(it.GetRequestedQtyDecimal(), it.GetRequestedQty()),
			RequestedPriceDecimal: optionalExactDecimalOrLegacy(it.RequestedPriceDecimal, it.GetRequestedPrice()),
			StrategyID:            it.GetStrategyId(),
			Environment:           it.GetEnvironment(),
			EnvironmentLabel:      venueEnvironmentLabel(it.GetEnvironment()),
			Market:                orderMarketLabel(it.GetMarket()),
			MarketLabel:           orderMarketLabel(it.GetMarket()),
			VenueID:               it.GetVenueId(),
			Exchange:              it.GetExchange(),
			ExchangeLabel:         orderExchangeLabel(it.GetExchange()),
			PositionSide:          orderPositionSideLabel(it.GetPositionSide()),
			SessionID:             it.GetSessionId(),
			Status:                it.GetStatus(),
			RejectCode:            it.GetRejectCode(),
			RejectMessage:         it.GetRejectMessage(),
		})
	}
	nextOffset := int32(int(offset) + len(intents))
	hasMore := int64(nextOffset) < resp.GetTotal()
	writeJSON(w, http.StatusOK, pagedResponse{
		Items:      out,
		NextOffset: nextOffset,
		HasMore:    hasMore,
		Total:      resp.GetTotal(),
	})
}

func (s *server) getSessionOrders(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s.orders == nil {
		writeErr(w, http.StatusServiceUnavailable, "order API not configured")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	limit, offset := parseAuditListPaging(r)
	q := r.URL.Query()
	resp, err := s.orders.QueryOrders(r.Context(), &orderv1.QueryOrdersRequest{
		SessionId: sessionID,
		Limit:     limit,
		Offset:    offset,
		IntentId:  q.Get("intent_id"),
		AttemptId: q.Get("attempt_id"),
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type orderJSON struct {
		Time                      string  `json:"time"`
		OrderID                   string  `json:"order_id"`
		ExchangeOrderID           string  `json:"exchange_order_id,omitempty"`
		ClientOrderID             string  `json:"client_order_id,omitempty"`
		AttemptID                 string  `json:"attempt_id,omitempty"`
		IntentID                  string  `json:"intent_id,omitempty"`
		Symbol                    string  `json:"symbol"`
		Side                      string  `json:"side"`
		OrigQty                   float64 `json:"orig_qty"`
		ExecutedQty               float64 `json:"executed_qty"`
		RemainingQty              float64 `json:"remaining_qty"`
		AvgPrice                  float64 `json:"avg_price"`
		Price                     float64 `json:"price"`
		OrigQtyDecimal            string  `json:"orig_qty_decimal"`
		ExecutedQtyDecimal        string  `json:"executed_qty_decimal"`
		RemainingQtyDecimal       string  `json:"remaining_qty_decimal"`
		AvgPriceDecimal           string  `json:"avg_price_decimal"`
		PriceDecimal              string  `json:"price_decimal,omitempty"`
		CumulativeQuoteQtyDecimal string  `json:"cumulative_quote_qty_decimal"`
		Status                    string  `json:"status"`
		Environment               int32   `json:"environment"`
		EnvironmentLabel          string  `json:"environment_label"`
		Market                    string  `json:"market"`
		MarketLabel               string  `json:"market_label"`
		VenueID                   int64   `json:"venue_id,omitempty"`
		Exchange                  int32   `json:"exchange"`
		ExchangeLabel             string  `json:"exchange_label"`
		PositionSide              string  `json:"position_side"`
		StrategyID                int64   `json:"strategy_id"`
		ErrorMessage              string  `json:"error_message,omitempty"`
	}

	orders := resp.GetOrders()
	out := make([]orderJSON, 0, len(orders))
	for _, o := range orders {
		out = append(out, orderJSON{
			Time:                      protoTime(o.GetTime()),
			OrderID:                   o.GetOrderId(),
			ExchangeOrderID:           o.GetExchangeOrderId(),
			ClientOrderID:             o.GetClientOrderId(),
			AttemptID:                 o.GetAttemptId(),
			IntentID:                  o.GetIntentId(),
			Symbol:                    o.GetSymbol(),
			Side:                      o.GetSide(),
			OrigQty:                   o.GetOrigQty(),
			ExecutedQty:               o.GetExecutedQty(),
			RemainingQty:              o.GetRemainingQty(),
			AvgPrice:                  o.GetAvgPrice(),
			Price:                     o.GetPrice(),
			OrigQtyDecimal:            exactDecimalOrLegacy(o.GetOrigQtyDecimal(), o.GetOrigQty()),
			ExecutedQtyDecimal:        exactDecimalOrLegacy(o.GetExecutedQtyDecimal(), o.GetExecutedQty()),
			RemainingQtyDecimal:       exactDecimalOrLegacy(o.GetRemainingQtyDecimal(), o.GetRemainingQty()),
			AvgPriceDecimal:           exactDecimalOrLegacy(o.GetAvgPriceDecimal(), o.GetAvgPrice()),
			PriceDecimal:              optionalExactDecimalOrLegacy(o.PriceDecimal, o.GetPrice()),
			CumulativeQuoteQtyDecimal: exactQuoteQtyOrProduct(o.GetCumulativeQuoteQtyDecimal(), o.GetExecutedQtyDecimal(), o.GetExecutedQty(), o.GetAvgPriceDecimal(), o.GetAvgPrice()),
			Status:                    o.GetStatus(),
			Environment:               o.GetEnvironment(),
			EnvironmentLabel:          venueEnvironmentLabel(o.GetEnvironment()),
			Market:                    orderMarketLabel(o.GetMarket()),
			MarketLabel:               orderMarketLabel(o.GetMarket()),
			VenueID:                   o.GetVenueId(),
			Exchange:                  o.GetExchange(),
			ExchangeLabel:             orderExchangeLabel(o.GetExchange()),
			PositionSide:              orderPositionSideLabel(o.GetPositionSide()),
			StrategyID:                o.GetStrategyId(),
			ErrorMessage:              o.GetErrorMessage(),
		})
	}
	// order.v1 exposes ``total`` instead of ``has_more``; derive the
	// shared paging contract at the gateway edge (see design.md task 1.5
	// decision B: keep order.v1 proto unchanged).
	nextOffset := int32(int(offset) + len(orders))
	hasMore := int64(nextOffset) < resp.GetTotal()
	writeJSON(w, http.StatusOK, pagedResponse{
		Items:      out,
		NextOffset: nextOffset,
		HasMore:    hasMore,
		Total:      resp.GetTotal(),
	})
}

func (s *server) getSessionAttempts(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s.orders == nil {
		writeErr(w, http.StatusServiceUnavailable, "order API not configured")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	limit, offset := parseAuditListPaging(r)
	resp, err := s.orders.QueryOrderAttempts(r.Context(), &orderv1.QueryOrderAttemptsRequest{
		SessionId: sessionID,
		Limit:     limit,
		Offset:    offset,
		IntentId:  r.URL.Query().Get("intent_id"),
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type attemptJSON struct {
		Time                  string  `json:"time"`
		AttemptID             string  `json:"attempt_id"`
		IntentID              string  `json:"intent_id,omitempty"`
		OrderID               string  `json:"order_id,omitempty"`
		ExchangeOrderID       string  `json:"exchange_order_id,omitempty"`
		ClientOrderID         string  `json:"client_order_id,omitempty"`
		Symbol                string  `json:"symbol"`
		Side                  string  `json:"side"`
		RequestedQty          float64 `json:"requested_qty"`
		RequestedPrice        float64 `json:"requested_price"`
		MarkPrice             float64 `json:"mark_price"`
		RequestedQtyDecimal   string  `json:"requested_qty_decimal"`
		RequestedPriceDecimal string  `json:"requested_price_decimal,omitempty"`
		MarkPriceDecimal      string  `json:"mark_price_decimal"`
		Status                string  `json:"status"`
		Environment           int32   `json:"environment"`
		EnvironmentLabel      string  `json:"environment_label"`
		Market                string  `json:"market"`
		MarketLabel           string  `json:"market_label"`
		VenueID               int64   `json:"venue_id,omitempty"`
		Exchange              int32   `json:"exchange"`
		ExchangeLabel         string  `json:"exchange_label"`
		PositionSide          string  `json:"position_side"`
		StrategyID            int64   `json:"strategy_id"`
		ErrorMessage          string  `json:"error_message,omitempty"`
		RecoveryError         string  `json:"recovery_error,omitempty"`
	}

	attempts := resp.GetAttempts()
	out := make([]attemptJSON, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, attemptJSON{
			Time:                  protoTime(a.GetTime()),
			AttemptID:             a.GetAttemptId(),
			IntentID:              a.GetIntentId(),
			OrderID:               a.GetOrderId(),
			ExchangeOrderID:       a.GetExchangeOrderId(),
			ClientOrderID:         a.GetClientOrderId(),
			Symbol:                a.GetSymbol(),
			Side:                  a.GetSide(),
			RequestedQty:          a.GetRequestedQty(),
			RequestedPrice:        a.GetRequestedPrice(),
			MarkPrice:             a.GetMarkPrice(),
			RequestedQtyDecimal:   exactDecimalOrLegacy(a.GetRequestedQtyDecimal(), a.GetRequestedQty()),
			RequestedPriceDecimal: optionalExactDecimalOrLegacy(a.RequestedPriceDecimal, a.GetRequestedPrice()),
			MarkPriceDecimal:      exactDecimalOrLegacy(a.GetMarkPriceDecimal(), a.GetMarkPrice()),
			Status:                a.GetStatus(),
			Environment:           a.GetEnvironment(),
			EnvironmentLabel:      venueEnvironmentLabel(a.GetEnvironment()),
			Market:                orderMarketLabel(a.GetMarket()),
			MarketLabel:           orderMarketLabel(a.GetMarket()),
			VenueID:               a.GetVenueId(),
			Exchange:              a.GetExchange(),
			ExchangeLabel:         orderExchangeLabel(a.GetExchange()),
			PositionSide:          orderPositionSideLabel(a.GetPositionSide()),
			StrategyID:            a.GetStrategyId(),
			ErrorMessage:          a.GetErrorMessage(),
			RecoveryError:         a.GetRecoveryError(),
		})
	}
	nextOffset := int32(int(offset) + len(attempts))
	hasMore := int64(nextOffset) < resp.GetTotal()
	writeJSON(w, http.StatusOK, pagedResponse{
		Items:      out,
		NextOffset: nextOffset,
		HasMore:    hasMore,
		Total:      resp.GetTotal(),
	})
}

func (s *server) getSessionFills(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s.orders == nil {
		writeErr(w, http.StatusServiceUnavailable, "order API not configured")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	limit, offset := parseAuditListPaging(r)
	q := r.URL.Query()
	resp, err := s.orders.QueryOrderFills(r.Context(), &orderv1.QueryOrderFillsRequest{
		SessionId: sessionID,
		Limit:     limit,
		Offset:    offset,
		IntentId:  q.Get("intent_id"),
		AttemptId: q.Get("attempt_id"),
		OrderId:   q.Get("order_id"),
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type fillJSON struct {
		Time             string  `json:"time"`
		FillID           string  `json:"fill_id"`
		ExchangeTradeID  string  `json:"exchange_trade_id,omitempty"`
		OrderID          string  `json:"order_id"`
		ExchangeOrderID  string  `json:"exchange_order_id,omitempty"`
		AttemptID        string  `json:"attempt_id,omitempty"`
		IntentID         string  `json:"intent_id,omitempty"`
		Symbol           string  `json:"symbol"`
		Side             string  `json:"side"`
		Qty              float64 `json:"qty"`
		FillPrice        float64 `json:"fill_price"`
		Fee              float64 `json:"fee"`
		FeeAsset         string  `json:"fee_asset"`
		QtyDecimal       string  `json:"qty_decimal"`
		FillPriceDecimal string  `json:"fill_price_decimal"`
		FeeDecimal       string  `json:"fee_decimal"`
		QuoteQtyDecimal  string  `json:"quote_qty_decimal"`
		Status           string  `json:"status"`
		Environment      int32   `json:"environment"`
		EnvironmentLabel string  `json:"environment_label"`
		Market           string  `json:"market"`
		MarketLabel      string  `json:"market_label"`
		VenueID          int64   `json:"venue_id,omitempty"`
		Exchange         int32   `json:"exchange"`
		ExchangeLabel    string  `json:"exchange_label"`
		PositionSide     string  `json:"position_side"`
		StrategyID       int64   `json:"strategy_id"`
	}

	fills := resp.GetFills()
	out := make([]fillJSON, 0, len(fills))
	for _, f := range fills {
		out = append(out, fillJSON{
			Time:             protoTime(f.GetTime()),
			FillID:           f.GetFillId(),
			ExchangeTradeID:  f.GetExchangeTradeId(),
			OrderID:          f.GetOrderId(),
			ExchangeOrderID:  f.GetExchangeOrderId(),
			AttemptID:        f.GetAttemptId(),
			IntentID:         f.GetIntentId(),
			Symbol:           f.GetSymbol(),
			Side:             f.GetSide(),
			Qty:              f.GetQty(),
			FillPrice:        f.GetFillPrice(),
			Fee:              f.GetFee(),
			FeeAsset:         f.GetFeeAsset(),
			QtyDecimal:       exactDecimalOrLegacy(f.GetQtyDecimal(), f.GetQty()),
			FillPriceDecimal: exactDecimalOrLegacy(f.GetFillPriceDecimal(), f.GetFillPrice()),
			FeeDecimal:       exactDecimalOrLegacy(f.GetFeeDecimal(), f.GetFee()),
			QuoteQtyDecimal:  exactQuoteQtyOrProduct(f.GetQuoteQtyDecimal(), f.GetQtyDecimal(), f.GetQty(), f.GetFillPriceDecimal(), f.GetFillPrice()),
			Status:           f.GetStatus(),
			Environment:      f.GetEnvironment(),
			EnvironmentLabel: venueEnvironmentLabel(f.GetEnvironment()),
			Market:           orderMarketLabel(f.GetMarket()),
			MarketLabel:      orderMarketLabel(f.GetMarket()),
			VenueID:          f.GetVenueId(),
			Exchange:         f.GetExchange(),
			ExchangeLabel:    orderExchangeLabel(f.GetExchange()),
			PositionSide:     orderPositionSideLabel(f.GetPositionSide()),
			StrategyID:       f.GetStrategyId(),
		})
	}
	nextOffset := int32(int(offset) + len(fills))
	hasMore := int64(nextOffset) < resp.GetTotal()
	writeJSON(w, http.StatusOK, pagedResponse{
		Items:      out,
		NextOffset: nextOffset,
		HasMore:    hasMore,
		Total:      resp.GetTotal(),
	})
}

func (s *server) getSessionLifecycleEvents(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s.orders == nil {
		writeErr(w, http.StatusServiceUnavailable, "order API not configured")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	if s.portfolios == nil {
		writeErr(w, http.StatusServiceUnavailable, "core-service not configured")
		return
	}
	if _, err := s.portfolios.GetSession(r.Context(), &portfoliov1.GetSessionRequest{SessionId: sessionID, UserId: uid}); err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	limit, afterEventID := parseLifecycleEventPaging(r)
	resp, err := s.orders.ListOrderLifecycleEvents(r.Context(), &orderv1.ListOrderLifecycleEventsRequest{
		SessionId:    sessionID,
		AfterEventId: afterEventID,
		Limit:        limit,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	events := resp.GetEvents()
	out := make([]orderLifecycleEventJSON, 0, len(events))
	var nextEventID int64
	for _, event := range events {
		out = append(out, orderLifecycleEventToJSON(event))
		if event.GetEventId() > nextEventID {
			nextEventID = event.GetEventId()
		}
	}
	if nextEventID == 0 {
		nextEventID = afterEventID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":         out,
		"next_event_id": nextEventID,
		"next_offset":   nextEventID,
		"has_more":      len(events) >= int(limit),
		"total":         int64(0),
	})
}

func parseLifecycleEventPaging(r *http.Request) (limit int32, afterEventID int64) {
	limit = auditListDefaultLimit
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			if n > auditListMaxLimit {
				n = auditListMaxLimit
			}
			limit = int32(n)
		}
	}
	if v := q.Get("after_event_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			afterEventID = n
		}
	}
	if afterEventID == 0 {
		if v := q.Get("offset"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
				afterEventID = n
			}
		}
	}
	return limit, afterEventID
}

type orderLifecycleEventJSON struct {
	EventID          int64          `json:"event_id"`
	SessionID        string         `json:"session_id"`
	PortfolioID      int64          `json:"portfolio_id"`
	VenueID          int64          `json:"venue_id,omitempty"`
	IntentID         string         `json:"intent_id,omitempty"`
	AttemptID        string         `json:"attempt_id,omitempty"`
	OrderID          string         `json:"order_id,omitempty"`
	ExchangeOrderID  string         `json:"exchange_order_id,omitempty"`
	ExchangeTradeID  string         `json:"exchange_trade_id,omitempty"`
	EventType        string         `json:"event_type"`
	OrderStatus      string         `json:"order_status"`
	Environment      int32          `json:"environment"`
	EnvironmentLabel string         `json:"environment_label"`
	Exchange         int32          `json:"exchange"`
	ExchangeLabel    string         `json:"exchange_label"`
	Market           int32          `json:"market"`
	MarketLabel      string         `json:"market_label"`
	PositionSide     string         `json:"position_side"`
	Side             string         `json:"side"`
	FillDelta        map[string]any `json:"fill_delta,omitempty"`
	OrderState       map[string]any `json:"order_state,omitempty"`
	OccurredAt       string         `json:"occurred_at"`
	CreatedAt        string         `json:"created_at"`
}

func orderLifecycleEventToJSON(event *orderv1.OrderLifecycleEventEntry) orderLifecycleEventJSON {
	return orderLifecycleEventJSON{
		EventID:          event.GetEventId(),
		SessionID:        event.GetSessionId(),
		PortfolioID:      event.GetPortfolioId(),
		VenueID:          event.GetVenueId(),
		IntentID:         event.GetIntentId(),
		AttemptID:        event.GetAttemptId(),
		OrderID:          event.GetOrderId(),
		ExchangeOrderID:  event.GetExchangeOrderId(),
		ExchangeTradeID:  event.GetExchangeTradeId(),
		EventType:        event.GetEventType(),
		OrderStatus:      event.GetOrderStatus(),
		Environment:      event.GetEnvironment(),
		EnvironmentLabel: venueEnvironmentLabel(event.GetEnvironment()),
		Exchange:         event.GetExchange(),
		ExchangeLabel:    orderExchangeLabel(event.GetExchange()),
		Market:           event.GetMarket(),
		MarketLabel:      orderMarketLabel(event.GetMarket()),
		PositionSide:     orderPositionSideLabel(event.GetPositionSide()),
		Side:             event.GetSide(),
		FillDelta:        fillDeltaToJSON(event.GetFillDelta()),
		OrderState:       orderStateToJSON(event.GetOrderState()),
		OccurredAt:       protoTime(event.GetOccurredAt()),
		CreatedAt:        protoTime(event.GetCreatedAt()),
	}
}

func fillDeltaToJSON(delta *orderv1.FillDeltaEntry) map[string]any {
	if delta == nil {
		return nil
	}
	return map[string]any{
		"exchange_trade_id":  delta.GetExchangeTradeId(),
		"exchange_order_id":  delta.GetExchangeOrderId(),
		"symbol":             delta.GetSymbol(),
		"qty":                delta.GetQty(),
		"fill_price":         delta.GetFillPrice(),
		"fee":                delta.GetFee(),
		"fee_asset":          delta.GetFeeAsset(),
		"qty_decimal":        exactDecimalOrLegacy(delta.GetQtyDecimal(), delta.GetQty()),
		"fill_price_decimal": exactDecimalOrLegacy(delta.GetFillPriceDecimal(), delta.GetFillPrice()),
		"fee_decimal":        exactDecimalOrLegacy(delta.GetFeeDecimal(), delta.GetFee()),
		"quote_qty_decimal":  exactQuoteQtyOrProduct(delta.GetQuoteQtyDecimal(), delta.GetQtyDecimal(), delta.GetQty(), delta.GetFillPriceDecimal(), delta.GetFillPrice()),
		"fee_missing":        delta.GetFeeMissing(),
		"trade_time":         protoTime(delta.GetTradeTime()),
	}
}

func orderStateToJSON(state *orderv1.OrderStateEntry) map[string]any {
	if state == nil {
		return nil
	}
	out := map[string]any{
		"exchange_order_id":            state.GetExchangeOrderId(),
		"client_order_id":              state.GetClientOrderId(),
		"symbol":                       state.GetSymbol(),
		"status":                       state.GetStatus(),
		"orig_qty":                     state.GetOrigQty(),
		"executed_qty":                 state.GetExecutedQty(),
		"remaining_qty":                state.GetRemainingQty(),
		"avg_price":                    state.GetAvgPrice(),
		"orig_qty_decimal":             exactDecimalOrLegacy(state.GetOrigQtyDecimal(), state.GetOrigQty()),
		"executed_qty_decimal":         exactDecimalOrLegacy(state.GetExecutedQtyDecimal(), state.GetExecutedQty()),
		"remaining_qty_decimal":        exactDecimalOrLegacy(state.GetRemainingQtyDecimal(), state.GetRemainingQty()),
		"avg_price_decimal":            exactDecimalOrLegacy(state.GetAvgPriceDecimal(), state.GetAvgPrice()),
		"cumulative_quote_qty_decimal": exactQuoteQtyOrProduct(state.GetCumulativeQuoteQtyDecimal(), state.GetExecutedQtyDecimal(), state.GetExecutedQty(), state.GetAvgPriceDecimal(), state.GetAvgPrice()),
		"updated_at":                   protoTime(state.GetUpdatedAt()),
	}
	if price := optionalExactDecimalOrLegacy(state.PriceDecimal, 0); price != "" {
		out["price_decimal"] = price
	}
	return out
}

func (s *server) getSessionReconciliation(w http.ResponseWriter, r *http.Request, sessionID string) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	limit, offset := parseAuditListPaging(r)

	resp, err := s.portfolios.ListReconciliationRuns(r.Context(), &portfoliov1.ListReconciliationRunsRequest{
		SessionId: sessionID,
		UserId:    uid,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type fieldDiffJSON struct {
		Field     string  `json:"field"`
		Severity  string  `json:"severity"`
		Exchange  float64 `json:"exchange"`
		Local     float64 `json:"local"`
		DiffAbs   float64 `json:"diff_abs"`
		DiffRatio float64 `json:"diff_ratio"`
		Threshold any     `json:"threshold,omitempty"`
		Passed    bool    `json:"passed"`
	}
	type runJSON struct {
		Time                 string          `json:"time"`
		RunID                string          `json:"run_id"`
		PortfolioID          int64           `json:"portfolio_id"`
		StrategyID           int64           `json:"strategy_id"`
		SessionID            string          `json:"session_id"`
		SnapshotReason       int32           `json:"snapshot_reason"`
		RunType              string          `json:"run_type"`
		Environment          int32           `json:"environment"`
		HardPass             bool            `json:"hard_pass"`
		SoftPass             bool            `json:"soft_pass"`
		HardFailCount        int             `json:"hard_fail_count"`
		SoftFailCount        int             `json:"soft_fail_count"`
		AdvisoryCount        int             `json:"advisory_count"`
		FieldDiffs           []fieldDiffJSON `json:"field_diffs"`
		AdvisoryDiffs        []fieldDiffJSON `json:"advisory_diffs"`
		LocalSnapshotJSON    string          `json:"local_snapshot_json"`
		ExchangeSnapshotJSON string          `json:"exchange_snapshot_json"`
		VenueDiffsJSON       string          `json:"venue_diffs_json"`
	}

	decodeThreshold := func(raw string) any {
		if raw == "" {
			return nil
		}
		var out any
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			return raw
		}
		return out
	}
	toFieldDiff := func(d *portfoliov1.FieldDiffEntry) fieldDiffJSON {
		return fieldDiffJSON{
			Field:     d.GetField(),
			Severity:  d.GetSeverity(),
			Exchange:  d.GetExchange(),
			Local:     d.GetLocal(),
			DiffAbs:   d.GetDiffAbs(),
			DiffRatio: d.GetDiffRatio(),
			Threshold: decodeThreshold(d.GetThresholdJson()),
			Passed:    d.GetPassed(),
		}
	}

	out := make([]runJSON, 0, len(resp.GetItems()))
	for _, run := range resp.GetItems() {
		t := ""
		if ts := run.GetTime(); ts != nil && ts.IsValid() {
			t = ts.AsTime().UTC().Format(time.RFC3339Nano)
		}
		fieldDiffs := make([]fieldDiffJSON, 0, len(run.GetFieldDiffs()))
		hardFailCount := 0
		softFailCount := 0
		for _, diff := range run.GetFieldDiffs() {
			fieldDiffs = append(fieldDiffs, toFieldDiff(diff))
			if diff.GetPassed() {
				continue
			}
			switch diff.GetSeverity() {
			case "hard":
				hardFailCount++
			case "soft":
				softFailCount++
			}
		}
		advisoryDiffs := make([]fieldDiffJSON, 0, len(run.GetAdvisoryDiffs()))
		for _, diff := range run.GetAdvisoryDiffs() {
			advisoryDiffs = append(advisoryDiffs, toFieldDiff(diff))
		}
		out = append(out, runJSON{
			Time:                 t,
			RunID:                run.GetRunId(),
			PortfolioID:          run.GetPortfolioId(),
			StrategyID:           run.GetStrategyId(),
			SessionID:            run.GetSessionId(),
			SnapshotReason:       run.GetSnapshotReason(),
			RunType:              run.GetRunType(),
			Environment:          run.GetEnvironment(),
			HardPass:             run.GetHardPass(),
			SoftPass:             run.GetSoftPass(),
			HardFailCount:        hardFailCount,
			SoftFailCount:        softFailCount,
			AdvisoryCount:        len(advisoryDiffs),
			FieldDiffs:           fieldDiffs,
			AdvisoryDiffs:        advisoryDiffs,
			LocalSnapshotJSON:    run.GetLocalSnapshotJson(),
			ExchangeSnapshotJSON: run.GetExchangeSnapshotJson(),
			VenueDiffsJSON:       run.GetVenueDiffsJson(),
		})
	}
	writeJSON(w, http.StatusOK, pagedResponse{
		Items:      out,
		NextOffset: resp.GetNextOffset(),
		HasMore:    resp.GetHasMore(),
		Total:      resp.GetTotal(),
	})
}

// getSessionReconciliationSummary serves GET /api/sessions/:id/reconciliation/summary.
// Returns the session-wide aggregate counts so the SessionDetailPage tile can
// render real totals instead of the current-page slice.
func (s *server) getSessionReconciliationSummary(w http.ResponseWriter, r *http.Request, sessionID string) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	resp, err := s.portfolios.GetSessionReconciliationSummary(r.Context(), &portfoliov1.GetSessionReconciliationSummaryRequest{
		SessionId: sessionID,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		TotalRuns    int64 `json:"total_runs"`
		HardFailRuns int64 `json:"hard_fail_runs"`
		SoftFailRuns int64 `json:"soft_fail_runs"`
	}{
		TotalRuns:    resp.GetTotalRuns(),
		HardFailRuns: resp.GetHardFailRuns(),
		SoftFailRuns: resp.GetSoftFailRuns(),
	})
}

type sessionJSON struct {
	SessionID       string          `json:"session_id"`
	PortfolioID     int64           `json:"portfolio_id"`
	StrategyID      int64           `json:"strategy_id"`
	Environment     int32           `json:"environment"`
	Status          string          `json:"status"`
	Interval        string          `json:"interval"`
	StartTimeMs     int64           `json:"start_time_ms,omitempty"`
	EndTimeMs       int64           `json:"end_time_ms,omitempty"`
	BarsProcessed   int32           `json:"bars_processed"`
	Error           string          `json:"error,omitempty"`
	ErrorCode       string          `json:"error_code,omitempty"`
	ErrorMessage    string          `json:"error_message,omitempty"`
	ErrorDetail     json.RawMessage `json:"error_detail,omitempty"`
	ErrorDetailJSON string          `json:"error_detail_json,omitempty"`
	RuntimeID       string          `json:"runtime_id,omitempty"`
	RuntimeSource   string          `json:"runtime_source,omitempty"`
	RuntimeName     string          `json:"runtime_name,omitempty"`
	SessionType     string          `json:"session_type,omitempty"`
	RuntimeVersion  string          `json:"runtime_version,omitempty"`
	SessionName     string          `json:"session_name,omitempty"`
	StartedAt       string          `json:"started_at"`
	CompletedAt     string          `json:"completed_at,omitempty"`
}

func protoSessionToJSON(se *portfoliov1.StrategySessionEntry) sessionJSON {
	if se == nil {
		return sessionJSON{}
	}
	j := sessionJSON{
		SessionID:       se.GetSessionId(),
		PortfolioID:     se.GetPortfolioId(),
		StrategyID:      se.GetStrategyId(),
		Environment:     se.GetEnvironment(),
		Status:          se.GetStatus(),
		Interval:        se.GetInterval(),
		StartTimeMs:     se.GetStartTimeMs(),
		EndTimeMs:       se.GetEndTimeMs(),
		BarsProcessed:   se.GetBarsProcessed(),
		Error:           se.GetError(),
		ErrorCode:       se.GetErrorCode(),
		ErrorMessage:    se.GetErrorMessage(),
		ErrorDetailJSON: se.GetErrorDetailJson(),
		RuntimeID:       se.GetRuntimeId(),
		RuntimeSource:   se.GetRuntimeSource(),
		RuntimeName:     se.GetRuntimeName(),
		SessionType:     se.GetSessionType(),
		RuntimeVersion:  se.GetRuntimeVersion(),
		SessionName:     se.GetSessionName(),
	}
	if raw := []byte(se.GetErrorDetailJson()); json.Valid(raw) {
		j.ErrorDetail = append(json.RawMessage(nil), raw...)
	}
	if ts := se.GetStartedAt(); ts != nil && ts.IsValid() {
		j.StartedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	if ts := se.GetCompletedAt(); ts != nil && ts.IsValid() {
		j.CompletedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	return j
}
