package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	cerrors "github.com/hushine-tech/golang-lib/pkg/errors"
	errorcodes "github.com/hushine-tech/golang-lib/pkg/errors/codes"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMarketDataCoverageRoute(t *testing.T) {
	fake := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, fake)

	req := withUID(httptest.NewRequest(
		http.MethodGet,
		"/api/market-data/coverage?exchange=binance&market=perpetual_futures&kind=kline&symbol=ETHUSDT&interval=1m&start_time_ms=1779033600000&end_time_ms=1779037200000",
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
	if fake.lastCoverageReq.GetKey().GetMarket() != "futures" {
		t.Fatalf("coverage market = %q, want internal futures", fake.lastCoverageReq.GetKey().GetMarket())
	}
}

func TestCoveragePreviewUsesDeclaredInputs(t *testing.T) {
	body := bytes.NewBufferString(`{"runtime_id":"rt-coverage","start_time_ms":1779033600000,"end_time_ms":1779037200000}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/strategy/coverage-preview", body), 6)
	rec := httptest.NewRecorder()
	proxy := &fakeControlPanelStrategyProxy{previewResp: &strategyv1.PreviewRunStrategyResponse{
		Profile:   "backtest",
		Supported: true,
		Ok:        true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{{
			Exchange: "binance",
			Market:   "perpetual_futures",
			Kind:     "kline",
			Symbol:   "ETHUSDT",
			Interval: "1m",
		}},
	}}
	s := &server{
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
		marketData:  &fakeMarketDataClient{},
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-coverage"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-coverage", Role: "executor"},
		},
		cpRuntime: proxy,
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
	if out.Inputs[0].Key.Market != "perpetual_futures" {
		t.Fatalf("input market=%q, want perpetual_futures", out.Inputs[0].Key.Market)
	}
}

func TestCoveragePreviewUsesRuntimeProxyDeadline(t *testing.T) {
	body := bytes.NewBufferString(`{"runtime_id":"rt-coverage","start_time_ms":1779033600000,"end_time_ms":1779037200000}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/strategy/coverage-preview", body), 6)
	rec := httptest.NewRecorder()
	proxy := &fakeControlPanelStrategyProxy{previewResp: &strategyv1.PreviewRunStrategyResponse{
		Profile:   "backtest",
		Supported: true,
		Ok:        true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{{
			Exchange: "binance",
			Market:   "perpetual_futures",
			Kind:     "kline",
			Symbol:   "ETHUSDT",
			Interval: "1m",
		}},
	}}
	s := &server{
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
		marketData:  &fakeMarketDataClient{},
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-coverage"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-coverage", Role: "executor"},
		},
		cpRuntime: proxy,
	}

	s.handleCoveragePreview(rec, req, 7)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !proxy.previewDeadlineSet {
		t.Fatal("coverage preview downstream call had no deadline")
	}
	if proxy.previewDeadlineUntil <= 0 || proxy.previewDeadlineUntil > previewRunStrategyRPCTimeout {
		t.Fatalf("coverage preview deadline remaining = %v, want within %v", proxy.previewDeadlineUntil, previewRunStrategyRPCTimeout)
	}
}

