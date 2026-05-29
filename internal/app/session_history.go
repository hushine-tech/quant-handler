package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hushine-tech/core-service/gen/accountv1"
	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
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
	// /api/sessions?account_id=X
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
	aidStr := q.Get("account_id")
	page := collectionPageRequested(r)
	if aidStr == "" && !page {
		writeErr(w, http.StatusBadRequest, "account_id is required")
		return
	}
	var aid int64
	if aidStr != "" {
		var err error
		aid, err = strconv.ParseInt(aidStr, 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid account_id")
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

	req := &accountv1.ListSessionsRequest{
		AccountId: aid,
		Limit:     limit,
		Offset:    offset,
		UserId:    uid,
	}
	if v := strings.TrimSpace(q.Get("runtime_id")); v != "" {
		req.RuntimeId = v
	}
	if v := strings.TrimSpace(q.Get("strategy_id")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			req.StrategyId = n
		}
	}
	if v := strings.TrimSpace(q.Get("mode")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 32); err == nil {
			req.Mode = int32(n)
			req.ModeSet = true
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
	resp, err := s.accounts.ListSessions(r.Context(), req)
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
	resp, err := s.accounts.GetSession(r.Context(), &accountv1.GetSessionRequest{SessionId: sessionID, UserId: uid})
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
	resp, err := s.accounts.ListSessionSnapshots(r.Context(), &accountv1.ListSessionSnapshotsRequest{
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
		AccountID        int64   `json:"account_id"`
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
			AccountID:        snap.GetAccountId(),
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

	intents := resp.GetIntents()
	out := make([]intentJSON, 0, len(intents))
	for _, it := range intents {
		out = append(out, intentJSON{
			Time:           protoTime(it.GetTime()),
			IntentID:       it.GetIntentId(),
			AccountID:      it.GetAccountId(),
			Symbol:         it.GetSymbol(),
			Side:           it.GetSide(),
			RequestedQty:   it.GetRequestedQty(),
			RequestedPrice: it.GetRequestedPrice(),
			StrategyID:     it.GetStrategyId(),
			Market:         orderMarketLabel(it.GetMarket()),
			SessionID:      it.GetSessionId(),
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
		Time            string  `json:"time"`
		OrderID         string  `json:"order_id"`
		ExchangeOrderID string  `json:"exchange_order_id,omitempty"`
		ClientOrderID   string  `json:"client_order_id,omitempty"`
		AttemptID       string  `json:"attempt_id,omitempty"`
		IntentID        string  `json:"intent_id,omitempty"`
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
		ErrorMessage    string  `json:"error_message,omitempty"`
	}

	orders := resp.GetOrders()
	out := make([]orderJSON, 0, len(orders))
	for _, o := range orders {
		out = append(out, orderJSON{
			Time:            protoTime(o.GetTime()),
			OrderID:         o.GetOrderId(),
			ExchangeOrderID: o.GetExchangeOrderId(),
			ClientOrderID:   o.GetClientOrderId(),
			AttemptID:       o.GetAttemptId(),
			IntentID:        o.GetIntentId(),
			Symbol:          o.GetSymbol(),
			Side:            o.GetSide(),
			OrigQty:         o.GetOrigQty(),
			ExecutedQty:     o.GetExecutedQty(),
			RemainingQty:    o.GetRemainingQty(),
			AvgPrice:        o.GetAvgPrice(),
			Price:           o.GetPrice(),
			Status:          o.GetStatus(),
			Market:          orderMarketLabel(o.GetMarket()),
			StrategyID:      o.GetStrategyId(),
			ErrorMessage:    o.GetErrorMessage(),
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
		Time            string  `json:"time"`
		AttemptID       string  `json:"attempt_id"`
		IntentID        string  `json:"intent_id,omitempty"`
		OrderID         string  `json:"order_id,omitempty"`
		ExchangeOrderID string  `json:"exchange_order_id,omitempty"`
		ClientOrderID   string  `json:"client_order_id,omitempty"`
		Symbol          string  `json:"symbol"`
		Side            string  `json:"side"`
		RequestedQty    float64 `json:"requested_qty"`
		RequestedPrice  float64 `json:"requested_price"`
		MarkPrice       float64 `json:"mark_price"`
		Status          string  `json:"status"`
		Market          string  `json:"market"`
		StrategyID      int64   `json:"strategy_id"`
		ErrorMessage    string  `json:"error_message,omitempty"`
		RecoveryError   string  `json:"recovery_error,omitempty"`
	}

	attempts := resp.GetAttempts()
	out := make([]attemptJSON, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, attemptJSON{
			Time:            protoTime(a.GetTime()),
			AttemptID:       a.GetAttemptId(),
			IntentID:        a.GetIntentId(),
			OrderID:         a.GetOrderId(),
			ExchangeOrderID: a.GetExchangeOrderId(),
			ClientOrderID:   a.GetClientOrderId(),
			Symbol:          a.GetSymbol(),
			Side:            a.GetSide(),
			RequestedQty:    a.GetRequestedQty(),
			RequestedPrice:  a.GetRequestedPrice(),
			MarkPrice:       a.GetMarkPrice(),
			Status:          a.GetStatus(),
			Market:          orderMarketLabel(a.GetMarket()),
			StrategyID:      a.GetStrategyId(),
			ErrorMessage:    a.GetErrorMessage(),
			RecoveryError:   a.GetRecoveryError(),
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
		Time            string  `json:"time"`
		FillID          string  `json:"fill_id"`
		ExchangeTradeID string  `json:"exchange_trade_id,omitempty"`
		OrderID         string  `json:"order_id"`
		ExchangeOrderID string  `json:"exchange_order_id,omitempty"`
		AttemptID       string  `json:"attempt_id,omitempty"`
		IntentID        string  `json:"intent_id,omitempty"`
		Symbol          string  `json:"symbol"`
		Side            string  `json:"side"`
		Qty             float64 `json:"qty"`
		FillPrice       float64 `json:"fill_price"`
		Fee             float64 `json:"fee"`
		Status          string  `json:"status"`
		Market          string  `json:"market"`
		StrategyID      int64   `json:"strategy_id"`
	}

	fills := resp.GetFills()
	out := make([]fillJSON, 0, len(fills))
	for _, f := range fills {
		out = append(out, fillJSON{
			Time:            protoTime(f.GetTime()),
			FillID:          f.GetFillId(),
			ExchangeTradeID: f.GetExchangeTradeId(),
			OrderID:         f.GetOrderId(),
			ExchangeOrderID: f.GetExchangeOrderId(),
			AttemptID:       f.GetAttemptId(),
			IntentID:        f.GetIntentId(),
			Symbol:          f.GetSymbol(),
			Side:            f.GetSide(),
			Qty:             f.GetQty(),
			FillPrice:       f.GetFillPrice(),
			Fee:             f.GetFee(),
			Status:          f.GetStatus(),
			Market:          orderMarketLabel(f.GetMarket()),
			StrategyID:      f.GetStrategyId(),
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

func (s *server) getSessionReconciliation(w http.ResponseWriter, r *http.Request, sessionID string) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	limit, offset := parseAuditListPaging(r)

	resp, err := s.accounts.ListReconciliationRuns(r.Context(), &accountv1.ListReconciliationRunsRequest{
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
		AccountID            int64           `json:"account_id"`
		StrategyID           int64           `json:"strategy_id"`
		SessionID            string          `json:"session_id"`
		SnapshotReason       int32           `json:"snapshot_reason"`
		RunType              string          `json:"run_type"`
		Mode                 int32           `json:"mode"`
		HardPass             bool            `json:"hard_pass"`
		SoftPass             bool            `json:"soft_pass"`
		HardFailCount        int             `json:"hard_fail_count"`
		SoftFailCount        int             `json:"soft_fail_count"`
		AdvisoryCount        int             `json:"advisory_count"`
		FieldDiffs           []fieldDiffJSON `json:"field_diffs"`
		AdvisoryDiffs        []fieldDiffJSON `json:"advisory_diffs"`
		LocalSnapshotJSON    string          `json:"local_snapshot_json"`
		ExchangeSnapshotJSON string          `json:"exchange_snapshot_json"`
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
	toFieldDiff := func(d *accountv1.FieldDiffEntry) fieldDiffJSON {
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
			AccountID:            run.GetAccountId(),
			StrategyID:           run.GetStrategyId(),
			SessionID:            run.GetSessionId(),
			SnapshotReason:       run.GetSnapshotReason(),
			RunType:              run.GetRunType(),
			Mode:                 run.GetMode(),
			HardPass:             run.GetHardPass(),
			SoftPass:             run.GetSoftPass(),
			HardFailCount:        hardFailCount,
			SoftFailCount:        softFailCount,
			AdvisoryCount:        len(advisoryDiffs),
			FieldDiffs:           fieldDiffs,
			AdvisoryDiffs:        advisoryDiffs,
			LocalSnapshotJSON:    run.GetLocalSnapshotJson(),
			ExchangeSnapshotJSON: run.GetExchangeSnapshotJson(),
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
	resp, err := s.accounts.GetSessionReconciliationSummary(r.Context(), &accountv1.GetSessionReconciliationSummaryRequest{
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
	SessionID      string `json:"session_id"`
	AccountID      int64  `json:"account_id"`
	StrategyID     int64  `json:"strategy_id"`
	Mode           int32  `json:"mode"`
	Status         string `json:"status"`
	Interval       string `json:"interval"`
	StartTimeMs    int64  `json:"start_time_ms,omitempty"`
	EndTimeMs      int64  `json:"end_time_ms,omitempty"`
	BarsProcessed  int32  `json:"bars_processed"`
	Error          string `json:"error,omitempty"`
	RuntimeID      string `json:"runtime_id,omitempty"`
	RuntimeSource  string `json:"runtime_source,omitempty"`
	RuntimeName    string `json:"runtime_name,omitempty"`
	SessionType    string `json:"session_type,omitempty"`
	RuntimeVersion string `json:"runtime_version,omitempty"`
	SessionName    string `json:"session_name,omitempty"`
	StartedAt      string `json:"started_at"`
	CompletedAt    string `json:"completed_at,omitempty"`
}

func protoSessionToJSON(se *accountv1.StrategySessionEntry) sessionJSON {
	if se == nil {
		return sessionJSON{}
	}
	j := sessionJSON{
		SessionID:      se.GetSessionId(),
		AccountID:      se.GetAccountId(),
		StrategyID:     se.GetStrategyId(),
		Mode:           se.GetMode(),
		Status:         se.GetStatus(),
		Interval:       se.GetInterval(),
		StartTimeMs:    se.GetStartTimeMs(),
		EndTimeMs:      se.GetEndTimeMs(),
		BarsProcessed:  se.GetBarsProcessed(),
		Error:          se.GetError(),
		RuntimeID:      se.GetRuntimeId(),
		RuntimeSource:  se.GetRuntimeSource(),
		RuntimeName:    se.GetRuntimeName(),
		SessionType:    se.GetSessionType(),
		RuntimeVersion: se.GetRuntimeVersion(),
		SessionName:    se.GetSessionName(),
	}
	if ts := se.GetStartedAt(); ts != nil && ts.IsValid() {
		j.StartedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	if ts := se.GetCompletedAt(); ts != nil && ts.IsValid() {
		j.CompletedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	return j
}
