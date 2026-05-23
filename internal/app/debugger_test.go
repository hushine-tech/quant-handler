package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/controlpanel"
)

func TestAccountDebugDataset_LoadsDataset(t *testing.T) {
	resolver := &fakeResolver{
		debugDataset: controlpanel.DebugDatasetState{
			DatasetID:      "dbg-1",
			AccountID:      7,
			RuntimeID:      "rt-debug",
			Market:         "futures",
			Symbol:         "ETHUSDT",
			Interval:       "1m",
			BarCount:       120,
			CoverageStatus: "complete",
			State:          "active",
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/accounts/7/debug-dataset", bytes.NewBufferString(`{
		"runtime_id":"rt-debug",
		"market":"futures",
		"symbol":"ethusdt",
		"interval":"1m",
		"start_time_ms":1000,
		"end_time_ms":121000
	}`)), 42)
	rec := httptest.NewRecorder()

	s.handleAccountDebugDataset(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.loadDebugCalls != 1 {
		t.Fatalf("LoadDebugDataset calls = %d, want 1", resolver.loadDebugCalls)
	}
	if resolver.gotUserID != 42 || resolver.gotAccountID != 7 || resolver.gotRuntimeID != "rt-debug" {
		t.Fatalf("load args user/account/runtime = %d/%d/%q", resolver.gotUserID, resolver.gotAccountID, resolver.gotRuntimeID)
	}
	if resolver.gotMarket != "futures" || resolver.gotSymbol != "ETHUSDT" || resolver.gotInterval != "1m" {
		t.Fatalf("load args market/symbol/interval = %q/%q/%q", resolver.gotMarket, resolver.gotSymbol, resolver.gotInterval)
	}
	var body debugDatasetJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.DatasetID != "dbg-1" || body.BarCount != 120 {
		t.Fatalf("dataset = %+v", body)
	}
}