func TestCoveragePreviewSanitizesRuntimeTimeout(t *testing.T) {
	body := bytes.NewBufferString(`{"runtime_id":"rt-coverage","start_time_ms":1779033600000,"end_time_ms":1779037200000}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/strategy/coverage-preview", body), 6)
	rec := httptest.NewRecorder()
	proxy := &fakeControlPanelStrategyProxy{
		previewErr: cerrors.New(errorcodes.Timeout, http.StatusGatewayTimeout, "stream terminated by RST_STREAM with error code: CANCEL"),
	}
	s := &server{
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
		marketData:  &fakeMarketDataClient{},
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-coverage"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-coverage", Role: "executor"},
		},
		cpRuntime: proxy,
	}

	s.handleCoveragePreview(rec, req, 7)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d want 504 body=%s", rec.Code, rec.Body.String())
	}
	var bodyJSON struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bodyJSON); err != nil {
		t.Fatal(err)
	}
	if contains(bodyJSON.Error, "10002") || contains(bodyJSON.Error, "RST_STREAM") || contains(bodyJSON.Error, "CANCEL") {
		t.Fatalf("coverage error leaks transport detail: %q", bodyJSON.Error)
	}
	if !contains(bodyJSON.Error, "Runtime did not respond in time") {
		t.Fatalf("coverage error = %q, want friendly runtime timeout", bodyJSON.Error)
	}
}

func TestDownloadAndRunCreatesJob(t *testing.T) {
	fakeMarket := &fakeMarketDataClient{
		coverageResp: &mdv1CoverageComplete,
	}
	proxy := &fakeControlPanelStrategyProxy{previewResp: &strategyv1.PreviewRunStrategyResponse{
		Profile:   "backtest",
		Supported: true,
		Ok:        true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{{
			Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m",
		}},
	}}
	s := &server{
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
		marketData:  fakeMarket,
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-download"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-download", Role: "executor"},
		},
		cpRuntime:       proxy,
		downloadRunJobs: newDownloadRunJobStore(),
	}

	body := bytes.NewBufferString(`{"runtime_id":"rt-download","start_time_ms":1779033600000,"end_time_ms":1779037200000,"interval":"1m","max_loss_close_pct":0.25}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/strategy/download-and-run", body), 6)
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
	deadline := time.Now().Add(2 * time.Second)
	var previewReq *strategyv1.PreviewRunStrategyRequest
	var runReq *strategyv1.RunStrategyRequest
	for time.Now().Before(deadline) {
		previewReq, runReq = proxy.strategyRequests()
		if runReq != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if previewReq == nil || runReq == nil {
		t.Fatalf("strategy proxy requests not completed: preview=%v run=%v", previewReq != nil, runReq != nil)
	}
	if got := previewReq.GetMaxLossClosePct(); got != 0.25 {
		t.Fatalf("preview max_loss_close_pct=%v want 0.25", got)
	}
	if got := runReq.GetMaxLossClosePct(); got != 0.25 {
		t.Fatalf("run max_loss_close_pct=%v want 0.25", got)
	}
}

func TestDownloadAndRunJobPreservesPreviewAndRunDependencyErrors(t *testing.T) {
	tests := []struct {
		name       string
		previewErr error
		runErr     error
		wantCode   string
	}{
		{
			name:       "preview",
			previewErr: runtimeDependencyTestError(codes.FailedPrecondition, "STRATEGY_DEPENDENCY_UNAVAILABLE"),
			wantCode:   "STRATEGY_DEPENDENCY_UNAVAILABLE",
		},
		{
			name:     "run",
			runErr:   runtimeDependencyTestError(codes.FailedPrecondition, "STRATEGY_IMPORT_FAILED"),
			wantCode: "STRATEGY_IMPORT_FAILED",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := &fakeControlPanelStrategyProxy{
				previewErr: tt.previewErr,
				runErr:     tt.runErr,
				previewResp: &strategyv1.PreviewRunStrategyResponse{
					Profile: "backtest", Supported: true, Ok: true,
					DeclaredInputs: []*strategyv1.LiveStreamBinding{{
						Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m",
					}},
				},
			}
			store := newDownloadRunJobStore()
			s := &server{
				marketData:      &fakeMarketDataClient{coverageResp: &mdv1CoverageComplete},
				downloadRunJobs: store,
			}
			job := store.create()
			s.runDownloadAndRunJob(context.Background(), job.JobID, proxy, 6, 7, "rt-download", downloadAndRunRequest{
				RuntimeID: "rt-download", StartTimeMS: 1779033600000, EndTimeMS: 1779037200000, Interval: "1m",
			})

			got, ok := store.get(job.JobID)
			if !ok {
				t.Fatal("job missing")
			}
			if got.Status != downloadRunError || got.Error != runtimeDependencyTestMessage {
				t.Fatalf("job status/error = %s/%q", got.Status, got.Error)
			}
			if got.RuntimeError == nil || got.RuntimeError.Code != tt.wantCode || got.RuntimeError.Message != got.Error {
				t.Fatalf("runtime_error = %+v", got.RuntimeError)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if contains(string(encoded), "StringValue") {
				t.Fatalf("job leaks serialized transport error: %s", encoded)
			}
		})
	}
}

func TestDownloadAndRunJobStopsOnStructuredPreviewFailure(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{previewResp: &strategyv1.PreviewRunStrategyResponse{
		Profile: "backtest", Supported: true, Ok: false,
		Failures: []*strategyv1.PreflightFailureProto{{
			Kind: "leverage", Reason: "ETHUSDT leverage read failed", Code: "LEVERAGE_READ_FAILED",
			Exchange: 1, Market: 2, Symbol: "ETHUSDT", VenueId: 8, Environment: 1,
			Retryable: true, Source: "core-service",
		}},
	}}
	store := newDownloadRunJobStore()
	s := &server{marketData: &fakeMarketDataClient{coverageResp: &mdv1CoverageComplete}, downloadRunJobs: store}
	job := store.create()

	s.runDownloadAndRunJob(context.Background(), job.JobID, proxy, 6, 7, "rt-download", downloadAndRunRequest{
		RuntimeID: "rt-download", StartTimeMS: 1779033600000, EndTimeMS: 1779037200000, Interval: "1m",
	})

	got, _ := store.get(job.JobID)
	if proxy.runRequest() != nil {
		t.Fatal("structured preview failure reached RunStrategy")
	}
	if got.Status != downloadRunError || got.Code != "LEVERAGE_READ_FAILED" || len(got.Failures) != 1 {
		t.Fatalf("structured preview failure lost: %+v", got)
	}
	if failure := got.Failures[0]; failure.Symbol != "ETHUSDT" || failure.VenueID != 8 || !failure.Retryable || failure.Source != "core-service" {
		t.Fatalf("preview failure fields lost: %+v", failure)
	}
}

func TestDownloadAndRunJobContinuesWhenPreviewOnlyLacksHistoricalData(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		previewResp: &strategyv1.PreviewRunStrategyResponse{
			Profile: "backtest", Supported: true, Ok: false,
			Failures: []*strategyv1.PreflightFailureProto{{
				Kind: "historical_data", Reason: "no historical kline data in requested range",
				Code: "HISTORICAL_DATA_MISSING", Environment: 0,
			}},
			DeclaredInputs: []*strategyv1.LiveStreamBinding{{
				Exchange: "binance", Market: "spot", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m",
			}},
		},
		runResp: &strategyv1.RunStrategyResponse{Ok: true, SessionId: "spot-session"},
	}
	store := newDownloadRunJobStore()
	s := &server{marketData: &fakeMarketDataClient{coverageResp: &mdv1CoverageComplete}, downloadRunJobs: store}
	job := store.create()

	s.runDownloadAndRunJob(context.Background(), job.JobID, proxy, 6, 7, "rt-download", downloadAndRunRequest{
		RuntimeID: "rt-download", StartTimeMS: 1779033600000, EndTimeMS: 1779037200000, Interval: "1m",
	})

	got, _ := store.get(job.JobID)
	if proxy.runRequest() == nil {
		t.Fatal("historical-data-only preview failure did not reach RunStrategy after coverage validation")
	}
	if got.Status != downloadRunReady || got.SessionID != "spot-session" {
		t.Fatalf("download-and-run job = %+v, want ready Spot session", got)
	}
}

func TestDownloadAndRunJobPreservesStructuredRunFailure(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		previewResp: &strategyv1.PreviewRunStrategyResponse{
			Profile: "backtest", Supported: true, Ok: true,
			DeclaredInputs: []*strategyv1.LiveStreamBinding{{
				Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m",
			}},
		},
		runResp: &strategyv1.RunStrategyResponse{
			Ok: false, Code: "LEVERAGE_ROLLBACK_FAILED", RollbackFailed: true,
			Failures: []*strategyv1.PreflightFailureProto{{
				Kind: "leverage", Reason: "rollback readback mismatch", Code: "LEVERAGE_ROLLBACK_FAILED",
				Exchange: 1, Market: 2, Symbol: "ETHUSDT", VenueId: 8, Environment: 1, Retryable: true, Source: "core-service",
			}},
			TargetResults: []*strategyv1.StrategyLeverageTargetResult{{
				VenueId: 8, Exchange: 1, Market: 2, Symbol: "ETHUSDT", EffectiveLeverage: 10,
				LeverageSource: "order_target", PreviousLeverage: uint32Ptr(2), CurrentLeverage: uint32Ptr(10),
				ChangeRequired: true, Status: "rollback_failed", ErrorCode: "LEVERAGE_ROLLBACK_FAILED",
				ErrorMessage: "rollback readback mismatch", Retryable: true,
			}},
		},
	}
	store := newDownloadRunJobStore()
	s := &server{marketData: &fakeMarketDataClient{coverageResp: &mdv1CoverageComplete}, downloadRunJobs: store}
	job := store.create()

	s.runDownloadAndRunJob(context.Background(), job.JobID, proxy, 6, 7, "rt-download", downloadAndRunRequest{
		RuntimeID: "rt-download", StartTimeMS: 1779033600000, EndTimeMS: 1779037200000, Interval: "1m",
	})

	got, _ := store.get(job.JobID)
	if got.Status != downloadRunError || got.Code != "LEVERAGE_ROLLBACK_FAILED" || !got.RollbackFailed || got.SessionID != "" {
		t.Fatalf("structured run failure terminal state lost: %+v", got)
	}
	if len(got.Failures) != 1 || len(got.TargetResults) != 1 || got.TargetResults[0].Status != "rollback_failed" || got.TargetResults[0].ErrorCode != got.Code {
		t.Fatalf("structured run target results lost: %+v", got)
	}
}

