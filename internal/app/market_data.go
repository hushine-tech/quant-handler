package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ── request / response JSON shapes ──────────────────────────────────────────

type streamKeyJSON struct {
	Exchange string `json:"exchange"`
	Market   string `json:"market"`
	Kind     string `json:"kind"`
	Symbol   string `json:"symbol"`
	Interval string `json:"interval"`
}

type marketDataStreamJSON struct {
	StreamID              int64         `json:"stream_id"`
	Key                   streamKeyJSON `json:"key"`
	DesiredState          string        `json:"desired_state"`
	ActualState           string        `json:"actual_state"`
	EffectiveLiveDelivery bool          `json:"effective_live_delivery"`
	LastDataAt            string        `json:"last_data_at,omitempty"`
	LastError             string        `json:"last_error,omitempty"`
	LastReconciledAt      string        `json:"last_reconciled_at,omitempty"`
	ActiveLeaseCount      int32         `json:"active_lease_count"`
	CreatedAt             string        `json:"created_at"`
	UpdatedAt             string        `json:"updated_at"`
}

type marketDataRequestJSON struct {
	RequestID         int64         `json:"request_id"`
	UserID            int64         `json:"user_id"`
	AccountID         int64         `json:"account_id,omitempty"`
	StreamID          int64         `json:"stream_id"`
	Key               streamKeyJSON `json:"key"`
	Scope             string        `json:"scope"`
	NeedsLiveDelivery bool          `json:"needs_live_delivery"`
	Status            string        `json:"status"`
	RequestedStartAt  string        `json:"requested_start_at,omitempty"`
	RequestedEndAt    string        `json:"requested_end_at,omitempty"`
	CoveredStartAt    string        `json:"covered_start_at,omitempty"`
	CoveredEndAt      string        `json:"covered_end_at,omitempty"`
	LastError         string        `json:"last_error,omitempty"`
	Ready             bool          `json:"ready"`
	CreatedAt         string        `json:"created_at"`
	UpdatedAt         string        `json:"updated_at"`
	CancelledAt       string        `json:"cancelled_at,omitempty"`
}

type marketDataTimeRangeJSON struct {
	StartAt       string `json:"start_at"`
	EndAt         string `json:"end_at"`
	ExpectedCount int64  `json:"expected_count,omitempty"`
}

type marketDataCoverageSegmentJSON struct {
	Key      streamKeyJSON `json:"key"`
	Year     int32         `json:"year"`
	StartAt  string        `json:"start_at"`
	EndAt    string        `json:"end_at"`
	RowCount int64         `json:"row_count"`
	Source   string        `json:"source,omitempty"`
}

type marketDataCoverageJSON struct {
	Key                   streamKeyJSON                   `json:"key"`
	RequestedStartAt      string                          `json:"requested_start_at"`
	RequestedEndAt        string                          `json:"requested_end_at"`
	Complete              bool                            `json:"complete"`
	ExpectedCount         int64                           `json:"expected_count"`
	CoveredCount          int64                           `json:"covered_count"`
	CoveredSegments       []marketDataCoverageSegmentJSON `json:"covered_segments"`
	MissingSegments       []marketDataTimeRangeJSON       `json:"missing_segments"`
	NonDownloadableReason string                          `json:"non_downloadable_reason,omitempty"`
}

type marketDataKlineJSON struct {
	OpenTime  string  `json:"open_time"`
	CloseTime string  `json:"close_time"`
	Open      float64 `json:"open"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Close     float64 `json:"close"`
	Volume    float64 `json:"volume"`
}

type marketDataKlinesJSON struct {
	Key              streamKeyJSON         `json:"key"`
	RequestedStartAt string                `json:"requested_start_at"`
	RequestedEndAt   string                `json:"requested_end_at"`
	Rows             []marketDataKlineJSON `json:"rows"`
	RowCount         int64                 `json:"row_count"`
	Truncated        bool                  `json:"truncated"`
	Limit            int32                 `json:"limit"`
}

type marketDataEntryJSON struct {
	Request marketDataRequestJSON `json:"request"`
	Stream  marketDataStreamJSON  `json:"stream"`
}

type streamDeliveryLeaseJSON struct {
	LeaseID         string `json:"lease_id"`
	SubscriptionID  int64  `json:"subscription_id"`
	OwnerInstanceID string `json:"owner_instance_id"`
	Status          string `json:"status"`
	AcquiredAt      string `json:"acquired_at,omitempty"`
	LastHeartbeatAt string `json:"last_heartbeat_at,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	LastDeliveryAt  string `json:"last_delivery_at,omitempty"`
	LastTopic       string `json:"last_topic,omitempty"`
	LastPartition   int32  `json:"last_partition,omitempty"`
	LastOffset      int64  `json:"last_offset,omitempty"`
}

