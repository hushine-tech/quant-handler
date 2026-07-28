package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hushine-tech/core-service/gen/portfoliov1"
)

type strategyIndicatorDefinitionJSON struct {
	SessionID       string `json:"session_id"`
	StrategyID      int64  `json:"strategy_id"`
	StreamKey       string `json:"stream_key"`
	IndicatorKey    string `json:"indicator_key"`
	Name            string `json:"name"`
	Type            string `json:"type"`
	Pane            string `json:"pane"`
	Color           string `json:"color"`
	Unit            string `json:"unit"`
	Description     string `json:"description"`
	ConfigJSON      string `json:"config_json"`
	ProtocolVersion uint32 `json:"protocol_version"`
}

type strategyIndicatorMarkerJSON struct {
	Sequence uint64   `json:"sequence"`
	Offset   uint32   `json:"offset"`
	TimeMS   int64    `json:"time_ms"`
	Text     string   `json:"text"`
	Price    *float64 `json:"price,omitempty"`
	Color    string   `json:"color"`
	Position string   `json:"position"`
	Shape    string   `json:"shape"`
}

type strategyIndicatorChunkJSON struct {
	SessionID       string                        `json:"session_id"`
	StreamKey       string                        `json:"stream_key"`
	IndicatorKey    string                        `json:"indicator_key"`
	ChunkIndex      uint32                        `json:"chunk_index"`
	StartSequence   uint64                        `json:"start_sequence"`
	EndSequence     uint64                        `json:"end_sequence"`
	StartTimeMS     int64                         `json:"start_time_ms"`
	EndTimeMS       int64                         `json:"end_time_ms"`
	IntervalMS      int64                         `json:"interval_ms"`
	Count           uint32                        `json:"count"`
	TimesMS         []int64                       `json:"times_ms"`
	ScalarValues    []*float64                    `json:"scalar_values"`
	Markers         []strategyIndicatorMarkerJSON `json:"markers"`
	Revision        uint64                        `json:"revision"`
	Finalized       bool                          `json:"finalized"`
	ProtocolVersion uint32                        `json:"protocol_version"`
}

func (s *server) getSessionIndicators(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s.portfolios == nil {
		writeErr(w, http.StatusServiceUnavailable, "core-service not configured")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	resp, err := s.portfolios.ListStrategyIndicatorsV2(r.Context(), &portfoliov1.ListStrategyIndicatorsV2Request{
		SessionId: strings.TrimSpace(sessionID),
		StreamKey: strings.TrimSpace(r.URL.Query().Get("stream_key")),
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	items := make([]strategyIndicatorDefinitionJSON, 0, len(resp.GetDefinitions()))
	for _, def := range resp.GetDefinitions() {
		items = append(items, strategyIndicatorDefinitionToJSON(def))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *server) getSessionIndicatorChunks(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s.portfolios == nil {
		writeErr(w, http.StatusServiceUnavailable, "core-service not configured")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	q := r.URL.Query()
	streamKey := strings.TrimSpace(q.Get("stream_key"))
	if streamKey == "" {
		writeErr(w, http.StatusBadRequest, "stream_key is required")
		return
	}
	startTimeMS, ok := parseRequiredPositiveInt64(q.Get("start_time_ms"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "start_time_ms must be a positive integer")
		return
	}
	endTimeMS, ok := parseRequiredPositiveInt64(q.Get("end_time_ms"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "end_time_ms must be a positive integer")
		return
	}
	if endTimeMS < startTimeMS {
		writeErr(w, http.StatusBadRequest, "end_time_ms must be greater than or equal to start_time_ms")
		return
	}

	resp, err := s.portfolios.ListStrategyIndicatorChunksV2(r.Context(), &portfoliov1.ListStrategyIndicatorChunksV2Request{
		SessionId:     strings.TrimSpace(sessionID),
		StreamKey:     streamKey,
		IndicatorKeys: parseCommaSeparated(q.Get("keys")),
		StartTimeMs:   startTimeMS,
		EndTimeMs:     endTimeMS,
		UserId:        uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	items := make([]strategyIndicatorChunkJSON, 0, len(resp.GetChunks()))
	for _, chunk := range resp.GetChunks() {
		items = append(items, strategyIndicatorChunkToJSON(chunk))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func parseRequiredPositiveInt64(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0
}

func parseCommaSeparated(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func strategyIndicatorDefinitionToJSON(def *portfoliov1.StrategyIndicatorDefinitionV2) strategyIndicatorDefinitionJSON {
	if def == nil {
		return strategyIndicatorDefinitionJSON{}
	}
	return strategyIndicatorDefinitionJSON{
		SessionID:       def.GetSessionId(),
		StrategyID:      def.GetStrategyId(),
		StreamKey:       def.GetStreamKey(),
		IndicatorKey:    def.GetIndicatorKey(),
		Name:            def.GetName(),
		Type:            def.GetType(),
		Pane:            def.GetPane(),
		Color:           def.GetColor(),
		Unit:            def.GetUnit(),
		Description:     def.GetDescription(),
		ConfigJSON:      def.GetConfigJson(),
		ProtocolVersion: def.GetProtocolVersion(),
	}
}

func strategyIndicatorChunkToJSON(chunk *portfoliov1.StrategyIndicatorChunkV2) strategyIndicatorChunkJSON {
	if chunk == nil {
		return strategyIndicatorChunkJSON{}
	}
	values := make([]*float64, len(chunk.GetScalarValues()))
	for i, value := range chunk.GetScalarValues() {
		if value != nil && value.Value != nil {
			v := value.GetValue()
			values[i] = &v
		}
	}
	markers := make([]strategyIndicatorMarkerJSON, 0, len(chunk.GetMarkers()))
	for _, marker := range chunk.GetMarkers() {
		if marker == nil {
			continue
		}
		var price *float64
		if marker.Price != nil {
			v := marker.GetPrice()
			price = &v
		}
		markers = append(markers, strategyIndicatorMarkerJSON{
			Sequence: marker.GetSequence(),
			Offset:   marker.GetOffset(),
			TimeMS:   marker.GetTimeMs(),
			Text:     marker.GetText(),
			Price:    price,
			Color:    marker.GetColor(),
			Position: marker.GetPosition(),
			Shape:    marker.GetShape(),
		})
	}
	return strategyIndicatorChunkJSON{
		SessionID:       chunk.GetSessionId(),
		StreamKey:       chunk.GetStreamKey(),
		IndicatorKey:    chunk.GetIndicatorKey(),
		ChunkIndex:      chunk.GetChunkIndex(),
		StartSequence:   chunk.GetStartSequence(),
		EndSequence:     chunk.GetEndSequence(),
		StartTimeMS:     chunk.GetStartTimeMs(),
		EndTimeMS:       chunk.GetEndTimeMs(),
		IntervalMS:      chunk.GetIntervalMs(),
		Count:           chunk.GetCount(),
		TimesMS:         append([]int64(nil), chunk.GetTimesMs()...),
		ScalarValues:    values,
		Markers:         markers,
		Revision:        chunk.GetRevision(),
		Finalized:       chunk.GetFinalized(),
		ProtocolVersion: chunk.GetProtocolVersion(),
	}
}