func TestDownloadAndRunJobRejectsSuccessfulRunWithoutSessionID(t *testing.T) {
	proxy := &fakeControlPanelStrategyProxy{
		previewResp: &strategyv1.PreviewRunStrategyResponse{
			Profile: "backtest", Supported: true, Ok: true,
			DeclaredInputs: []*strategyv1.LiveStreamBinding{{
				Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m",
			}},
		},
		runResp: &strategyv1.RunStrategyResponse{Ok: true},
	}
	store := newDownloadRunJobStore()
	s := &server{marketData: &fakeMarketDataClient{coverageResp: &mdv1CoverageComplete}, downloadRunJobs: store}
	job := store.create()

	s.runDownloadAndRunJob(context.Background(), job.JobID, proxy, 6, 7, "rt-download", downloadAndRunRequest{
		RuntimeID: "rt-download", StartTimeMS: 1779033600000, EndTimeMS: 1779037200000, Interval: "1m",
	})

	got, _ := store.get(job.JobID)
	if got.Status != downloadRunError || got.Code != "STRATEGY_SESSION_ID_MISSING" || got.SessionID != "" {
		t.Fatalf("empty successful Session ID must be terminal error: %+v", got)
	}
}

func TestCreateMissingCoverageRequestsRequiresFundingForCompleteFuturesKlines(t *testing.T) {
	start := time.UnixMilli(1779033600000).UTC()
	mid := start.Add(30 * time.Minute)
	end := start.Add(time.Hour)
	fake := &fakeMarketDataClient{}
	fake.coverageFn = func(req *mdv1.QueryMarketDataCoverageRequest) (*mdv1.QueryMarketDataCoverageResponse, error) {
		if req.GetKey().GetKind() == "kline" {
			return &mdv1.QueryMarketDataCoverageResponse{Key: req.GetKey(), Complete: true}, nil
		}
		return &mdv1.QueryMarketDataCoverageResponse{
			Key: req.GetKey(), Complete: false,
			MissingSegments: []*mdv1.MarketDataTimeRange{
				{StartAt: timestamppb.New(start), EndAt: timestamppb.New(mid)},
				{StartAt: timestamppb.New(mid), EndAt: timestamppb.New(end)},
			},
		}, nil
	}
	fake.createFn = func(req *mdv1.CreateMarketDataRequestRequest) (*mdv1.CreateMarketDataRequestResponse, error) {
		id := int64(101)
		if req.GetRequestedStartAt().AsTime().Equal(mid) {
			id = 102
		}
		return &mdv1.CreateMarketDataRequestResponse{Request: &mdv1.MarketDataRequest{RequestId: id, Key: req.GetKey()}}, nil
	}
	s := &server{marketData: fake}
	declared := []*strategyv1.LiveStreamBinding{
		{Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
		{Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
	}
	ids, err := s.createMissingCoverageRequests(context.Background(), 6, 7, declared, downloadAndRunRequest{
		StartTimeMS: start.UnixMilli(), EndTimeMS: end.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("createMissingCoverageRequests: %v", err)
	}
	if len(ids) != 2 || len(fake.createCalls) != 2 {
		t.Fatalf("Funding request IDs/calls = %v/%d, want two missing ranges without duplicate-input copies", ids, len(fake.createCalls))
	}
	for i, call := range fake.createCalls {
		if call.GetUserId() != 6 || call.GetPortfolioId() != 7 || call.GetKey().GetKind() != "funding_rate" ||
			call.GetKey().GetMarket() != "futures" || call.GetKey().GetSymbol() != "BTCUSDT" || call.GetKey().GetInterval() != "" ||
			!call.GetRequestedStartAt().AsTime().Equal([]time.Time{start, mid}[i]) || !call.GetRequestedEndAt().AsTime().Equal([]time.Time{mid, end}[i]) {
			t.Fatalf("Funding missing-range request %d = %#v", i, call)
		}
	}
	if len(fake.coverageCalls) != 2 || fake.coverageCalls[0].GetKey().GetKind() != "kline" || fake.coverageCalls[1].GetKey().GetKind() != "funding_rate" {
		t.Fatalf("coverage requirements = %#v, want deduplicated Kline then Funding", fake.coverageCalls)
	}
	retryIDs, err := s.createMissingCoverageRequests(context.Background(), 6, 7, declared, downloadAndRunRequest{
		StartTimeMS: start.UnixMilli(), EndTimeMS: end.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("retry createMissingCoverageRequests: %v", err)
	}
	if len(retryIDs) != 2 || len(fake.createCalls) != 4 {
		t.Fatalf("idempotent Funding retry IDs/calls = %v/%d, want the same two request IDs across an exact retry", retryIDs, len(fake.createCalls))
	}
	for id := range ids {
		if _, ok := retryIDs[id]; !ok {
			t.Fatalf("Funding retry IDs = %v, want original ID %d", retryIDs, id)
		}
	}
}

func TestCreateMissingCoverageRequestsCoversEveryFuturesSymbolAndOmitsSpotFunding(t *testing.T) {
	start := time.UnixMilli(1779033600000).UTC()
	end := start.Add(time.Hour)
	fake := &fakeMarketDataClient{}
	fake.coverageFn = func(req *mdv1.QueryMarketDataCoverageRequest) (*mdv1.QueryMarketDataCoverageResponse, error) {
		if req.GetKey().GetKind() == "funding_rate" {
			return &mdv1.QueryMarketDataCoverageResponse{Key: req.GetKey(), MissingSegments: []*mdv1.MarketDataTimeRange{{StartAt: timestamppb.New(start), EndAt: timestamppb.New(end)}}}, nil
		}
		return &mdv1.QueryMarketDataCoverageResponse{Key: req.GetKey(), Complete: true}, nil
	}
	fake.createFn = func(req *mdv1.CreateMarketDataRequestRequest) (*mdv1.CreateMarketDataRequestResponse, error) {
		id := int64(201)
		if req.GetKey().GetSymbol() == "ETHUSDT" {
			id = 202
		}
		return &mdv1.CreateMarketDataRequestResponse{Request: &mdv1.MarketDataRequest{RequestId: id, Key: req.GetKey()}}, nil
	}
	declared := []*strategyv1.LiveStreamBinding{
		{Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
		{Exchange: "okx", Market: "perpetual_futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m"},
		{Exchange: "binance", Market: "spot", Kind: "kline", Symbol: "SOLUSDT", Interval: "1m"},
	}
	ids, err := (&server{marketData: fake}).createMissingCoverageRequests(context.Background(), 6, 7, declared, downloadAndRunRequest{StartTimeMS: start.UnixMilli(), EndTimeMS: end.UnixMilli()})
	if err != nil {
		t.Fatalf("createMissingCoverageRequests: %v", err)
	}
	if len(ids) != 2 || len(fake.createCalls) != 2 {
		t.Fatalf("multi-symbol Funding IDs/calls = %v/%d, want BTC and ETH", ids, len(fake.createCalls))
	}
	for _, call := range fake.createCalls {
		if call.GetKey().GetKind() != "funding_rate" || call.GetKey().GetMarket() != "futures" || call.GetKey().GetSymbol() == "SOLUSDT" {
			t.Fatalf("unexpected Funding request = %#v", call)
		}
	}
}

func TestValidateDeclaredCoverageWaitsForFullWindowFundingCoverage(t *testing.T) {
	start := time.UnixMilli(1779033600000).UTC()
	end := start.Add(time.Hour)
	fundingComplete := false
	fake := &fakeMarketDataClient{
		validateFn: func(req *mdv1.ValidateMarketDataCoverageRequest) (*mdv1.ValidateMarketDataCoverageResponse, error) {
			return &mdv1.ValidateMarketDataCoverageResponse{Key: req.GetKey(), Ok: true}, nil
		},
		coverageFn: func(req *mdv1.QueryMarketDataCoverageRequest) (*mdv1.QueryMarketDataCoverageResponse, error) {
			return &mdv1.QueryMarketDataCoverageResponse{Key: req.GetKey(), Complete: fundingComplete}, nil
		},
	}
	declared := []*strategyv1.LiveStreamBinding{{Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"}}
	s := &server{marketData: fake}
	ok, err := s.validateDeclaredCoverage(context.Background(), declared, downloadAndRunRequest{StartTimeMS: start.UnixMilli(), EndTimeMS: end.UnixMilli()})
	if err != nil || ok {
		t.Fatalf("validation with missing Funding = %v/%v, want false/nil", ok, err)
	}
	if len(fake.coverageCalls) != 1 || fake.coverageCalls[0].GetKey().GetKind() != "funding_rate" || fake.coverageCalls[0].GetKey().GetInterval() != "" ||
		!fake.coverageCalls[0].GetStartAt().AsTime().Equal(start) || !fake.coverageCalls[0].GetEndAt().AsTime().Equal(end) {
		t.Fatalf("Funding validation query = %#v, want exact full window", fake.coverageCalls)
	}
	fundingComplete = true
	ok, err = s.validateDeclaredCoverage(context.Background(), declared, downloadAndRunRequest{StartTimeMS: start.UnixMilli(), EndTimeMS: end.UnixMilli()})
	if err != nil || !ok {
		t.Fatalf("validation with complete Kline/Funding = %v/%v, want true/nil", ok, err)
	}
}

func TestDownloadAndRunJobTracksFundingRequestError(t *testing.T) {
	start := time.UnixMilli(1779033600000).UTC()
	end := start.Add(time.Hour)
	fundingKey := &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "funding_rate", Symbol: "BTCUSDT"}
	fake := &fakeMarketDataClient{
		coverageFn: func(req *mdv1.QueryMarketDataCoverageRequest) (*mdv1.QueryMarketDataCoverageResponse, error) {
			if req.GetKey().GetKind() == "kline" {
				return &mdv1.QueryMarketDataCoverageResponse{Key: req.GetKey(), Complete: true}, nil
			}
			return &mdv1.QueryMarketDataCoverageResponse{Key: req.GetKey(), MissingSegments: []*mdv1.MarketDataTimeRange{{StartAt: timestamppb.New(start), EndAt: timestamppb.New(end)}}}, nil
		},
		createResp:   &mdv1.CreateMarketDataRequestResponse{Request: &mdv1.MarketDataRequest{RequestId: 88, Key: fundingKey}},
		validateResp: &mdv1.ValidateMarketDataCoverageResponse{Ok: true},
		listResp: &mdv1.ListMarketDataRequestsResponse{Entries: []*mdv1.MarketDataRequestWithStream{{Request: &mdv1.MarketDataRequest{
			RequestId: 88, UserId: 6, Scope: "historical", Status: "error", Key: fundingKey, LastError: "Funding storage failed",
		}}}},
	}
	store := newDownloadRunJobStore()
	job := store.create()
	proxy := &fakeControlPanelStrategyProxy{previewResp: &strategyv1.PreviewRunStrategyResponse{
		Profile: "backtest", Supported: true, Ok: true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{{Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"}},
	}}
	s := &server{marketData: fake, downloadRunJobs: store}
	s.runDownloadAndRunJob(context.Background(), job.JobID, proxy, 6, 7, "rt-download", downloadAndRunRequest{RuntimeID: "rt-download", StartTimeMS: start.UnixMilli(), EndTimeMS: end.UnixMilli(), Interval: "1m"})
	got, _ := store.get(job.JobID)
	if got.Status != downloadRunError || !strings.Contains(got.Error, "historical request 88 error: Funding storage failed") || len(got.Requests) != 1 || got.Requests[0].RequestID != 88 {
		t.Fatalf("Funding request error job = %+v", got)
	}
	if proxy.runRequest() != nil {
		t.Fatal("Backtest started before Funding coverage completed")
	}
}

func TestDownloadAndRunJobStatusSurfacesHistoricalRequestState(t *testing.T) {
	start := time.UnixMilli(1779033600000).UTC()
	end := time.UnixMilli(1779037200000).UTC()
	key := &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m"}
	fundingKey := &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "funding_rate", Symbol: "ETHUSDT"}
	fakeMarket := &fakeMarketDataClient{
		listResp: &mdv1.ListMarketDataRequestsResponse{
			Entries: []*mdv1.MarketDataRequestWithStream{
				{Request: &mdv1.MarketDataRequest{
					RequestId:        77,
					UserId:           6,
					Scope:            "historical",
					Status:           "running",
					Key:              key,
					RequestedStartAt: timestamppb.New(start),
					RequestedEndAt:   timestamppb.New(end),
					CreatedAt:        timestamppb.New(start),
					UpdatedAt:        timestamppb.New(start.Add(10 * time.Second)),
				}},
				{Request: &mdv1.MarketDataRequest{
					RequestId:        78,
					UserId:           6,
					Scope:            "historical",
					Status:           "running",
					Key:              fundingKey,
					RequestedStartAt: timestamppb.New(start),
					RequestedEndAt:   timestamppb.New(end),
					CreatedAt:        timestamppb.New(start),
					UpdatedAt:        timestamppb.New(start.Add(10 * time.Second)),
				}},
			},
		},
		listCalled: make(chan struct{}, 1),
		validateResponses: []*mdv1.ValidateMarketDataCoverageResponse{
			{Ok: false, Key: key, RequestedStartAt: timestamppb.New(start), RequestedEndAt: timestamppb.New(end)},
			{Ok: true, Key: key, RequestedStartAt: timestamppb.New(start), RequestedEndAt: timestamppb.New(end)},
		},
	}
	fundingCoverageCalls := 0
	fakeMarket.coverageFn = func(req *mdv1.QueryMarketDataCoverageRequest) (*mdv1.QueryMarketDataCoverageResponse, error) {
		if req.GetKey().GetKind() == "funding_rate" {
			fundingCoverageCalls++
			if fundingCoverageCalls > 1 {
				return &mdv1.QueryMarketDataCoverageResponse{Key: req.GetKey(), Complete: true}, nil
			}
		}
		return &mdv1.QueryMarketDataCoverageResponse{Key: req.GetKey(), MissingSegments: []*mdv1.MarketDataTimeRange{{StartAt: timestamppb.New(start), EndAt: timestamppb.New(end)}}}, nil
	}
	fakeMarket.createFn = func(req *mdv1.CreateMarketDataRequestRequest) (*mdv1.CreateMarketDataRequestResponse, error) {
		id := int64(77)
		if req.GetKey().GetKind() == "funding_rate" {
			id = 78
		}
		return &mdv1.CreateMarketDataRequestResponse{Request: &mdv1.MarketDataRequest{
			RequestId: id, UserId: 6, Scope: "historical", Status: "pending", Key: req.GetKey(),
			RequestedStartAt: req.GetRequestedStartAt(), RequestedEndAt: req.GetRequestedEndAt(),
			CreatedAt: timestamppb.New(start), UpdatedAt: timestamppb.New(start),
		}}, nil
	}
	proxy := &fakeControlPanelStrategyProxy{previewResp: &strategyv1.PreviewRunStrategyResponse{
		Profile:   "backtest",
		Supported: true,
		Ok:        true,
		DeclaredInputs: []*strategyv1.LiveStreamBinding{{
			Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m",
		}},
	}}
	s := &server{
		jwtSecret:   []byte("s"),
		corsOrigins: []string{"*"},
		marketData:  fakeMarket,
		controlPanel: &fakeResolver{
			resp:        controlpanel.Route{RuntimeID: "rt-download"},
			runtimeResp: controlpanel.Runtime{RuntimeID: "rt-download", Role: "executor"},
		},
		cpRuntime:       proxy,
		downloadRunJobs: newDownloadRunJobStore(),
	}

	body := bytes.NewBufferString(`{"runtime_id":"rt-download","start_time_ms":1779033600000,"end_time_ms":1779037200000,"interval":"1m","max_loss_close_pct":0.25}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/strategy/download-and-run", body), 6)
	rec := httptest.NewRecorder()

	s.handleDownloadAndRun(rec, req, 7)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created downloadRunJob
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fakeMarket.listCalled:
	case <-time.After(time.Second):
		t.Fatal("download job did not query historical request state")
	}
	requestStateDeadline := time.Now().Add(time.Second)
	for {
		job, ok := s.downloadRunJobs.get(created.JobID)
		if ok && len(job.Requests) == 2 {
			break
		}
		if time.Now().After(requestStateDeadline) {
			t.Fatal("download job did not publish historical request state")
		}
		time.Sleep(time.Millisecond)
	}

	statusReq := withUID(httptest.NewRequest(http.MethodGet, "/api/strategy/download-and-run-jobs/"+created.JobID, nil), 6)
	statusRec := httptest.NewRecorder()
	s.handleDownloadRunJobStatus(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status endpoint code=%d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var statusBody map[string]any
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody["message"] == "" {
		t.Fatalf("job status should include a live progress message, body=%s", statusRec.Body.String())
	}
	requests, ok := statusBody["requests"].([]any)
	if !ok || len(requests) != 2 {
		t.Fatalf("job status should expose historical request details, body=%s", statusRec.Body.String())
	}
	first, _ := requests[0].(map[string]any)
	if first["status"] != "running" || first["request_id"].(float64) != 77 {
		t.Fatalf("request status = %#v, want request_id=77 status=running", first)
	}
	second, _ := requests[1].(map[string]any)
	if second["status"] != "running" || second["request_id"].(float64) != 78 || second["key"].(map[string]any)["kind"] != "funding_rate" {
		t.Fatalf("Funding request status = %#v, want request_id=78 kind=funding_rate status=running", second)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if proxy.runRequest() != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("download job did not finish after coverage became valid")
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
