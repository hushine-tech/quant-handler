package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/hushine-tech/core-service/gen/accountv1"
)

type strategyIndicatorDefinitionJSON struct {
	SessionID    string `json:"session_id"`
	StrategyID   int64  `json:"strategy_id"`
	StreamKey    string `json:"stream_key"`
	IndicatorKey string `json:"indicator_key"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Pane         string `json:"pane"`
	Color        string `json:"color"`
	Unit         string `json:"unit"`
	Description  string `json:"description"`
	ConfigJSON   string `json:"config_json"`
}

type strategyIndicatorChunkJSON struct {
	SessionID    string `json:"session_id"`
	StreamKey    string `json:"stream_key"`
	IndicatorKey string `json:"indicator_key"`
	ChunkIndex   int32  `json:"chunk_index"`
	StartTimeMS  int64  `json:"start_time_ms"`
	EndTimeMS    int64  `json:"end_time_ms"`
	IntervalMS   int64  `json:"interval_ms"`
	Count        int32  `json:"count"`
	ValuesJSON   string `json:"values_json"`
}

func (s *server) getSessionIndicators(w http.ResponseWriter, r *http.Request, sessionID string) {
	if s.accounts == nil {
		writeErr(w, http.StatusServiceUnavailable, "core-service not configured")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	resp, err := s.accounts.ListStrategyIndicators(r.Context(), &accountv1.ListStrategyIndicatorsRequest{
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
	if s.accounts == nil {
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

	resp, err := s.accounts.ListStrategyIndicatorChunks(r.Context(), &accountv1.ListStrategyIndicatorChunksRequest{
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

func strategyIndicatorDefinitionToJSON(def *accountv1.StrategyIndicatorDefinition) strategyIndicatorDefinitionJSON {
	if def == nil {
		return strategyIndicatorDefinitionJSON{}
	}
	return strategyIndicatorDefinitionJSON{
		SessionID:    def.GetSessionId(),
		StrategyID:   def.GetStrategyId(),
		StreamKey:    def.GetStreamKey(),
		IndicatorKey: def.GetIndicatorKey(),
		Name:         def.GetName(),
		Type:         def.GetType(),
		Pane:         def.GetPane(),
		Color:        def.GetColor(),
		Unit:         def.GetUnit(),
		Description:  def.GetDescription(),
		ConfigJSON:   def.GetConfigJson(),
	}
}

func strategyIndicatorChunkToJSON(chunk *accountv1.StrategyIndicatorChunk) strategyIndicatorChunkJSON {
	if chunk == nil {
		return strategyIndicatorChunkJSON{}
	}
	return strategyIndicatorChunkJSON{
		SessionID:    chunk.GetSessionId(),
		StreamKey:    chunk.GetStreamKey(),
		IndicatorKey: chunk.GetIndicatorKey(),
		ChunkIndex:   chunk.GetChunkIndex(),
		StartTimeMS:  chunk.GetStartTimeMs(),
		EndTimeMS:    chunk.GetEndTimeMs(),
		IntervalMS:   chunk.GetIntervalMs(),
		Count:        chunk.GetCount(),
		ValuesJSON:   chunk.GetValuesJson(),
	}
}
