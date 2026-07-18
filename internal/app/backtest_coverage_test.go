package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
			if contains(string(encoded), "10000") || contains(string(encoded), "StringValue") {
				t.Fatalf("job leaks serialized transport error: %s", encoded)
			}
		})
	}
}

func TestDownloadAndRunJobStatusSurfacesHistoricalRequestState(t *testing.T) {
	start := time.UnixMilli(1779033600000).UTC()
	end := time.UnixMilli(1779037200000).UTC()
	key := &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m"}
	fakeMarket := &fakeMarketDataClient{
		createResp: &mdv1.CreateMarketDataRequestResponse{
			Request: &mdv1.MarketDataRequest{
				RequestId:        77,
				UserId:           6,
				Scope:            "historical",
				Status:           "pending",
				Key:              key,
				RequestedStartAt: timestamppb.New(start),
				RequestedEndAt:   timestamppb.New(end),
				CreatedAt:        timestamppb.New(start),
				UpdatedAt:        timestamppb.New(start),
			},
		},
		listResp: &mdv1.ListMarketDataRequestsResponse{
			Entries: []*mdv1.MarketDataRequestWithStream{{
				Request: &mdv1.MarketDataRequest{
					RequestId:        77,
					UserId:           6,
					Scope:            "historical",
					Status:           "running",
					Key:              key,
					RequestedStartAt: timestamppb.New(start),
					RequestedEndAt:   timestamppb.New(end),
					CreatedAt:        timestamppb.New(start),
					UpdatedAt:        timestamppb.New(start.Add(10 * time.Second)),
				},
			}},
		},
		listCalled: make(chan struct{}, 1),
		validateResponses: []*mdv1.ValidateMarketDataCoverageResponse{
			{Ok: false, Key: key, RequestedStartAt: timestamppb.New(start), RequestedEndAt: timestamppb.New(end)},
			{Ok: true, Key: key, RequestedStartAt: timestamppb.New(start), RequestedEndAt: timestamppb.New(end)},
		},
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
	if !ok || len(requests) != 1 {
		t.Fatalf("job status should expose historical request details, body=%s", statusRec.Body.String())
	}
	first, _ := requests[0].(map[string]any)
	if first["status"] != "running" || first["request_id"].(float64) != 77 {
		t.Fatalf("request status = %#v, want request_id=77 status=running", first)
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