type streamDeliveryFailureJSON struct {
	FailureID       int64  `json:"failure_id"`
	SubscriptionID  int64  `json:"subscription_id"`
	OwnerInstanceID string `json:"owner_instance_id,omitempty"`
	Topic           string `json:"topic,omitempty"`
	StreamKey       string `json:"stream_key,omitempty"`
	FailureCode     string `json:"failure_code"`
	Reason          string `json:"reason"`
	FirstSeenAt     string `json:"first_seen_at,omitempty"`
	LastSeenAt      string `json:"last_seen_at,omitempty"`
	AttemptCount    int32  `json:"attempt_count"`
}

type sessionDeliveryHealthJSON struct {
	Subscription  sessionMarketDataSubscriptionJSON `json:"subscription"`
	Lease         *streamDeliveryLeaseJSON          `json:"lease,omitempty"`
	LatestFailure *streamDeliveryFailureJSON        `json:"latest_failure,omitempty"`
	HealthStatus  string                            `json:"health_status"`
	BlockedReason string                            `json:"blocked_reason,omitempty"`
	ObservedAt    string                            `json:"observed_at,omitempty"`
}

type sessionMarketDataSubscriptionJSON struct {
	SubscriptionID int64         `json:"subscription_id"`
	UserID         int64         `json:"user_id"`
	SessionID      string        `json:"session_id"`
	RuntimeID      string        `json:"runtime_id"`
	Key            streamKeyJSON `json:"key"`
	Mode           int32         `json:"mode"`
	Status         string        `json:"status"`
	CreatedAt      string        `json:"created_at,omitempty"`
	UpdatedAt      string        `json:"updated_at,omitempty"`
	ReleasedAt     string        `json:"released_at,omitempty"`
}

type sessionDeliveryHealthListJSON struct {
	Items []sessionDeliveryHealthJSON `json:"items"`
}

type createMarketDataRequestBody struct {
	// client may send a flat object or nested {key: {...}}; we accept both.
	Exchange          string         `json:"exchange"`
	Market            string         `json:"market"`
	Kind              string         `json:"kind"`
	Symbol            string         `json:"symbol"`
	Interval          string         `json:"interval"`
	Key               *streamKeyJSON `json:"key"`
	Scope             string         `json:"scope"`
	StartTimeMs       int64          `json:"start_time_ms"`
	EndTimeMs         int64          `json:"end_time_ms"`
	AccountID         int64          `json:"account_id"`
	NeedsLiveDelivery *bool          `json:"needs_live_delivery"`
}

// ── proto converters ────────────────────────────────────────────────────────

func streamKeyToJSON(k *mdv1.StreamKey) streamKeyJSON {
	if k == nil {
		return streamKeyJSON{}
	}
	return streamKeyJSON{
		Exchange: k.GetExchange(),
		Market:   k.GetMarket(),
		Kind:     k.GetKind(),
		Symbol:   k.GetSymbol(),
		Interval: k.GetInterval(),
	}
}

