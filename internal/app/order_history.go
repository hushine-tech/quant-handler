package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type orderHistoryFilters struct {
	userID     int64
	accountID  int64
	strategyID int64
	intentID   string
	attemptID  string
	orderID    string
	limit      int32
	offset     int32
}

func (s *server) handleOrders(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/orders")
	path = strings.Trim(path, "/")

	switch path {
	case "":
		s.handleOrderHistory(w, r)
	case "intents":
		s.handleOrderIntents(w, r)
	case "attempts":
		s.handleOrderAttempts(w, r)
	case "fills":
		s.handleOrderFills(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) handleOrderIntents(w http.ResponseWriter, r *http.Request) {
	filters, ok := s.parseOrderHistoryFilters(w, r)
	if !ok {
		return
	}

	resp, err := s.orders.QueryOrderIntents(r.Context(), &orderv1.QueryOrderIntentsRequest{
		AccountId:  filters.accountID,
		StrategyId: filters.strategyID,
		Limit:      filters.limit,
		Offset:     filters.offset,
		UserId:     filters.userID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type intentJSON struct {
		Time           string  `json:"time"`
		IntentID       string  `json:"intent_id"`
		AccountID      int64   `json:"account_id"`
		Symbol         string  `json:"symbol"`
		Side           string  `json:"side"`
		RequestedQty   float64 `json:"requested_qty"`
		RequestedPrice float64 `json:"requested_price"`
		StrategyID     int64   `json:"strategy_id"`
		Market         string  `json:"market"`
		SessionID      string  `json:"session_id,omitempty"`
	}

	items := make([]intentJSON, 0, len(resp.GetIntents()))
	for _, it := range resp.GetIntents() {
		items = append(items, intentJSON{
			Time:           protoTime(it.GetTime()),
			IntentID:       it.GetIntentId(),
			AccountID:      it.GetAccountId(),
			Symbol:         it.GetSymbol(),
			Side:           it.GetSide(),
			RequestedQty:   it.GetRequestedQty(),
			RequestedPrice: it.GetRequestedPrice(),
			StrategyID:     it.GetStrategyId(),
			Market:         it.GetMarket(),
			SessionID:      it.GetSessionId(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": resp.GetTotal(),
	})
}

func (s *server) handleOrderHistory(w http.ResponseWriter, r *http.Request) {
	filters, ok := s.parseOrderHistoryFilters(w, r)
	if !ok {
		return
	}

	resp, err := s.orders.QueryOrders(r.Context(), &orderv1.QueryOrdersRequest{
		AccountId:  filters.accountID,
		StrategyId: filters.strategyID,
		IntentId:   filters.intentID,
		AttemptId:  filters.attemptID,
		Limit:      filters.limit,
		Offset:     filters.offset,
		UserId:     filters.userID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type orderJSON struct {
		Time            string  `json:"time"`
		OrderID         string  `json:"order_id"`
		ExchangeOrderID string  `json:"exchange_order_id,omitempty"`
		ClientOrderID   string  `json:"client_order_id,omitempty"`
		AttemptID       string  `json:"attempt_id,omitempty"`
		IntentID        string  `json:"intent_id,omitempty"`
		AccountID       int64   `json:"account_id"`
		Symbol          string  `json:"symbol"`
		Side            string  `json:"side"`
		OrigQty         float64 `json:"orig_qty"`
		ExecutedQty     float64 `json:"executed_qty"`
		RemainingQty    float64 `json:"remaining_qty"`
		AvgPrice        float64 `json:"avg_price"`
		Price           float64 `json:"price"`
		Status          string  `json:"status"`
		Market          string  `json:"market"`
		StrategyID      int64   `json:"strategy_id"`
		SessionID       string  `json:"session_id,omitempty"`
		ErrorMessage    string  `json:"error_message,omitempty"`
	}

	items := make([]orderJSON, 0, len(resp.GetOrders()))
	for _, o := range resp.GetOrders() {
		items = append(items, orderJSON{
			Time:            protoTime(o.GetTime()),
			OrderID:         o.GetOrderId(),
			ExchangeOrderID: o.GetExchangeOrderId(),
			ClientOrderID:   o.GetClientOrderId(),
			AttemptID:       o.GetAttemptId(),
			IntentID:        o.GetIntentId(),
			AccountID:       o.GetAccountId(),
			Symbol:          o.GetSymbol(),
			Side:            o.GetSide(),
			OrigQty:         o.GetOrigQty(),
			ExecutedQty:     o.GetExecutedQty(),
			RemainingQty:    o.GetRemainingQty(),
			AvgPrice:        o.GetAvgPrice(),
			Price:           o.GetPrice(),
			Status:          o.GetStatus(),
			Market:          o.GetMarket(),
			StrategyID:      o.GetStrategyId(),
			SessionID:       o.GetSessionId(),
			ErrorMessage:    o.GetErrorMessage(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": resp.GetTotal(),
	})
}

func (s *server) handleOrderAttempts(w http.ResponseWriter, r *http.Request) {
	filters, ok := s.parseOrderHistoryFilters(w, r)
	if !ok {
		return
	}

	resp, err := s.orders.QueryOrderAttempts(r.Context(), &orderv1.QueryOrderAttemptsRequest{
		AccountId:  filters.accountID,
		StrategyId: filters.strategyID,
		IntentId:   filters.intentID,
		Limit:      filters.limit,
		Offset:     filters.offset,
		UserId:     filters.userID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type attemptJSON struct {
		Time            string  `json:"time"`
		AttemptID       string  `json:"attempt_id"`
		IntentID        string  `json:"intent_id,omitempty"`
		OrderID         string  `json:"order_id,omitempty"`
		ExchangeOrderID string  `json:"exchange_order_id,omitempty"`
		ClientOrderID   string  `json:"client_order_id,omitempty"`
		AccountID       int64   `json:"account_id"`
		Symbol          string  `json:"symbol"`
		Side            string  `json:"side"`
		RequestedQty    float64 `json:"requested_qty"`
		RequestedPrice  float64 `json:"requested_price"`
		MarkPrice       float64 `json:"mark_price"`
		Status          string  `json:"status"`
		Market          string  `json:"market"`
		StrategyID      int64   `json:"strategy_id"`
		SessionID       string  `json:"session_id,omitempty"`
		ErrorMessage    string  `json:"error_message,omitempty"`
		RecoveryError   string  `json:"recovery_error,omitempty"`
	}

	items := make([]attemptJSON, 0, len(resp.GetAttempts()))
	for _, a := range resp.GetAttempts() {
		items = append(items, attemptJSON{
			Time:            protoTime(a.GetTime()),
			AttemptID:       a.GetAttemptId(),
			IntentID:        a.GetIntentId(),
			OrderID:         a.GetOrderId(),
			ExchangeOrderID: a.GetExchangeOrderId(),
			ClientOrderID:   a.GetClientOrderId(),
			AccountID:       a.GetAccountId(),
			Symbol:          a.GetSymbol(),
			Side:            a.GetSide(),
			RequestedQty:    a.GetRequestedQty(),
			RequestedPrice:  a.GetRequestedPrice(),
			MarkPrice:       a.GetMarkPrice(),
			Status:          a.GetStatus(),
			Market:          a.GetMarket(),
			StrategyID:      a.GetStrategyId(),
			SessionID:       a.GetSessionId(),
			ErrorMessage:    a.GetErrorMessage(),
			RecoveryError:   a.GetRecoveryError(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": resp.GetTotal(),
	})
}

func (s *server) handleOrderFills(w http.ResponseWriter, r *http.Request) {
	filters, ok := s.parseOrderHistoryFilters(w, r)
	if !ok {
		return
	}

	resp, err := s.orders.QueryOrderFills(r.Context(), &orderv1.QueryOrderFillsRequest{
		AccountId:  filters.accountID,
		StrategyId: filters.strategyID,
		IntentId:   filters.intentID,
		AttemptId:  filters.attemptID,
		OrderId:    filters.orderID,
		Limit:      filters.limit,
		Offset:     filters.offset,
		UserId:     filters.userID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	type fillJSON struct {
		Time            string  `json:"time"`
		FillID          string  `json:"fill_id"`
		ExchangeTradeID string  `json:"exchange_trade_id,omitempty"`
		OrderID         string  `json:"order_id"`
		ExchangeOrderID string  `json:"exchange_order_id,omitempty"`
		AttemptID       string  `json:"attempt_id,omitempty"`
		IntentID        string  `json:"intent_id,omitempty"`
		AccountID       int64   `json:"account_id"`
		Symbol          string  `json:"symbol"`
		Side            string  `json:"side"`
		Qty             float64 `json:"qty"`
		FillPrice       float64 `json:"fill_price"`
		Fee             float64 `json:"fee"`
		Status          string  `json:"status"`
		Market          string  `json:"market"`
		StrategyID      int64   `json:"strategy_id"`
		SessionID       string  `json:"session_id,omitempty"`
	}

	items := make([]fillJSON, 0, len(resp.GetFills()))
	for _, f := range resp.GetFills() {
		items = append(items, fillJSON{
			Time:            protoTime(f.GetTime()),
			FillID:          f.GetFillId(),
			ExchangeTradeID: f.GetExchangeTradeId(),
			OrderID:         f.GetOrderId(),
			ExchangeOrderID: f.GetExchangeOrderId(),
			AttemptID:       f.GetAttemptId(),
			IntentID:        f.GetIntentId(),
			AccountID:       f.GetAccountId(),
			Symbol:          f.GetSymbol(),
			Side:            f.GetSide(),
			Qty:             f.GetQty(),
			FillPrice:       f.GetFillPrice(),
			Fee:             f.GetFee(),
			Status:          f.GetStatus(),
			Market:          f.GetMarket(),
			StrategyID:      f.GetStrategyId(),
			SessionID:       f.GetSessionId(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": resp.GetTotal(),
	})
}

func (s *server) parseOrderHistoryFilters(w http.ResponseWriter, r *http.Request) (orderHistoryFilters, bool) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return orderHistoryFilters{}, false
	}
	if s.orders == nil {
		writeErr(w, http.StatusServiceUnavailable, "order API not configured")
		return orderHistoryFilters{}, false
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return orderHistoryFilters{}, false
	}

	q := r.URL.Query()
	var accountID int64
	if v := q.Get("account_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid account_id")
			return orderHistoryFilters{}, false
		}
		accountID = n
	}

	var strategyID int64
	if v := q.Get("strategy_id"); v != "" {
		sid, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid strategy_id")
			return orderHistoryFilters{}, false
		}
		strategyID = sid
	}

	limit := int32(100)
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n > 0 {
			limit = int32(n)
		}
	}

	var offset int32
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	return orderHistoryFilters{
		userID:     uid,
		accountID:  accountID,
		strategyID: strategyID,
		intentID:   strings.TrimSpace(q.Get("intent_id")),
		attemptID:  strings.TrimSpace(q.Get("attempt_id")),
		orderID:    strings.TrimSpace(q.Get("order_id")),
		limit:      limit,
		offset:     offset,
	}, true
}

func protoTime(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339Nano)
}
