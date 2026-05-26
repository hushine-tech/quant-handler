package app

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	"github.com/parquet-go/parquet-go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestAccountDebugPackage_DownloadsZipWithParquet(t *testing.T) {
	start := int64(1735689600000)
	end := int64(1735689660000)
	fake := &fakeMarketDataClient{
		coverageResp: &mdv1.QueryMarketDataCoverageResponse{
			Key:              &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
			RequestedStartAt: timestamppb.New(timeFromMS(start)),
			RequestedEndAt:   timestamppb.New(timeFromMS(end)),
			Complete:         true,
			ExpectedCount:    1,
			CoveredCount:     1,
		},
		validateResp: &mdv1.ValidateMarketDataCoverageResponse{
			Key:              &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
			RequestedStartAt: timestamppb.New(timeFromMS(start)),
			RequestedEndAt:   timestamppb.New(timeFromMS(end)),
			Ok:               true,
			ExpectedCount:    1,
			ActualCount:      1,
		},
		klinesResp: &mdv1.QueryMarketDataKlinesResponse{
			Key:              &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
			RequestedStartAt: timestamppb.New(timeFromMS(start)),
			RequestedEndAt:   timestamppb.New(timeFromMS(end)),
			Rows: []*mdv1.MarketDataKline{{
				OpenTime:  timestamppb.New(timeFromMS(start)),
				CloseTime: timestamppb.New(timeFromMS(end)),
				Open:      100,
				High:      101,
				Low:       99,
				Close:     100.5,
				Volume:    12,
			}},
			RowCount: 1,
			Limit:    1000,
		},
	}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/accounts/7/debug-package", strings.NewReader(`{
		"market":"futures",
		"symbol":"BTCUSDT",
		"interval":"1m",
		"start_time_ms":1735689600000,
		"end_time_ms":1735689660000,
		"wallet_source":"manual",
		"initial_balance":1000
	}`)), 42)
	rr := httptest.NewRecorder()

	s.handleAccountsByID().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "debug-package") {
		t.Fatalf("content-disposition = %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[f.Name] = f
	}
	for _, name := range []string{"manifest.yaml", "wallet.yaml", "strategy.py.template", "README.md", "data.parquet"} {
		if entries[name] == nil {
			t.Fatalf("missing zip entry %s", name)
		}
	}
	if entries["data.parquet"].UncompressedSize64 == 0 {
		t.Fatalf("data.parquet is empty")
	}
	rows := readDebugPackageRows(t, entries["data.parquet"])
	if len(rows) != 1 {
		t.Fatalf("parquet rows = %d, want 1", len(rows))
	}
	if rows[0].TimestampMS != start || rows[0].Close != 100.5 {
		t.Fatalf("parquet row = %#v", rows[0])
	}
	if fake.lastValidateReq == nil {
		t.Fatal("coverage was not validated")
	}
	if fake.lastKlinesReq == nil {
		t.Fatal("kline rows were not queried")
	}
}

func TestAccountDebugPackage_RejectsIncompleteCoverage(t *testing.T) {
	fake := &fakeMarketDataClient{
		validateResp: &mdv1.ValidateMarketDataCoverageResponse{
			Key:           &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
			Ok:            false,
			ExpectedCount: 2,
			ActualCount:   1,
			Reason:        "missing bars",
		},
	}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/accounts/7/debug-package", strings.NewReader(`{
		"market":"futures",
		"symbol":"BTCUSDT",
		"interval":"1m",
		"start_time_ms":1735689600000,
		"end_time_ms":1735689660000
	}`)), 42)
	rr := httptest.NewRecorder()

	s.handleAccountDebugPackage(rr, req, 7)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestAccountDebugPackage_RejectsStaleValidationWhenRowsIncomplete(t *testing.T) {
	start := int64(1735689600000)
	fake := &fakeMarketDataClient{
		validateResp: &mdv1.ValidateMarketDataCoverageResponse{
			Key:           &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
			Ok:            true,
			ExpectedCount: 2,
			ActualCount:   2,
		},
		klinesResp: &mdv1.QueryMarketDataKlinesResponse{
			Key: &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
			Rows: []*mdv1.MarketDataKline{{
				OpenTime:  timestamppb.New(timeFromMS(start)),
				CloseTime: timestamppb.New(timeFromMS(start + 60_000)),
				Open:      100,
				High:      101,
				Low:       99,
				Close:     100,
				Volume:    10,
			}},
			RowCount: 1,
			Limit:    1000,
		},
	}
	s := newServerWithFakeMarketData(t, fake)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/accounts/7/debug-package", strings.NewReader(`{
		"market":"futures",
		"symbol":"BTCUSDT",
		"interval":"1m",
		"start_time_ms":1735689600000,
		"end_time_ms":1735689720000
	}`)), 42)
	rr := httptest.NewRecorder()

	s.handleAccountDebugPackage(rr, req, 7)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func readDebugPackageRows(t *testing.T, entry *zip.File) []debugPackageKlineRow {
	t.Helper()
	rc, err := entry.Open()
	if err != nil {
		t.Fatalf("open parquet entry: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read parquet entry: %v", err)
	}
	rows, err := parquet.Read[debugPackageKlineRow](bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read parquet rows: %v", err)
	}
	return rows
}

func timeFromMS(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}