func streamToJSON(s *mdv1.MarketDataStream) marketDataStreamJSON {
	if s == nil {
		return marketDataStreamJSON{}
	}
	j := marketDataStreamJSON{
		StreamID:              s.GetStreamId(),
		Key:                   streamKeyToJSON(s.GetKey()),
		DesiredState:          s.GetDesiredState(),
		ActualState:           s.GetActualState(),
		EffectiveLiveDelivery: s.GetEffectiveLiveDelivery(),
		LastError:             s.GetLastError(),
		ActiveLeaseCount:      s.GetActiveLeaseCount(),
	}
	if s.GetCreatedAt() != nil {
		j.CreatedAt = s.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if s.GetUpdatedAt() != nil {
		j.UpdatedAt = s.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if s.GetLastDataAt() != nil {
		j.LastDataAt = s.GetLastDataAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if s.GetLastReconciledAt() != nil {
		j.LastReconciledAt = s.GetLastReconciledAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	return j
}

func requestToJSON(r *mdv1.MarketDataRequest) marketDataRequestJSON {
	if r == nil {
		return marketDataRequestJSON{}
	}
	j := marketDataRequestJSON{
		RequestID:         r.GetRequestId(),
		UserID:            r.GetUserId(),
		AccountID:         r.GetAccountId(),
		StreamID:          r.GetStreamId(),
		Key:               streamKeyToJSON(r.GetKey()),
		Scope:             r.GetScope(),
		NeedsLiveDelivery: r.GetNeedsLiveDelivery(),
		Status:            r.GetStatus(),
		LastError:         r.GetLastError(),
		Ready:             r.GetReady(),
	}
	if r.GetCreatedAt() != nil {
		j.CreatedAt = r.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if r.GetUpdatedAt() != nil {
		j.UpdatedAt = r.GetUpdatedAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if r.GetCancelledAt() != nil {
		j.CancelledAt = r.GetCancelledAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if r.GetRequestedStartAt() != nil {
		j.RequestedStartAt = r.GetRequestedStartAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if r.GetRequestedEndAt() != nil {
		j.RequestedEndAt = r.GetRequestedEndAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if r.GetCoveredStartAt() != nil {
		j.CoveredStartAt = r.GetCoveredStartAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	if r.GetCoveredEndAt() != nil {
		j.CoveredEndAt = r.GetCoveredEndAt().AsTime().UTC().Format(time.RFC3339Nano)
	}
	return j
}

func formatProtoTime(ts *timestamppb.Timestamp) string {
	if ts == nil || !ts.IsValid() {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339Nano)
}

func sessionSubscriptionToJSON(s *mdv1.SessionMarketDataSubscription) sessionMarketDataSubscriptionJSON {
	if s == nil {
		return sessionMarketDataSubscriptionJSON{}
	}
	return sessionMarketDataSubscriptionJSON{
		SubscriptionID: s.GetSubscriptionId(),
		UserID:         s.GetUserId(),
		SessionID:      s.GetSessionId(),
		RuntimeID:      s.GetRuntimeId(),
		Key:            streamKeyToJSON(s.GetKey()),
		Mode:           s.GetMode(),
		Status:         s.GetStatus(),
		CreatedAt:      formatProtoTime(s.GetCreatedAt()),
		UpdatedAt:      formatProtoTime(s.GetUpdatedAt()),
		ReleasedAt:     formatProtoTime(s.GetReleasedAt()),
	}
}

func deliveryLeaseToJSON(l *mdv1.StreamDeliveryLease) *streamDeliveryLeaseJSON {
	if l == nil {
		return nil
	}
	return &streamDeliveryLeaseJSON{
		LeaseID:         l.GetLeaseId(),
		SubscriptionID:  l.GetSubscriptionId(),
		OwnerInstanceID: l.GetOwnerInstanceId(),
		Status:          l.GetStatus(),
		AcquiredAt:      formatProtoTime(l.GetAcquiredAt()),
		LastHeartbeatAt: formatProtoTime(l.GetLastHeartbeatAt()),
		ExpiresAt:       formatProtoTime(l.GetExpiresAt()),
		LastDeliveryAt:  formatProtoTime(l.GetLastDeliveryAt()),
		LastTopic:       l.GetLastTopic(),
		LastPartition:   l.GetLastPartition(),
		LastOffset:      l.GetLastOffset(),
	}
}

func deliveryFailureToJSON(f *mdv1.StreamDeliveryFailure) *streamDeliveryFailureJSON {
	if f == nil {
		return nil
	}
	return &streamDeliveryFailureJSON{
		FailureID:       f.GetFailureId(),
		SubscriptionID:  f.GetSubscriptionId(),
		OwnerInstanceID: f.GetOwnerInstanceId(),
		Topic:           f.GetTopic(),
		StreamKey:       f.GetStreamKey(),
		FailureCode:     f.GetFailureCode(),
		Reason:          f.GetReason(),
		FirstSeenAt:     formatProtoTime(f.GetFirstSeenAt()),
		LastSeenAt:      formatProtoTime(f.GetLastSeenAt()),
		AttemptCount:    f.GetAttemptCount(),
	}
}

func deliveryHealthToJSON(h *mdv1.SessionDeliveryHealth) sessionDeliveryHealthJSON {
	if h == nil {
		return sessionDeliveryHealthJSON{}
	}
	return sessionDeliveryHealthJSON{
		Subscription:  sessionSubscriptionToJSON(h.GetSubscription()),
		Lease:         deliveryLeaseToJSON(h.GetLease()),
		LatestFailure: deliveryFailureToJSON(h.GetLatestFailure()),
		HealthStatus:  h.GetHealthStatus(),
		BlockedReason: h.GetBlockedReason(),
		ObservedAt:    formatProtoTime(h.GetObservedAt()),
	}
}

// ── route dispatcher ────────────────────────────────────────────────────────

// handleMarketDataRequests routes:
//
//	GET    /api/market-data/requests              → list owned
//	POST   /api/market-data/requests              → create/upsert (needs body)
//	DELETE /api/market-data/requests/{id}         → cancel by id
//	GET    /api/market-data/streams/{id}          → get stream by id
//	GET    /api/market-data/streams?exchange=...  → get stream by key (query)
func (s *server) handleMarketData(w http.ResponseWriter, r *http.Request) {
	if s.marketData == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service is not configured (market-data control plane unavailable)")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/market-data/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	switch parts[0] {
	case "coverage":
		s.handleMarketDataCoverage(w, r)
	case "klines":
		s.handleMarketDataKlines(w, r)
	case "requests":
		s.handleMarketDataRequestsRoute(w, r, parts[1:])
	case "streams":
		s.handleMarketDataStreamsRoute(w, r, parts[1:])
	case "delivery-health":
		s.handleMarketDataDeliveryHealth(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) handleMarketDataRequestsRoute(w http.ResponseWriter, r *http.Request, sub []string) {
	if len(sub) == 0 || sub[0] == "" {
		// /api/market-data/requests
		switch r.Method {
		case http.MethodGet:
			s.listMarketDataRequests(w, r)
		case http.MethodPost:
			s.createMarketDataRequest(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	// /api/market-data/requests/{id}
	id, err := strconv.ParseInt(sub[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "request_id must be an integer")
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.cancelMarketDataRequest(w, r, id)
}

func (s *server) handleMarketDataStreamsRoute(w http.ResponseWriter, r *http.Request, sub []string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(sub) == 0 || sub[0] == "" {
		// /api/market-data/streams?exchange=..&market=..&kind=kline&symbol=..&interval=..
		s.getMarketDataStreamByKey(w, r)
		return
	}
	// /api/market-data/streams/{id}
	id, err := strconv.ParseInt(sub[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "stream_id must be an integer")
		return
	}
	s.getMarketDataStreamByID(w, r, id)
}

// ── handlers ────────────────────────────────────────────────────────────────

func (s *server) createMarketDataRequest(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var body createMarketDataRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Accept either flat or nested key shape.
	keyFlat := streamKeyJSON{
		Exchange: body.Exchange, Market: body.Market,
		Kind: body.Kind, Symbol: body.Symbol, Interval: body.Interval,
	}
	if body.Key != nil {
		keyFlat = *body.Key
	}

	// Default kind to kline for convenience; v1 only supports kline anyway.
	if strings.TrimSpace(keyFlat.Kind) == "" {
		keyFlat.Kind = "kline"
	}
	scope := strings.TrimSpace(strings.ToLower(body.Scope))
	if scope == "" {
		scope = "live"
	}
	if scope != "live" && scope != "historical" {
		writeErr(w, http.StatusBadRequest, "scope must be 'live' or 'historical'")
		return
	}
	// Default live delivery only for live scope. Historical requests never need Kafka.
	needsLive := scope == "live"
	if body.NeedsLiveDelivery != nil {
		needsLive = *body.NeedsLiveDelivery
	}
	if scope == "historical" {
		if body.StartTimeMs <= 0 || body.EndTimeMs <= 0 {
			writeErr(w, http.StatusBadRequest, "historical scope requires start_time_ms and end_time_ms")
			return
		}
		if body.EndTimeMs <= body.StartTimeMs {
			writeErr(w, http.StatusBadRequest, "end_time_ms must be greater than start_time_ms")
			return
		}
		if needsLive {
			writeErr(w, http.StatusBadRequest, "historical scope does not support needs_live_delivery=true")
			return
		}
	}

	req := &mdv1.CreateMarketDataRequestRequest{
		UserId:            uid,
		AccountId:         body.AccountID,
		Key:               &mdv1.StreamKey{Exchange: keyFlat.Exchange, Market: keyFlat.Market, Kind: keyFlat.Kind, Symbol: keyFlat.Symbol, Interval: keyFlat.Interval},
		NeedsLiveDelivery: needsLive,
		Scope:             scope,
	}
	if scope == "historical" {
		req.RequestedStartAt = timestamppb.New(time.UnixMilli(body.StartTimeMs).UTC())
		req.RequestedEndAt = timestamppb.New(time.UnixMilli(body.EndTimeMs).UTC())
	}
	resp, err := s.marketData.CreateMarketDataRequest(r.Context(), req)
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusCreated, marketDataEntryJSON{
		Request: requestToJSON(resp.GetRequest()),
		Stream:  streamToJSON(resp.GetStream()),
	})
}

func (s *server) cancelMarketDataRequest(w http.ResponseWriter, r *http.Request, id int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.marketData.CancelMarketDataRequest(r.Context(), &mdv1.CancelMarketDataRequestRequest{
		RequestId: id,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}

func (s *server) listMarketDataRequests(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	limit, offset := parseCollectionPaging(r)
	page := collectionPageRequested(r)
	req := &mdv1.ListMarketDataRequestsRequest{UserId: uid}
	if page {
		req.Limit = limit
		req.Offset = offset
	}
	resp, err := s.marketData.ListMarketDataRequests(r.Context(), req)
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	out := make([]marketDataEntryJSON, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		out = append(out, marketDataEntryJSON{
			Request: requestToJSON(e.GetRequest()),
			Stream:  streamToJSON(e.GetStream()),
		})
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

func (s *server) getMarketDataStreamByID(w http.ResponseWriter, r *http.Request, id int64) {
	resp, err := s.marketData.GetMarketDataStreamStatus(r.Context(), &mdv1.GetMarketDataStreamStatusRequest{
		StreamId: id,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, streamToJSON(resp.GetStream()))
}

func (s *server) getMarketDataStreamByKey(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := &mdv1.StreamKey{
		Exchange: q.Get("exchange"),
		Market:   q.Get("market"),
		Kind:     q.Get("kind"),
		Symbol:   q.Get("symbol"),
		Interval: q.Get("interval"),
	}
	if key.Kind == "" {
		key.Kind = "kline" // v1 default
	}
	if key.Exchange == "" || key.Market == "" || key.Symbol == "" || key.Interval == "" {
		writeErr(w, http.StatusBadRequest, "exchange, market, symbol, interval are required query params")
		return
	}
	resp, err := s.marketData.GetMarketDataStreamStatus(r.Context(), &mdv1.GetMarketDataStreamStatusRequest{Key: key})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, streamToJSON(resp.GetStream()))
}

func (s *server) handleMarketDataCoverage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key, startMS, endMS, ok := parseCoverageQuery(w, r)
	if !ok {
		return
	}
	resp, err := s.marketData.QueryMarketDataCoverage(r.Context(), &mdv1.QueryMarketDataCoverageRequest{
		Key:     key,
		StartAt: timestamppb.New(time.UnixMilli(startMS).UTC()),
		EndAt:   timestamppb.New(time.UnixMilli(endMS).UTC()),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, coverageToJSON(resp))
}

func (s *server) handleMarketDataKlines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	key, startMS, endMS, ok := parseCoverageQuery(w, r)
	if !ok {
		return
	}
	limit := int64(100)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}
	if limit > 500 {
		limit = 500
	}
	resp, err := s.marketData.QueryMarketDataKlines(r.Context(), &mdv1.QueryMarketDataKlinesRequest{
		Key:     key,
		StartAt: timestamppb.New(time.UnixMilli(startMS).UTC()),
		EndAt:   timestamppb.New(time.UnixMilli(endMS).UTC()),
		Limit:   int32(limit),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, klinesToJSON(resp))
}

func (s *server) handleMarketDataDeliveryHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	q := r.URL.Query()
	resp, err := s.marketData.ListSessionDeliveryHealth(r.Context(), &mdv1.ListSessionDeliveryHealthRequest{
		UserId:    uid,
		SessionId: q.Get("session_id"),
		RuntimeId: q.Get("runtime_id"),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	items := make([]sessionDeliveryHealthJSON, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		items = append(items, deliveryHealthToJSON(item))
	}
	writeJSON(w, http.StatusOK, sessionDeliveryHealthListJSON{Items: items})
}

func parseCoverageQuery(w http.ResponseWriter, r *http.Request) (*mdv1.StreamKey, int64, int64, bool) {
	q := r.URL.Query()
	key := &mdv1.StreamKey{
		Exchange: q.Get("exchange"),
		Market:   q.Get("market"),
		Kind:     q.Get("kind"),
		Symbol:   q.Get("symbol"),
		Interval: q.Get("interval"),
	}
	if strings.TrimSpace(key.Kind) == "" {
		key.Kind = "kline"
	}
	if key.Exchange == "" || key.Market == "" || key.Symbol == "" || key.Interval == "" {
		writeErr(w, http.StatusBadRequest, "exchange, market, symbol, interval are required query params")
		return nil, 0, 0, false
	}
	startMS, err := strconv.ParseInt(q.Get("start_time_ms"), 10, 64)
	if err != nil || startMS <= 0 {
		writeErr(w, http.StatusBadRequest, "start_time_ms must be a positive integer")
		return nil, 0, 0, false
	}
	endMS, err := strconv.ParseInt(q.Get("end_time_ms"), 10, 64)
	if err != nil || endMS <= 0 {
		writeErr(w, http.StatusBadRequest, "end_time_ms must be a positive integer")
		return nil, 0, 0, false
	}
	if endMS <= startMS {
		writeErr(w, http.StatusBadRequest, "end_time_ms must be greater than start_time_ms")
		return nil, 0, 0, false
	}
	return key, startMS, endMS, true
}

func coverageToJSON(resp *mdv1.QueryMarketDataCoverageResponse) marketDataCoverageJSON {
	if resp == nil {
		return marketDataCoverageJSON{}
	}
	covered := make([]marketDataCoverageSegmentJSON, 0, len(resp.GetCoveredSegments()))
	for _, segment := range resp.GetCoveredSegments() {
		covered = append(covered, coverageSegmentToJSON(segment))
	}
	missing := make([]marketDataTimeRangeJSON, 0, len(resp.GetMissingSegments()))
	for _, item := range resp.GetMissingSegments() {
		missing = append(missing, timeRangeToJSON(item))
	}
	return marketDataCoverageJSON{
		Key:                   streamKeyToJSON(resp.GetKey()),
		RequestedStartAt:      formatProtoTime(resp.GetRequestedStartAt()),
		RequestedEndAt:        formatProtoTime(resp.GetRequestedEndAt()),
		Complete:              resp.GetComplete(),
		ExpectedCount:         resp.GetExpectedCount(),
		CoveredCount:          resp.GetCoveredCount(),
		CoveredSegments:       covered,
		MissingSegments:       missing,
		NonDownloadableReason: resp.GetNonDownloadableReason(),
	}
}

func klinesToJSON(resp *mdv1.QueryMarketDataKlinesResponse) marketDataKlinesJSON {
	if resp == nil {
		return marketDataKlinesJSON{}
	}
	rows := make([]marketDataKlineJSON, 0, len(resp.GetRows()))
	for _, row := range resp.GetRows() {
		rows = append(rows, marketDataKlineJSON{
			OpenTime:  formatProtoTime(row.GetOpenTime()),
			CloseTime: formatProtoTime(row.GetCloseTime()),
			Open:      row.GetOpen(),
			High:      row.GetHigh(),
			Low:       row.GetLow(),
			Close:     row.GetClose(),
			Volume:    row.GetVolume(),
		})
	}
	return marketDataKlinesJSON{
		Key:              streamKeyToJSON(resp.GetKey()),
		RequestedStartAt: formatProtoTime(resp.GetRequestedStartAt()),
		RequestedEndAt:   formatProtoTime(resp.GetRequestedEndAt()),
		Rows:             rows,
		RowCount:         resp.GetRowCount(),
		Truncated:        resp.GetTruncated(),
		Limit:            resp.GetLimit(),
	}
}

func coverageSegmentToJSON(segment *mdv1.MarketDataCoverageSegment) marketDataCoverageSegmentJSON {
	if segment == nil {
		return marketDataCoverageSegmentJSON{}
	}
	return marketDataCoverageSegmentJSON{
		Key:      streamKeyToJSON(segment.GetKey()),
		Year:     segment.GetYear(),
		StartAt:  formatProtoTime(segment.GetStartAt()),
		EndAt:    formatProtoTime(segment.GetEndAt()),
		RowCount: segment.GetRowCount(),
		Source:   segment.GetSource(),
	}
}

func timeRangeToJSON(item *mdv1.MarketDataTimeRange) marketDataTimeRangeJSON {
	if item == nil {
		return marketDataTimeRangeJSON{}
	}
	return marketDataTimeRangeJSON{
		StartAt:       formatProtoTime(item.GetStartAt()),
		EndAt:         formatProtoTime(item.GetEndAt()),
		ExpectedCount: item.GetExpectedCount(),
	}
}
