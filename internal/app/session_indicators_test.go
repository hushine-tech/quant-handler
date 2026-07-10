package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/core-service/gen/portfoliov1"
)

func TestHandleSessions_RoutesStrategyIndicatorDefinitions(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		indicatorDefinitionsResp: &portfoliov1.ListStrategyIndicatorsResponse{
			Definitions: []*portfoliov1.StrategyIndicatorDefinition{
				{
					SessionId:    "sess-1",
					StrategyId:   19,
					StreamKey:    "binance:perpetual_futures:ETHUSDT:1m",
					IndicatorKey: "alpha_score",
					Name:         "Alpha Score",
					Type:         "line",
					Pane:         "strategy",
					Color:        "#2563eb",
					Unit:         "score",
					Description:  "debug score",
					ConfigJson:   `{"width":2}`,
				},
			},
		},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/indicators?stream_key=binance:perpetual_futures:ETHUSDT:1m", nil), 6)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if acct.lastIndicatorDefinitionsReq == nil {
		t.Fatalf("expected ListStrategyIndicators RPC to be called")
	}
	if got := acct.lastIndicatorDefinitionsReq.GetSessionId(); got != "sess-1" {
		t.Errorf("grpc session_id = %q, want sess-1", got)
	}
	if got := acct.lastIndicatorDefinitionsReq.GetStreamKey(); got != "binance:perpetual_futures:ETHUSDT:1m" {
		t.Errorf("grpc stream_key = %q", got)
	}
	if got := acct.lastIndicatorDefinitionsReq.GetUserId(); got != 6 {
		t.Errorf("grpc user_id = %d, want 6", got)
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(body.Items))
	}
	if got := body.Items[0]["indicator_key"]; got != "alpha_score" {
		t.Errorf("indicator_key = %v, want alpha_score", got)
	}
	if got := body.Items[0]["config_json"]; got != `{"width":2}` {
		t.Errorf("config_json = %v", got)
	}
}

func TestHandleSessions_RoutesStrategyIndicatorChunks(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		indicatorChunksResp: &portfoliov1.ListStrategyIndicatorChunksResponse{
			Chunks: []*portfoliov1.StrategyIndicatorChunk{
				{
					SessionId:    "sess-1",
					StreamKey:    "binance:perpetual_futures:ETHUSDT:1m",
					IndicatorKey: "alpha_score",
					ChunkIndex:   3,
					StartTimeMs:  1710000000000,
					EndTimeMs:    1710000060000,
					IntervalMs:   60000,
					Count:        2,
					ValuesJson:   `[1.25,null]`,
				},
			},
		},
	}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/indicators/chunks?stream_key=binance:perpetual_futures:ETHUSDT:1m&keys=alpha_score,signal&start_time_ms=1710000000000&end_time_ms=1710000600000", nil), 6)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if acct.lastIndicatorChunksReq == nil {
		t.Fatalf("expected ListStrategyIndicatorChunks RPC to be called")
	}
	if got := acct.lastIndicatorChunksReq.GetSessionId(); got != "sess-1" {
		t.Errorf("grpc session_id = %q, want sess-1", got)
	}
	if got := acct.lastIndicatorChunksReq.GetStreamKey(); got != "binance:perpetual_futures:ETHUSDT:1m" {
		t.Errorf("grpc stream_key = %q", got)
	}
	if got := acct.lastIndicatorChunksReq.GetIndicatorKeys(); len(got) != 2 || got[0] != "alpha_score" || got[1] != "signal" {
		t.Errorf("grpc indicator_keys = %#v", got)
	}
	if got := acct.lastIndicatorChunksReq.GetStartTimeMs(); got != 1710000000000 {
		t.Errorf("grpc start_time_ms = %d", got)
	}
	if got := acct.lastIndicatorChunksReq.GetEndTimeMs(); got != 1710000600000 {
		t.Errorf("grpc end_time_ms = %d", got)
	}
	if got := acct.lastIndicatorChunksReq.GetUserId(); got != 6 {
		t.Errorf("grpc user_id = %d, want 6", got)
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(body.Items))
	}
	if got := body.Items[0]["values_json"]; got != `[1.25,null]` {
		t.Errorf("values_json = %v", got)
	}
}

func TestHandleSessions_StrategyIndicatorChunksRequiresRange(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{}
	s := &server{portfolios: acct, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/sessions/sess-1/indicators/chunks?stream_key=binance:perpetual_futures:ETHUSDT:1m", nil), 6)
	rec := httptest.NewRecorder()
	s.handleSessions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if acct.lastIndicatorChunksReq != nil {
		t.Fatalf("ListStrategyIndicatorChunks should not be called on invalid range")
	}
}
