package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/core-service/gen/portfoliov1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestHandleSessions_RoutesStrategyIndicatorDefinitions(t *testing.T) {
	acct := &fakeSessionPortfoliosClient{
		indicatorDefinitionsV2Resp: &portfoliov1.ListStrategyIndicatorsV2Response{
			Definitions: []*portfoliov1.StrategyIndicatorDefinitionV2{
				{
					SessionId:       "sess-1",
					StrategyId:      19,
					StreamKey:       "binance:perpetual_futures:ETHUSDT:1m",
					IndicatorKey:    "alpha_score",
					Name:            "Alpha Score",
					Type:            "line",
					Pane:            "strategy",
					Color:           "#2563eb",
					Unit:            "score",
					Description:     "debug score",
					ConfigJson:      `{"width":2}`,
					ProtocolVersion: 2,
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
	if acct.lastIndicatorDefinitionsV2Req == nil {
		t.Fatalf("expected ListStrategyIndicatorsV2 RPC to be called")
	}
	if got := acct.lastIndicatorDefinitionsV2Req.GetSessionId(); got != "sess-1" {
		t.Errorf("grpc session_id = %q, want sess-1", got)
	}
	if got := acct.lastIndicatorDefinitionsV2Req.GetStreamKey(); got != "binance:perpetual_futures:ETHUSDT:1m" {
		t.Errorf("grpc stream_key = %q", got)
	}
	if got := acct.lastIndicatorDefinitionsV2Req.GetUserId(); got != 6 {
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
	if got := body.Items[0]["protocol_version"]; got != float64(2) {
		t.Errorf("protocol_version = %v, want 2", got)
	}
}

func TestStrategyIndicatorV1Removed(t *testing.T) {
	service := portfoliov1.File_portfolio_service_proto.Services().
		ByName("PortfolioService")
	if service == nil {
		t.Fatal("PortfolioService descriptor is missing")
	}
	for _, method := range []string{
		"ListStrategyIndicators",
		"ListStrategyIndicatorChunks",
	} {
		if service.Methods().ByName(protoreflect.Name(method)) != nil {
			t.Fatalf("indicator V1 method is still present: %s", method)
		}
	}
	for _, method := range []string{
		"ListStrategyIndicatorsV2",
		"ListStrategyIndicatorChunksV2",
	} {
		if service.Methods().ByName(protoreflect.Name(method)) == nil {
			t.Fatalf("indicator V2 method is missing: %s", method)
		}
	}

	current := strategyIndicatorChunkToJSON(
		&portfoliov1.StrategyIndicatorChunkV2{
			SessionId:       "sess-1",
			StreamKey:       "binance:spot:BTCUSDT:1m",
			IndicatorKey:    "alpha",
			Count:           1,
			TimesMs:         []int64{60_000},
			ScalarValues:    []*portfoliov1.NullableDoubleV2{{}},
			Revision:        1,
			ProtocolVersion: 2,
		},
	)
	if current.ProtocolVersion != 2 || len(current.ScalarValues) != 1 {
		t.Fatalf("V2 mapper lost typed fields: %+v", current)
	}
}

func TestHandleSessions_RoutesStrategyIndicatorChunks(t *testing.T) {
	zero := 0.0
	value := 1.25
	acct := &fakeSessionPortfoliosClient{
		indicatorChunksV2Resp: &portfoliov1.ListStrategyIndicatorChunksV2Response{
			Chunks: []*portfoliov1.StrategyIndicatorChunkV2{
				{
					SessionId:     "sess-1",
					StreamKey:     "binance:perpetual_futures:ETHUSDT:1m",
					IndicatorKey:  "alpha_score",
					ChunkIndex:    3,
					StartSequence: 3072,
					EndSequence:   3073,
					StartTimeMs:   1710000000000,
					EndTimeMs:     1710000120000,
					IntervalMs:    60000,
					Count:         2,
					TimesMs:       []int64{1710000000000, 1710000120000},
					ScalarValues: []*portfoliov1.NullableDoubleV2{
						{Value: &value},
						{},
					},
					Markers: []*portfoliov1.StrategyIndicatorMarkerV2{
						{
							Sequence: 3073,
							Offset:   1,
							TimeMs:   1710000120000,
							Text:     "BUY",
							Price:    &zero,
							Color:    "#00ff00",
							Position: "belowBar",
							Shape:    "arrowUp",
						},
					},
					Revision:        2,
					Finalized:       true,
					ProtocolVersion: 2,
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
	if acct.lastIndicatorChunksV2Req == nil {
		t.Fatalf("expected ListStrategyIndicatorChunksV2 RPC to be called")
	}
	if got := acct.lastIndicatorChunksV2Req.GetSessionId(); got != "sess-1" {
		t.Errorf("grpc session_id = %q, want sess-1", got)
	}
	if got := acct.lastIndicatorChunksV2Req.GetStreamKey(); got != "binance:perpetual_futures:ETHUSDT:1m" {
		t.Errorf("grpc stream_key = %q", got)
	}
	if got := acct.lastIndicatorChunksV2Req.GetIndicatorKeys(); len(got) != 2 || got[0] != "alpha_score" || got[1] != "signal" {
		t.Errorf("grpc indicator_keys = %#v", got)
	}
	if got := acct.lastIndicatorChunksV2Req.GetStartTimeMs(); got != 1710000000000 {
		t.Errorf("grpc start_time_ms = %d", got)
	}
	if got := acct.lastIndicatorChunksV2Req.GetEndTimeMs(); got != 1710000600000 {
		t.Errorf("grpc end_time_ms = %d", got)
	}
	if got := acct.lastIndicatorChunksV2Req.GetUserId(); got != 6 {
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
	if _, ok := body.Items[0]["values_json"]; ok {
		t.Fatalf("legacy values_json must not be exposed: %#v", body.Items[0])
	}
	times, ok := body.Items[0]["times_ms"].([]any)
	if !ok || len(times) != 2 || times[1] != float64(1710000120000) {
		t.Fatalf("times_ms = %#v", body.Items[0]["times_ms"])
	}
	values, ok := body.Items[0]["scalar_values"].([]any)
	if !ok || len(values) != 2 || values[0] != 1.25 || values[1] != nil {
		t.Fatalf("scalar_values = %#v", body.Items[0]["scalar_values"])
	}
	markers, ok := body.Items[0]["markers"].([]any)
	if !ok || len(markers) != 1 {
		t.Fatalf("markers = %#v", body.Items[0]["markers"])
	}
	marker := markers[0].(map[string]any)
	if marker["time_ms"] != float64(1710000120000) || marker["price"] != 0.0 ||
		marker["position"] != "belowBar" || marker["shape"] != "arrowUp" {
		t.Fatalf("marker = %#v", marker)
	}
	if body.Items[0]["revision"] != float64(2) || body.Items[0]["finalized"] != true ||
		body.Items[0]["protocol_version"] != float64(2) {
		t.Fatalf("chunk metadata = %#v", body.Items[0])
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
	if acct.lastIndicatorChunksV2Req != nil {
		t.Fatalf("ListStrategyIndicatorChunksV2 should not be called on invalid range")
	}
}
