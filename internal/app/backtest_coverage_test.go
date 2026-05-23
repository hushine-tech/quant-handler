package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMarketDataCoverageRoute(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)

	req := withUID(httptest.NewRequest(
		http.MethodGet,
		"/api/market-data/coverage?exchange=binance&market=futures&kind=kline&symbol=ETHUSDT&interval=1m&start_time_ms=1779033600000&end_time_ms=1779037200000",
		nil,
	), 6)
	rec := httptest.NewRecorder()

	s.handleMarketData(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out marketDataCoverageJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Complete {
		t.Fatal("complete=true want false")
	}
	if len(out.MissingSegments) != 1 {
		t.Fatalf("missing=%d want 1", len(out.MissingSegments))
	}
	if fake.lastCoverageReq.GetKey().GetSymbol() != "ETHUSDT" {
		t.Fatalf("coverage symbol = %q", fake.lastCoverageReq.GetKey().GetSymbol())
	}
}

func TestCoveragePreviewUsesDeclaredInputs(t *testing.T) {
	body := bytes.NewBufferString(`{"start_time_ms":1779033600000,"end_time_ms":1779037200000}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/accounts/7/strategy/coverage-preview", body), 6)
	rec := httptest.NewRecorder()
	s := &server{
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
		marketData:  &fakeMarketDataClient{},
		strategy: &fakeStrategyClient{previewResp: &strategyv1.PreviewRunStrategyResponse{
			Profile:   "backtest",
			Supported: true,
			Ok:        true,
			DeclaredInputs: []*strategyv1.LiveStreamBinding{{
				Exchange: "binance",
				Market:   "futures",
				Kind:     "kline",
				Symbol:   "ETHUSDT",
				Interval: "1m",
			}},
		}},
	}

	s.handleCoveragePreview(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out coveragePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Complete {
		t.Fatal("complete=true want false")
	}
	if len(out.Inputs) != 1 {
		t.Fatalf("inputs=%d want 1", len(out.Inputs))
	}
	if out.Inputs[0].Key.Symbol != "ETHUSDT" {
		t.Fatalf("input symbol=%q", out.Inputs[0].Key.Symbol)
	}
}

func TestDownloadAndRunCreatesJob(t *testing.T) {
	fakeMarket := &fakeMarketDataClient{
		coverageResp: &mdv1CoverageComplete,
	}
	fakeStrategy := &fakeStrategyClient{previewResp: &strategyv1.PreviewRunStrategyResponse{
		Profile:   "backtest",
		Supported: true,
		Ok:        true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{{
			Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m",
		}},
	}}
	s := &server{
		jwtSecret:       []byte("s"),
		corsOrigins:     []string{"*"},
		marketData:      fakeMarket,
		strategy:        fakeStrategy,
		downloadRunJobs: newDownloadRunJobStore(),
	}

	body := bytes.NewBufferString(`{"start_time_ms":1779033600000,"end_time_ms":1779037200000,"interval":"1m"}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/accounts/7/strategy/download-and-run", body), 6)
	rec := httptest.NewRecorder()

	s.handleDownloadAndRun(rec, req, 7)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var job downloadRunJob
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if job.JobID == "" {
		t.Fatal("job_id is empty")
	}
}

var mdv1CoverageComplete = mdv1.QueryMarketDataCoverageResponse{
	Complete:         true,
	ExpectedCount:    60,
	CoveredCount:     60,
	RequestedStartAt: timestamppb.New(time.UnixMilli(1779033600000)),
	RequestedEndAt:   timestamppb.New(time.UnixMilli(1779037200000)),
	Key: &mdv1.StreamKey{
		Exchange: "binance",
		Market:   "futures",
		Kind:     "kline",
		Symbol:   "ETHUSDT",
		Interval: "1m",
	},
}
