package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"github.com/parquet-go/parquet-go"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
)

type fakeDebugPackagePortfolioClient struct {
	portfoliov1.PortfolioServiceClient
	strategy        *portfoliov1.StrategyEntry
	capabilities    *portfoliov1.GetProductCapabilitiesResponse
	capabilityErr   error
	capabilityCalls int
	snapshot        *portfoliov1.GetPortfolioSnapshotResponse
	preflight       *portfoliov1.PreflightStrategySessionResponse
	preflightErr    error
	lastStrategyID  int64
	lastSnapshot    *portfoliov1.GetPortfolioSnapshotRequest
	lastPreflight   *portfoliov1.PreflightStrategySessionRequest
}

func (f *fakeDebugPackagePortfolioClient) GetPortfolio(_ context.Context, req *portfoliov1.GetPortfolioRequest, _ ...grpc.CallOption) (*portfoliov1.GetPortfolioResponse, error) {
	return &portfoliov1.GetPortfolioResponse{Portfolio: &portfoliov1.PortfolioRegistryEntry{PortfolioId: req.GetPortfolioId(), UserId: req.GetUserId()}}, nil
}

func (f *fakeDebugPackagePortfolioClient) GetStrategy(_ context.Context, req *portfoliov1.GetStrategyRequest, _ ...grpc.CallOption) (*portfoliov1.GetStrategyResponse, error) {
	f.lastStrategyID = req.GetStrategyId()
	return &portfoliov1.GetStrategyResponse{Strategy: f.strategy}, nil
}

func (f *fakeDebugPackagePortfolioClient) GetProductCapabilities(_ context.Context, _ *portfoliov1.GetProductCapabilitiesRequest, _ ...grpc.CallOption) (*portfoliov1.GetProductCapabilitiesResponse, error) {
	f.capabilityCalls++
	return f.capabilities, f.capabilityErr
}

func (f *fakeDebugPackagePortfolioClient) GetPortfolioSnapshot(_ context.Context, req *portfoliov1.GetPortfolioSnapshotRequest, _ ...grpc.CallOption) (*portfoliov1.GetPortfolioSnapshotResponse, error) {
	f.lastSnapshot = req
	return f.snapshot, nil
}

func (f *fakeDebugPackagePortfolioClient) PreflightStrategySession(_ context.Context, req *portfoliov1.PreflightStrategySessionRequest, _ ...grpc.CallOption) (*portfoliov1.PreflightStrategySessionResponse, error) {
	f.lastPreflight = req
	return f.preflight, f.preflightErr
}

type debugPackageManifestV2ForTest struct {
	SchemaVersion int `yaml:"schema_version"`
	Inputs        []struct {
		StreamID string `yaml:"stream_id"`
		Exchange string `yaml:"exchange"`
		Market   string `yaml:"market"`
		Kind     string `yaml:"kind"`
		Symbol   string `yaml:"symbol"`
		Interval string `yaml:"interval"`
	} `yaml:"inputs"`
	DataFiles []struct {
		StreamID string `yaml:"stream_id"`
		Path     string `yaml:"path"`
	} `yaml:"data_files"`
	Wallet struct {
		Assets []struct {
			Asset  string `yaml:"asset"`
			Free   string `yaml:"free"`
			Locked string `yaml:"locked"`
		} `yaml:"assets"`
	} `yaml:"wallet"`
	Integrity struct {
		Files []struct {
			Path   string `yaml:"path"`
			SHA256 string `yaml:"sha256"`
		} `yaml:"files"`
	} `yaml:"integrity"`
}

func TestDebugPackageV2UsesDeclarationsPreparedBySelectedRuntime(t *testing.T) {
	portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"stream_id": "stale-futures", "exchange": "binance", "market": "perpetual_futures", "kind": "kline", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
    def __init__(self):
        type(self).INPUTS = [{"stream_id": "runtime-spot", "exchange": "binance", "market": "spot", "kind": "kline", "symbol": "BTCUSDT", "interval": "1m"}]
`)
	proxy := &fakeControlPanelStrategyProxy{validateResp: &strategyv1.ValidateStrategySourceResponse{
		Ok: true,
		DeclaredInputs: []*strategyv1.StrategyInputDeclaration{{
			StreamId: "runtime-spot", Exchange: "binance", Market: "spot", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m",
		}},
	}}
	resolver := &fakeResolver{resp: controlpanel.Route{RuntimeID: "rt-debug"}}
	s := newServerWithFakeMarketData(t, &fakeMarketDataClient{})
	s.portfolios = portfolio
	s.controlPanel = resolver
	s.cpRuntime = proxy
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{
		"runtime_id":"rt-debug","strategy_id":42,
		"start_time_ms":1735689600000,
		"end_time_ms":1735689660000
	}`)), 9)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if resolver.resolveByIDCalls != 1 || resolver.gotRuntimeID != "rt-debug" {
		t.Fatalf("runtime resolution = calls:%d runtime:%q", resolver.resolveByIDCalls, resolver.gotRuntimeID)
	}
	if resolver.gotEnvironment != 0 || resolver.gotRole != "executor" {
		t.Fatalf("runtime policy = role:%q environment:%d", resolver.gotRole, resolver.gotEnvironment)
	}
	if proxy.validateReq == nil || !proxy.validateReq.GetIncludeDeclarations() {
		t.Fatalf("runtime declaration request = %#v", proxy.validateReq)
	}
	if proxy.validateReq.GetSource() != portfolio.strategy.GetCode() || proxy.validateReq.GetUserId() != 9 || proxy.validateReq.GetRuntimeId() != "rt-debug" {
		t.Fatalf("runtime declaration request facts = %#v", proxy.validateReq)
	}
	manifest := readDebugPackageManifestV2(t, openDebugPackageZip(t, rr.Body.Bytes()))
	if len(manifest.Inputs) != 1 || manifest.Inputs[0].StreamID != "runtime-spot" || manifest.Inputs[0].Market != "spot" {
		t.Fatalf("manifest inputs = %#v", manifest.Inputs)
	}
}

func TestDebugPackageV2UsesStrategyRoutesAndCanonicalSpotFacts(t *testing.T) {
	start := int64(1735689600000)
	end := start + 60_000
	portfolio := newDebugPackagePortfolioFake(`
from hushine_strategy import Exchange, Market

class MyStrategy:
    INPUTS = [
        {"stream_id": "spot-btc-1m", "exchange": Exchange.BINANCE, "market": Market.SPOT, "kind": "kline", "symbol": "BTCUSDT", "interval": "1m"},
        {"stream_id": "futures-eth-1m", "exchange": Exchange.BINANCE, "market": Market.PERPETUAL_FUTURES, "kind": "kline", "symbol": "ETHUSDT", "interval": "1m"},
    ]
    ORDER_TARGETS = [
        {"exchange": Exchange.BINANCE, "market": Market.SPOT, "symbol": "BTCUSDT"},
    ]
    def on_market_data(self, data, wallet):
        return None
`)
	portfolio.snapshot.Snapshot.Wallet.Futures = &portfoliov1.FuturesWallet{
		InitialBalance: 500, WalletBalance: 500, AvailableBalance: 500,
	}
	portfolio.preflight.ResolvedVenues = []*portfoliov1.VenueEntry{{
		VenueId: 17, ApiKey: "demo-api-key", DisplayName: "demo-api-secret",
		Description: "postgres://internal kafka:9092",
	}}
	market := &fakeMarketDataClient{}
	s := newDebugPackageTestServer(t, market, portfolio)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{
		"runtime_id":"rt-debug","strategy_id":42,
		"start_time_ms":1735689600000,
		"end_time_ms":1735689660000,
		"symbol":"CALLER_ROUTE_MUST_BE_IGNORED"
	}`)), 9)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if portfolio.lastStrategyID != 42 {
		t.Fatalf("GetStrategy id = %d, want 42", portfolio.lastStrategyID)
	}
	zr := openDebugPackageZip(t, rr.Body.Bytes())
	manifest := readDebugPackageManifestV2(t, zr)
	if manifest.SchemaVersion != 2 || len(manifest.Inputs) != 2 || len(manifest.DataFiles) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
	paths := debugPackageZipPaths(zr)
	for _, want := range []string{
		"data/streams/spot-btc-1m/binance/spot/kline/BTCUSDT/1m.parquet",
		"data/streams/futures-eth-1m/binance/perpetual_futures/kline/ETHUSDT/1m.parquet",
	} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("missing %s; paths=%v", want, sortedDebugPackagePaths(paths))
		}
	}
	if portfolio.lastSnapshot == nil || portfolio.lastSnapshot.GetPortfolioId() != 7 || len(portfolio.lastSnapshot.GetRequiredSymbols()) != 2 {
		t.Fatalf("canonical wallet snapshot request = %#v", portfolio.lastSnapshot)
	}
	if portfolio.lastPreflight == nil || !portfolio.lastPreflight.GetDebugPackage() || portfolio.lastPreflight.GetSessionId() != "" || len(portfolio.lastPreflight.GetRequiredSymbols()) != 1 {
		t.Fatalf("side-effect-free Spot facts preflight = %#v", portfolio.lastPreflight)
	}
	if len(manifest.Wallet.Assets) != 2 || manifest.Wallet.Assets[0].Asset != "BTC" || manifest.Wallet.Assets[1].Asset != "USDT" {
		t.Fatalf("wallet = %#v", manifest.Wallet)
	}
	if len(manifest.Integrity.Files) == 0 {
		t.Fatal("integrity files are missing")
	}
	manifestText := readZipText(t, debugPackageZipPaths(zr)["manifest.yaml"])
	for _, fact := range []string{"EXCHANGE_MAX_NUM_ORDERS", "MAX_NUM_ORDERS", "MAX_ASSET"} {
		if !strings.Contains(manifestText, fact) {
			t.Fatalf("manifest is missing preflight fact %q", fact)
		}
	}
	for _, forbidden := range []string{"demo-api-key", "demo-api-secret", "postgres://", "kafka:9092"} {
		for _, entry := range zr.File {
			if strings.Contains(readZipText(t, entry), forbidden) {
				t.Fatalf("decompressed archive entry %q leaks platform value %q", entry.Name, forbidden)
			}
		}
	}
	_ = start
	_ = end
}

func TestDebugPackageV2SpotFactsPreflightAllowsOnlyActiveSessionAdmissionIssue(t *testing.T) {
	code := `
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`
	t.Run("active session does not block offline export", func(t *testing.T) {
		portfolio := newDebugPackagePortfolioFake(code)
		portfolio.preflight.Ok = false
		portfolio.preflight.Issues = []*portfoliov1.PreflightIssue{{Code: "ACTIVE_SESSION_EXISTS", Message: "portfolio already has an active session"}}
		market := &fakeMarketDataClient{}
		s := newDebugPackageTestServer(t, market, portfolio)
		req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 9)
		rr := httptest.NewRecorder()

		s.handlePortfolioDebugPackage(rr, req, 7)

		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
		}
	})
	t.Run("metadata issue fails before data loading", func(t *testing.T) {
		portfolio := newDebugPackagePortfolioFake(code)
		portfolio.preflight.Ok = false
		portfolio.preflight.Issues = []*portfoliov1.PreflightIssue{{Code: "SPOT_METADATA_UNAVAILABLE", Message: "rules unavailable"}}
		market := &fakeMarketDataClient{}
		s := newDebugPackageTestServer(t, market, portfolio)
		req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 9)
		rr := httptest.NewRecorder()

		s.handlePortfolioDebugPackage(rr, req, 7)

		if rr.Code != http.StatusFailedDependency {
			t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
		}
		if market.lastValidateReq != nil {
			t.Fatal("market data loaded after Spot facts preflight failed")
		}
		if portfolio.lastSnapshot != nil {
			t.Fatalf("wallet snapshot loaded before Spot facts preflight guard: %#v", portfolio.lastSnapshot)
		}
	})
}

func TestDebugPackageV2SpotFactsPreflightRejectsFalseWithoutIssues(t *testing.T) {
	portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
	portfolio.preflight.Ok = false
	portfolio.preflight.Issues = nil
	market := &fakeMarketDataClient{}
	s := newDebugPackageTestServer(t, market, portfolio)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 9)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusFailedDependency {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if market.lastValidateReq != nil {
		t.Fatal("market data loaded after non-ok facts preflight")
	}
	if portfolio.lastSnapshot != nil {
		t.Fatalf("wallet snapshot loaded before non-ok facts preflight guard: %#v", portfolio.lastSnapshot)
	}
}

func TestDebugPackageV2RejectsSpotFactsThatImporterWouldReject(t *testing.T) {
	portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
	portfolio.preflight.SpotRiskSnapshots[0].Metadata.Symbol = "ETHUSDT"
	market := &fakeMarketDataClient{}
	s := newDebugPackageTestServer(t, market, portfolio)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 9)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusFailedDependency || !strings.Contains(rr.Body.String(), "incomplete") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if market.lastValidateReq != nil {
		t.Fatal("market data loaded for debugger-incompatible Spot facts")
	}
}

func TestDebugPackageV2RejectsProducerFactsAndWalletsThatImporterWouldReject(t *testing.T) {
	tests := []struct {
		name      string
		portfolio func() *fakeDebugPackagePortfolioClient
		userID    int64
		want      string
	}{
		{
			name: "metadata base and quote do not compose symbol",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				portfolio.preflight.SpotRiskSnapshots[0].Metadata.BaseAsset = "ETH"
				return portfolio
			},
			userID: 9,
			want:   "compose",
		},
		{
			name: "negative metadata snapshot time",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				portfolio.preflight.SpotRiskSnapshots[0].Metadata.SnapshotTimeMs = -1
				return portfolio
			},
			userID: 9,
			want:   "snapshot time",
		},
		{
			name: "blank exchange filter type",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				portfolio.preflight.SpotRiskSnapshots[0].ExchangeFilters[0].FilterType = " "
				return portfolio
			},
			userID: 9,
			want:   "filter type",
		},
		{
			name: "negative exact Spot balance",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				portfolio.snapshot.Snapshot.Wallet.Spot.Assets[0].FreeDecimal = "-1"
				return portfolio
			},
			userID: 9,
			want:   "non-negative exact decimal",
		},
		{
			name: "non-finite fallback Spot balance",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				asset := portfolio.snapshot.Snapshot.Wallet.Spot.Assets[0]
				asset.FreeDecimal = ""
				asset.Free = math.NaN()
				return portfolio
			},
			userID: 9,
			want:   "non-negative exact decimal",
		},
		{
			name: "unsafe Spot asset code",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				portfolio.snapshot.Snapshot.Wallet.Spot.Assets[0].Asset = "../USDT"
				return portfolio
			},
			userID: 9,
			want:   "asset code",
		},
		{
			name: "oversized Spot asset code",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				portfolio.snapshot.Snapshot.Wallet.Spot.Assets[0].Asset = strings.Repeat("A", 129)
				return portfolio
			},
			userID: 9,
			want:   "exceeds 128 ASCII bytes",
		},
		{
			name: "trading symbol stored as Spot asset",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				portfolio.snapshot.Snapshot.Wallet.Spot.Assets[0].Asset = "BTCUSDT"
				return portfolio
			},
			userID: 9,
			want:   "trading symbol",
		},
		{
			name: "negative Futures balance",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newFuturesDebugPackagePortfolioFake()
				portfolio.snapshot.Snapshot.Wallet.Futures.AvailableBalance = -1
				return portfolio
			},
			userID: 42,
			want:   "non-negative exact decimal",
		},
		{
			name: "non-finite Futures balance",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newFuturesDebugPackagePortfolioFake()
				portfolio.snapshot.Snapshot.Wallet.Futures.WalletBalance = math.Inf(1)
				return portfolio
			},
			userID: 42,
			want:   "non-negative exact decimal",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			portfolio := tc.portfolio()
			market := &fakeMarketDataClient{}
			s := newDebugPackageTestServer(t, market, portfolio)
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), tc.userID)
			rr := httptest.NewRecorder()

			s.handlePortfolioDebugPackage(rr, req, 7)

			if rr.Code != http.StatusFailedDependency || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("status=%d body=%s, want %q", rr.Code, rr.Body.String(), tc.want)
			}
			if rr.Header().Get("Content-Type") == "application/zip" {
				t.Fatal("invalid producer facts returned a ZIP")
			}
		})
	}
}

func TestDebugPackageV2SpotCapabilityFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*fakeDebugPackagePortfolioClient)
		wantStatus int
	}{
		{
			name: "missing capability",
			configure: func(fake *fakeDebugPackagePortfolioClient) {
				fake.capabilities = &portfoliov1.GetProductCapabilitiesResponse{}
			},
			wantStatus: http.StatusPreconditionFailed,
		},
		{
			name: "disabled capability",
			configure: func(fake *fakeDebugPackagePortfolioClient) {
				fake.capabilities = &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{Name: "offline_spot_usdt", Configured: true, Effective: false, Reason: "disabled for test"}}}
			},
			wantStatus: http.StatusPreconditionFailed,
		},
		{
			name: "discovery unavailable",
			configure: func(fake *fakeDebugPackagePortfolioClient) {
				fake.capabilityErr = errors.New("rpc unavailable")
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
			tc.configure(portfolio)
			market := &fakeMarketDataClient{}
			s := newDebugPackageTestServer(t, market, portfolio)
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 9)
			rr := httptest.NewRecorder()

			s.handlePortfolioDebugPackage(rr, req, 7)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if market.lastValidateReq != nil || portfolio.lastSnapshot != nil {
				t.Fatal("disabled/unavailable Spot export must fail before data or wallet loading")
			}
		})
	}
}

func TestDebugPackageV2PreservesDistinctStreamIDsForTheSameKlineRoute(t *testing.T) {
	portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [
        {"stream_id": "spot-a", "exchange": "binance", "market": "spot", "kind": "kline", "symbol": "BTCUSDT", "interval": "1m"},
        {"stream_id": "spot-b", "exchange": "binance", "market": "spot", "kind": "kline", "symbol": "BTCUSDT", "interval": "1m"},
    ]
    ORDER_TARGETS = []
`)
	market := &fakeMarketDataClient{}
	s := newDebugPackageTestServer(t, market, portfolio)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 9)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	manifest := readDebugPackageManifestV2(t, openDebugPackageZip(t, rr.Body.Bytes()))
	if len(manifest.Inputs) != 2 || len(manifest.DataFiles) != 2 {
		t.Fatalf("distinct stream identities collapsed: %#v", manifest)
	}
	seen := make(map[string]struct{})
	for _, file := range manifest.DataFiles {
		seen[file.Path] = struct{}{}
	}
	if len(seen) != 2 {
		t.Fatalf("distinct files collapsed: %#v", manifest.DataFiles)
	}
}

func TestDebugPackageV2RejectsDuplicateDeclarationsReturnedByRuntime(t *testing.T) {
	inputs := []debugPackageInput{
		{StreamID: "dup", Exchange: "binance", Market: "spot", Symbol: "BTCUSDT", Interval: "1m"},
		{StreamID: "dup", Exchange: "binance", Market: "spot", Symbol: "ETHUSDT", Interval: "1m"},
	}

	_, _, err := normalizeDebugPackageDeclarations(inputs, nil)

	if err == nil || !strings.Contains(err.Error(), "duplicate stream_id") {
		t.Fatalf("error = %v, want duplicate stream_id", err)
	}
}

func TestDebugPackageV2RejectsRuntimeDeclarationFailureBeforeDataLoading(t *testing.T) {
	portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
	market := &fakeMarketDataClient{}
	s := newServerWithFakeMarketData(t, market)
	s.portfolios = portfolio
	s.controlPanel = &fakeResolver{resp: controlpanel.Route{RuntimeID: "rt-debug"}}
	s.cpRuntime = &fakeControlPanelStrategyProxy{validateResp: &strategyv1.ValidateStrategySourceResponse{
		Ok: false,
		Issues: []*strategyv1.StrategyValidationIssueProto{{
			Code: "STRATEGY_DECLARATION_INVALID", Message: "strategy declarations could not be resolved",
		}},
	}}
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 9)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "STRATEGY_DECLARATION_INVALID") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if market.lastValidateReq != nil || portfolio.lastSnapshot != nil || portfolio.lastPreflight != nil {
		t.Fatal("runtime declaration failure must precede facts, wallet, and market-data loading")
	}
}

func TestDebugPackageV2RejectsInconsistentRuntimeDeclarationResponse(t *testing.T) {
	tests := []struct {
		name     string
		response *strategyv1.ValidateStrategySourceResponse
	}{
		{
			name: "ok with validation issues",
			response: &strategyv1.ValidateStrategySourceResponse{
				Ok: true,
				Issues: []*strategyv1.StrategyValidationIssueProto{{
					Code: "PARTIAL_VALIDATION", Message: "declarations are not authoritative",
				}},
				DeclaredInputs: []*strategyv1.StrategyInputDeclaration{{
					Exchange: "binance", Market: "spot", Symbol: "BTCUSDT", Interval: "1m",
				}},
			},
		},
		{name: "ok without inputs", response: &strategyv1.ValidateStrategySourceResponse{Ok: true}},
		{
			name: "nil input",
			response: &strategyv1.ValidateStrategySourceResponse{
				Ok: true, DeclaredInputs: []*strategyv1.StrategyInputDeclaration{nil},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
			market := &fakeMarketDataClient{}
			s := newServerWithFakeMarketData(t, market)
			s.portfolios = portfolio
			s.controlPanel = &fakeResolver{resp: controlpanel.Route{RuntimeID: "rt-debug"}}
			s.cpRuntime = &fakeControlPanelStrategyProxy{validateResp: tc.response}
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 9)
			rr := httptest.NewRecorder()

			s.handlePortfolioDebugPackage(rr, req, 7)

			if rr.Code != http.StatusBadGateway {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if market.lastValidateReq != nil || portfolio.lastSnapshot != nil || portfolio.lastPreflight != nil {
				t.Fatal("malformed runtime response must fail before downstream loading")
			}
		})
	}
}

func TestDebugPackageV2RequiresTopLevelCanonicalPortfolioWallet(t *testing.T) {
	portfolio := newFuturesDebugPackagePortfolioFake()
	portfolio.snapshot.Snapshot.Wallet = nil
	portfolio.snapshot.Snapshot.Venues = []*portfoliov1.VenueSnapshot{{
		VenueId: 99,
		Wallet: &portfoliov1.PortfolioWalletState{Futures: &portfoliov1.FuturesWallet{
			InitialBalance: 1000, WalletBalance: 1000, AvailableBalance: 1000,
		}},
	}}
	market := &fakeMarketDataClient{}
	s := newDebugPackageTestServer(t, market, portfolio)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 42)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusFailedDependency || !strings.Contains(rr.Body.String(), "canonical portfolio wallet snapshot is missing") {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDebugPackageV2RequiresWalletComponentForEveryDeclaredMarket(t *testing.T) {
	tests := []struct {
		name      string
		portfolio *fakeDebugPackagePortfolioClient
		userID    int64
		want      string
	}{
		{
			name: "Futures route without Futures wallet",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newFuturesDebugPackagePortfolioFake()
				portfolio.snapshot.Snapshot.Wallet = &portfoliov1.PortfolioWalletState{Spot: &portfoliov1.SpotWallet{Assets: []*portfoliov1.SpotAsset{{Asset: "USDT", FreeDecimal: "1000.00000000"}}}}
				return portfolio
			}(),
			userID: 42,
			want:   "Futures wallet",
		},
		{
			name: "Spot route without Spot wallet",
			portfolio: func() *fakeDebugPackagePortfolioClient {
				portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"exchange": "binance", "market": "spot", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
				portfolio.snapshot.Snapshot.Wallet = &portfoliov1.PortfolioWalletState{Futures: &portfoliov1.FuturesWallet{InitialBalance: 1000, WalletBalance: 1000, AvailableBalance: 1000}}
				return portfolio
			}(),
			userID: 9,
			want:   "Spot wallet",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			market := &fakeMarketDataClient{}
			s := newDebugPackageTestServer(t, market, tc.portfolio)
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), tc.userID)
			rr := httptest.NewRecorder()

			s.handlePortfolioDebugPackage(rr, req, 7)

			if rr.Code != http.StatusFailedDependency || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestDebugPackageV2RejectsUnboundedStreamAndBarRequests(t *testing.T) {
	base := debugPackageInput{Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"}
	tooManyStreams := make([]debugPackageInput, debugPackageMaxStreams+1)
	for index := range tooManyStreams {
		tooManyStreams[index] = base
		tooManyStreams[index].StreamID = fmt.Sprintf("stream-%d", index)
	}
	if err := validateDebugPackageExportBounds(tooManyStreams, nil, 1, 60_001); err == nil || !strings.Contains(err.Error(), "too many streams") {
		t.Fatalf("stream bound error=%v", err)
	}
	start := int64(time.Minute / time.Millisecond)
	end := start + (debugPackageMaxTotalBars+1)*int64(time.Minute/time.Millisecond)
	base.StreamID = "one-stream"
	if err := validateDebugPackageExportBounds([]debugPackageInput{base}, nil, start, end); err == nil || !strings.Contains(err.Error(), "too many bars") {
		t.Fatalf("bar bound error=%v", err)
	}
}

func TestDebugPackageV2RejectsUnboundedDeclarationsBeforeDownstreamRPCs(t *testing.T) {
	makeInput := func(index int) *strategyv1.StrategyInputDeclaration {
		return &strategyv1.StrategyInputDeclaration{
			StreamId: fmt.Sprintf("stream-%03d", index),
			Exchange: "binance", Market: "spot", Kind: "kline",
			Symbol: fmt.Sprintf("I%03dUSDT", index), Interval: "1m",
		}
	}
	makeTarget := func(index int) *strategyv1.StrategyOrderTargetBinding {
		return &strategyv1.StrategyOrderTargetBinding{
			Exchange: "binance", Market: "spot", Symbol: fmt.Sprintf("T%03dUSDT", index),
		}
	}
	tests := []struct {
		name     string
		inputs   int
		targets  int
		wantText string
	}{
		{name: "order targets", inputs: 1, targets: 129, wantText: "too many order targets"},
		{name: "required symbols", inputs: 65, targets: 64, wantText: "too many required symbols"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			portfolio := newDebugPackagePortfolioFake(`
class MyStrategy:
    INPUTS = [{"stream_id": "placeholder", "exchange": "binance", "market": "spot", "kind": "kline", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = []
`)
			response := &strategyv1.ValidateStrategySourceResponse{Ok: true}
			for index := 0; index < tc.inputs; index++ {
				response.DeclaredInputs = append(response.DeclaredInputs, makeInput(index))
			}
			for index := 0; index < tc.targets; index++ {
				response.DeclaredOrderTargets = append(response.DeclaredOrderTargets, makeTarget(index))
			}
			market := &fakeMarketDataClient{}
			s := newServerWithFakeMarketData(t, market)
			s.portfolios = portfolio
			s.controlPanel = &fakeResolver{resp: controlpanel.Route{RuntimeID: "rt-debug"}}
			s.cpRuntime = &fakeControlPanelStrategyProxy{validateResp: response}
			req := withUID(httptest.NewRequest(
				http.MethodPost,
				"/api/portfolios/7/debug-package",
				strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`),
			), 9)
			rr := httptest.NewRecorder()

			s.handlePortfolioDebugPackage(rr, req, 7)

			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.wantText) {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			if portfolio.capabilityCalls != 0 || portfolio.lastPreflight != nil || portfolio.lastSnapshot != nil {
				t.Fatalf("downstream portfolio RPCs ran: capability=%d preflight=%v snapshot=%v", portfolio.capabilityCalls, portfolio.lastPreflight, portfolio.lastSnapshot)
			}
			if market.lastValidateReq != nil || market.lastKlinesReq != nil {
				t.Fatalf("market-data RPCs ran: validate=%v klines=%v", market.lastValidateReq, market.lastKlinesReq)
			}
		})
	}
}

func TestDebugPackageV2EnforcesPortablePathComponentLength(t *testing.T) {
	input := debugPackageInput{
		StreamID: strings.Repeat("a", 129), Exchange: "binance",
		Market: "spot", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m",
	}
	if _, _, err := normalizeDebugPackageDeclarations([]debugPackageInput{input}, nil); err == nil || !strings.Contains(err.Error(), "stream_id exceeds 128 ASCII bytes") {
		t.Fatalf("long stream_id error=%v", err)
	}
	input.StreamID = strings.Repeat("a", 128)
	if _, _, err := normalizeDebugPackageDeclarations([]debugPackageInput{input}, nil); err != nil {
		t.Fatalf("128-byte stream_id should be portable: %v", err)
	}
}

func TestDebugPackageRejectsMismatchedMarketDataResponseKey(t *testing.T) {
	start := int64(1735689600000)
	end := start + 60_000
	fake := &fakeMarketDataClient{klinesResp: &mdv1.QueryMarketDataKlinesResponse{
		Key: &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m"},
		Rows: []*mdv1.MarketDataKline{{
			OpenTime: timestamppb.New(timeFromMS(start)), CloseTime: timestamppb.New(timeFromMS(end)),
			Open: 100, High: 101, Low: 99, Close: 100, Volume: 10,
		}},
	}}
	s := newServerWithFakeMarketData(t, fake)
	key := &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"}
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	if _, err := s.fetchDebugPackageKlines(req, key, start, end); err == nil || !strings.Contains(err.Error(), "response key") {
		t.Fatalf("error=%v, want mismatched response key", err)
	}
}

func TestDebugPackageIgnoresUntrustedCoverageCountForAllocation(t *testing.T) {
	start := int64(1735689600000)
	end := start + 60_000
	key := &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"}
	for _, expectedCount := range []int64{-1, math.MaxInt64} {
		t.Run(fmt.Sprintf("expected_%d", expectedCount), func(t *testing.T) {
			fake := &fakeMarketDataClient{validateResp: &mdv1.ValidateMarketDataCoverageResponse{
				Key: key, Ok: true, ExpectedCount: expectedCount,
			}}
			s := newServerWithFakeMarketData(t, fake)
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("untrusted expected_count caused panic: %v", recovered)
				}
			}()

			rows, err := s.fetchDebugPackageKlines(req, key, start, end)

			if err != nil || len(rows) != 1 {
				t.Fatalf("rows=%d err=%v", len(rows), err)
			}
		})
	}
}

func TestDebugPackageRejectsInvalidKlineNumerics(t *testing.T) {
	start := int64(1735689600000)
	valid := func() *mdv1.MarketDataKline {
		return &mdv1.MarketDataKline{
			OpenTime: timestamppb.New(timeFromMS(start)),
			Open:     100, High: 101, Low: 99, Close: 100.5, Volume: 12,
		}
	}
	tests := []struct {
		name   string
		mutate func(*mdv1.MarketDataKline)
	}{
		{name: "NaN open", mutate: func(row *mdv1.MarketDataKline) { row.Open = math.NaN() }},
		{name: "infinite high", mutate: func(row *mdv1.MarketDataKline) { row.High = math.Inf(1) }},
		{name: "zero low", mutate: func(row *mdv1.MarketDataKline) { row.Low = 0 }},
		{name: "negative close", mutate: func(row *mdv1.MarketDataKline) { row.Close = -1 }},
		{name: "negative volume", mutate: func(row *mdv1.MarketDataKline) { row.Volume = -1 }},
		{name: "NaN volume", mutate: func(row *mdv1.MarketDataKline) { row.Volume = math.NaN() }},
		{name: "high below open", mutate: func(row *mdv1.MarketDataKline) { row.High = 99.5 }},
		{name: "low above close", mutate: func(row *mdv1.MarketDataKline) { row.Low = 100.75 }},
		{name: "high below low", mutate: func(row *mdv1.MarketDataKline) { row.High, row.Low = 98, 99 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := valid()
			tc.mutate(row)

			if err := validateDebugPackageKlineRows([]*mdv1.MarketDataKline{row}, start, start+60_000, 60_000); err == nil {
				t.Fatal("invalid kline was accepted")
			}
		})
	}
}

func TestDebugPackageIntervalRejectsOverflow(t *testing.T) {
	if _, err := debugPackageIntervalMS("9223372036854775807m"); err == nil {
		t.Fatal("overflowing interval must fail before coverage iteration")
	}
}

func TestDebugPackageIntervalsMatchRawCoverageContract(t *testing.T) {
	for _, interval := range []string{"1s", "5s", "10s", "30s", "1m", "3m", "5m", "15m", "30m", "1h", "2h", "4h", "6h", "8h", "12h", "1d", "3d"} {
		if _, err := debugPackageIntervalMS(interval); err != nil {
			t.Errorf("supported interval %q rejected: %v", interval, err)
		}
	}
	for _, interval := range []string{"7m", "1w", "1M", "9223372036854775807m"} {
		if _, err := debugPackageIntervalMS(interval); err == nil {
			t.Errorf("unsupported interval %q accepted", interval)
		}
	}
}

func TestDebugPackageRangeMustAlignToEveryDeclaredInterval(t *testing.T) {
	input := debugPackageInput{StreamID: "btc-1m", Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"}
	if err := validateDebugPackageExportBounds([]debugPackageInput{input}, nil, 60_001, 120_001); err == nil || !strings.Contains(err.Error(), "align") {
		t.Fatalf("unaligned range error=%v", err)
	}
	if err := validateDebugPackageExportBounds([]debugPackageInput{input}, nil, 60_000, 120_000); err != nil {
		t.Fatalf("aligned range rejected: %v", err)
	}
}

func TestDebugPackageSymbolsMatchMarketDataContract(t *testing.T) {
	for _, symbol := range []string{"B", "BTC-USDT", strings.Repeat("A", 31)} {
		input := debugPackageInput{StreamID: "stream", Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: symbol, Interval: "1m"}
		if err := validateDebugPackageInput(input); err == nil || !strings.Contains(err.Error(), "symbol") {
			t.Errorf("input symbol %q error=%v", symbol, err)
		}
		target := debugPackageOrderTarget{Exchange: "binance", Market: "perpetual_futures", Symbol: symbol}
		if err := validateDebugPackageOrderTarget(target); err == nil || !strings.Contains(err.Error(), "symbol") {
			t.Errorf("target symbol %q error=%v", symbol, err)
		}
	}
	for _, symbol := range []string{"BT", "BTCUSDT", strings.Repeat("A", 30)} {
		input := debugPackageInput{StreamID: "stream", Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: symbol, Interval: "1m"}
		if err := validateDebugPackageInput(input); err != nil {
			t.Errorf("valid input symbol %q rejected: %v", symbol, err)
		}
	}
}

func TestDebugPackageArchiveRejectsDeclarationToFileMismatch(t *testing.T) {
	input := debugPackageInput{StreamID: "btc-1m", Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"}
	expectedPath := "data/streams/btc-1m/binance/perpetual_futures/kline/BTCUSDT/1m.parquet"
	stream := debugPackageStreamPayload{input: input, path: expectedPath, data: []byte("parquet")}
	body := debugPackageBody{StrategyID: 42, StartTimeMS: 60_000, EndTimeMS: 120_000}
	wallet := debugPackageWallet{Assets: []debugPackageWalletAsset{{Asset: "USDT", Free: "1000.00000000", Locked: "0.00000000"}}}
	tests := []struct {
		name    string
		inputs  []debugPackageInput
		streams []debugPackageStreamPayload
	}{
		{name: "missing stream", inputs: []debugPackageInput{input}},
		{name: "extra stream", streams: []debugPackageStreamPayload{stream}},
		{name: "mismatched identity", inputs: []debugPackageInput{input}, streams: []debugPackageStreamPayload{{input: debugPackageInput{StreamID: "btc-1m", Exchange: "binance", Market: "perpetual_futures", Kind: "kline", Symbol: "ETHUSDT", Interval: "1m"}, path: expectedPath, data: []byte("parquet")}}},
		{name: "mismatched path", inputs: []debugPackageInput{input}, streams: []debugPackageStreamPayload{{input: input, path: "data/wrong.parquet", data: []byte("parquet")}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildDebugPackageArchive(body, "class MyStrategy: pass", tc.inputs, nil, debugPackageSpotSnapshot{}, wallet, tc.streams)
			if err == nil || !strings.Contains(err.Error(), "declaration-to-file mismatch") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDebugPackageArchivePreservesStrategySourceExactly(t *testing.T) {
	body := debugPackageBody{StrategyID: 42, StartTimeMS: 60_000, EndTimeMS: 120_000}
	wallet := debugPackageWallet{Assets: []debugPackageWalletAsset{{Asset: "USDT", Free: "1000.00000000", Locked: "0.00000000"}}}
	source := "\n  # leading whitespace is source\nclass MyStrategy: pass\n\n"

	archive, err := buildDebugPackageArchive(body, source, nil, nil, debugPackageSpotSnapshot{}, wallet, nil)
	if err != nil {
		t.Fatalf("build archive: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	for _, entry := range zr.File {
		if entry.Name != "strategy.py.template" {
			continue
		}
		if got := readZipText(t, entry); got != source {
			t.Fatalf("strategy source was rewritten:\n got %q\nwant %q", got, source)
		}
		return
	}
	t.Fatal("strategy.py.template is missing")
}

func TestDebugPackagePayloadSizeLimitsMatchImporter(t *testing.T) {
	tests := []struct {
		path  string
		limit int64
	}{
		{path: "manifest.yaml", limit: 4 * 1024 * 1024},
		{path: "strategy.py.template", limit: 1024 * 1024},
		{path: "wallet.yaml", limit: 1024 * 1024},
		{path: "data/streams/a/binance/spot/kline/BTCUSDT/1m.parquet", limit: 256 * 1024 * 1024},
		{path: "README.md", limit: 4 * 1024 * 1024},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			if err := validateDebugPackagePayloadSizes([]debugPackagePayloadSize{{path: tc.path, size: tc.limit}}); err != nil {
				t.Fatalf("exact boundary rejected: %v", err)
			}
			err := validateDebugPackagePayloadSizes([]debugPackagePayloadSize{{path: tc.path, size: tc.limit + 1}})
			if !errors.Is(err, errDebugPackagePayloadTooLarge) {
				t.Fatalf("boundary+1 error=%v, want payload-too-large", err)
			}
		})
	}

	parquet := int64(256 * 1024 * 1024)
	if err := validateDebugPackagePayloadSizes([]debugPackagePayloadSize{
		{path: "data/streams/a.parquet", size: parquet},
		{path: "data/streams/b.parquet", size: parquet},
	}); err != nil {
		t.Fatalf("exact aggregate boundary rejected: %v", err)
	}
	err := validateDebugPackagePayloadSizes([]debugPackagePayloadSize{
		{path: "data/streams/a.parquet", size: parquet},
		{path: "data/streams/b.parquet", size: parquet},
		{path: "README.md", size: 1},
	})
	if !errors.Is(err, errDebugPackagePayloadTooLarge) || !strings.Contains(err.Error(), "total uncompressed size") {
		t.Fatalf("aggregate boundary+1 error=%v", err)
	}
}

func TestDebugPackageStreamReferencePriceDoesNotRequireRetainingProtoRows(t *testing.T) {
	stream := debugPackageStreamPayload{
		input:          debugPackageInput{Exchange: "binance", Market: "spot", Symbol: "BTCUSDT"},
		referencePrice: "100.50000000",
	}
	if got := debugPackageStreamReferencePrice([]debugPackageStreamPayload{stream}, "binance", "spot", "BTCUSDT"); got != "100.50000000" {
		t.Fatalf("reference price = %q", got)
	}
}

func TestEncodeDebugPackageKlinesParquetCrossesBatchAndRowGroupBoundaries(t *testing.T) {
	const count = 10_001
	start := int64(1735689600000)
	rows := make([]*mdv1.MarketDataKline, 0, count)
	for index := 0; index < count; index++ {
		price := 100.0 + float64(index)
		rows = append(rows, &mdv1.MarketDataKline{
			OpenTime: timestamppb.New(timeFromMS(start + int64(index)*60_000)),
			Open:     price, High: price + 1, Low: price - 1, Close: price, Volume: float64(index),
		})
	}

	data, err := encodeDebugPackageKlinesParquet(rows)
	if err != nil {
		t.Fatalf("encode parquet: %v", err)
	}
	decoded, err := parquet.Read[debugPackageKlineRow](bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("decode parquet: %v", err)
	}
	if len(decoded) != count {
		t.Fatalf("decoded rows = %d, want %d", len(decoded), count)
	}
	if decoded[0].TimestampMS != start || decoded[count-1].TimestampMS != start+(count-1)*60_000 {
		t.Fatalf("decoded boundary timestamps = %d/%d", decoded[0].TimestampMS, decoded[count-1].TimestampMS)
	}
}

func TestDebugPackageKlineRowsRejectUnexpectedDuplicateOrUnorderedRows(t *testing.T) {
	start := int64(1735689600000)
	step := int64(time.Minute / time.Millisecond)
	row := func(timestampMS int64) *mdv1.MarketDataKline {
		return &mdv1.MarketDataKline{OpenTime: timestamppb.New(timeFromMS(timestampMS))}
	}
	tests := []struct {
		name string
		rows []*mdv1.MarketDataKline
	}{
		{name: "unexpected out-of-range row", rows: []*mdv1.MarketDataKline{row(start), row(start + step)}},
		{name: "duplicate row", rows: []*mdv1.MarketDataKline{row(start), row(start), row(start + step)}},
		{name: "unordered rows", rows: []*mdv1.MarketDataKline{row(start + step), row(start)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			end := start + step
			if tc.name != "unexpected out-of-range row" {
				end = start + 2*step
			}
			if err := validateDebugPackageKlineRows(tc.rows, start, end, step); !errors.Is(err, errDebugPackageIncompleteCoverage) {
				t.Fatalf("error = %v, want incomplete coverage", err)
			}
		})
	}
}

func TestDebugPackageV2ArchiveIsReproducibleAndHashesPayloads(t *testing.T) {
	portfolio := newFuturesDebugPackagePortfolioFake()
	market := &fakeMarketDataClient{}
	s := newDebugPackageTestServer(t, market, portfolio)
	request := func() []byte {
		req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{"runtime_id":"rt-debug","strategy_id":42,"start_time_ms":1735689600000,"end_time_ms":1735689660000}`)), 42)
		rr := httptest.NewRecorder()
		s.handlePortfolioDebugPackage(rr, req, 7)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
		}
		return append([]byte(nil), rr.Body.Bytes()...)
	}
	first := request()
	second := request()
	if !bytes.Equal(first, second) {
		t.Fatal("same strategy, snapshot, range, and market data produced different package bytes")
	}
	zr := openDebugPackageZip(t, first)
	manifest := readDebugPackageManifestV2(t, zr)
	paths := debugPackageZipPaths(zr)
	for _, item := range manifest.Integrity.Files {
		entry := paths[item.Path]
		if entry == nil {
			t.Fatalf("integrity path %q missing", item.Path)
		}
		rc, err := entry.Open()
		if err != nil {
			t.Fatalf("open %s: %v", item.Path, err)
		}
		payload, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", item.Path, err)
		}
		if got := debugPackageSHA256(payload); got != item.SHA256 {
			t.Fatalf("hash[%s] = %s, want %s", item.Path, got, item.SHA256)
		}
	}
}

func newDebugPackagePortfolioFake(code string) *fakeDebugPackagePortfolioClient {
	return &fakeDebugPackagePortfolioClient{
		strategy: &portfoliov1.StrategyEntry{StrategyId: 42, UserId: 9, Name: "mixed", Code: code},
		capabilities: &portfoliov1.GetProductCapabilitiesResponse{Capabilities: []*portfoliov1.ProductCapabilityState{{
			Name: "offline_spot_usdt", Configured: true, Effective: true,
		}}},
		snapshot: &portfoliov1.GetPortfolioSnapshotResponse{Snapshot: &portfoliov1.PortfolioSnapshot{
			PortfolioId: 7, UserId: 9,
			Wallet: &portfoliov1.PortfolioWalletState{Spot: &portfoliov1.SpotWallet{Assets: []*portfoliov1.SpotAsset{
				{Asset: "USDT", FreeDecimal: "1000.00000000", LockedDecimal: "0.00000000"},
				{Asset: "BTC", FreeDecimal: "0.01000000", LockedDecimal: "0.00000000"},
			}}},
			Venues: []*portfoliov1.VenueSnapshot{{
				VenueId: 17, Exchange: 1, Market: 1,
				SpotSymbols: []*portfoliov1.SpotSymbolMetadata{{
					Symbol: "BTCUSDT", Status: "TRADING", BaseAsset: "BTC", QuoteAsset: "USDT",
					SpotTradingAllowed: true, OrderTypes: []string{"MARKET", "LIMIT"},
					PermissionSets: []*portfoliov1.SpotSymbolPermissionSet{{Alternatives: []string{"SPOT"}}},
					Filters:        []*portfoliov1.SpotSymbolFilter{{FilterType: "LOT_SIZE", MinQty: "0.00001000", StepSize: "0.00001000"}},
				}},
				SpotRiskSnapshot: &portfoliov1.SpotRiskFactSnapshot{
					SnapshotId: "spot-fact-1", VenueId: 17, Exchange: 1, Market: 1, Symbol: "BTCUSDT",
					CapturedAt:            timestamppb.New(time.UnixMilli(1735689600000)),
					ReferencePriceDecimal: "50000.00000000",
				},
			}},
		}},
		preflight: &portfoliov1.PreflightStrategySessionResponse{Ok: true, SpotRiskSnapshots: []*portfoliov1.SpotRiskFactSnapshot{{
			SnapshotId: "spot-fact-1", VenueId: 17, Exchange: 1, Market: 1, Symbol: "BTCUSDT",
			CapturedAt: timestamppb.New(time.UnixMilli(1735689600000)),
			Metadata: &portfoliov1.SpotSymbolMetadata{
				Symbol: "BTCUSDT", Status: "TRADING", BaseAsset: "BTC", QuoteAsset: "USDT",
				SpotTradingAllowed: true, OrderTypes: []string{"MARKET", "LIMIT"},
				PermissionSets: []*portfoliov1.SpotSymbolPermissionSet{{Alternatives: []string{"SPOT"}}},
				Filters:        []*portfoliov1.SpotSymbolFilter{{FilterType: "LOT_SIZE", MinQty: "0.00001000", StepSize: "0.00001000"}},
			},
			ExchangeFilters:       []*portfoliov1.SpotSymbolFilter{{FilterType: "EXCHANGE_MAX_NUM_ORDERS", MaxNumOrders: 1000}},
			SymbolFilters:         []*portfoliov1.SpotSymbolFilter{{FilterType: "MAX_NUM_ORDERS", MaxNumOrders: 100}},
			AssetFilters:          []*portfoliov1.SpotAssetFilter{{FilterType: "MAX_ASSET", Asset: "BTC", Limit: "100"}},
			ReferencePriceDecimal: "50000.00000000",
		}}},
	}
}

func newDebugPackageTestServer(
	t *testing.T,
	market *fakeMarketDataClient,
	portfolio *fakeDebugPackagePortfolioClient,
) *server {
	t.Helper()
	inputs, targets := debugPackageDeclarationsForRuntimeFake(t, portfolio.strategy.GetCode())
	response := &strategyv1.ValidateStrategySourceResponse{Ok: true}
	for _, item := range inputs {
		response.DeclaredInputs = append(response.DeclaredInputs, &strategyv1.StrategyInputDeclaration{
			StreamId: item.StreamID,
			Exchange: item.Exchange,
			Market:   item.Market,
			Kind:     item.Kind,
			Symbol:   item.Symbol,
			Interval: item.Interval,
		})
	}
	for _, item := range targets {
		response.DeclaredOrderTargets = append(response.DeclaredOrderTargets, &strategyv1.StrategyOrderTargetBinding{
			Exchange: item.Exchange, Market: item.Market, Symbol: item.Symbol,
		})
	}
	s := newServerWithFakeMarketData(t, market)
	s.portfolios = portfolio
	s.controlPanel = &fakeResolver{resp: controlpanel.Route{RuntimeID: "rt-debug"}}
	s.cpRuntime = &fakeControlPanelStrategyProxy{validateResp: response}
	return s
}

func debugPackageDeclarationsForRuntimeFake(
	t *testing.T,
	source string,
) ([]debugPackageInput, []debugPackageOrderTarget) {
	t.Helper()
	replacer := strings.NewReplacer(
		"Exchange.BINANCE", `"binance"`,
		"Exchange.OKX", `"okx"`,
		"Market.SPOT", `"spot"`,
		"Market.PERPETUAL_FUTURES", `"perpetual_futures"`,
		"Market.DELIVERY_FUTURES", `"delivery_futures"`,
	)
	pythonTrailingComma := regexp.MustCompile(`,\s*([}\]])`)
	inputJSON := pythonTrailingComma.ReplaceAllString(
		replacer.Replace(debugPackageAssignmentListForTest(t, source, "INPUTS")), "$1",
	)
	targetJSON := pythonTrailingComma.ReplaceAllString(
		replacer.Replace(debugPackageAssignmentListForTest(t, source, "ORDER_TARGETS")), "$1",
	)
	var inputs []debugPackageInput
	if err := json.Unmarshal([]byte(inputJSON), &inputs); err != nil {
		t.Fatalf("decode runtime fake INPUTS: %v\n%s", err, inputJSON)
	}
	var targets []debugPackageOrderTarget
	if err := json.Unmarshal([]byte(targetJSON), &targets); err != nil {
		t.Fatalf("decode runtime fake ORDER_TARGETS: %v\n%s", err, targetJSON)
	}
	return inputs, targets
}

func debugPackageAssignmentListForTest(t *testing.T, source, name string) string {
	t.Helper()
	marker := name + " ="
	start := strings.Index(source, marker)
	if start < 0 {
		t.Fatalf("test strategy is missing %s", name)
	}
	start += len(marker)
	for start < len(source) && (source[start] == ' ' || source[start] == '\t' || source[start] == '\r' || source[start] == '\n') {
		start++
	}
	if start >= len(source) || source[start] != '[' {
		t.Fatalf("test strategy %s is not a list", name)
	}
	depth := 0
	quote := byte(0)
	escaped := false
	for index := start; index < len(source); index++ {
		current := source[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if current == '\\' {
				escaped = true
				continue
			}
			if current == quote {
				quote = 0
			}
			continue
		}
		if current == '\'' || current == '"' {
			quote = current
			continue
		}
		switch current {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return source[start : index+1]
			}
		}
	}
	t.Fatalf("test strategy %s list is unterminated", name)
	return ""
}

func openDebugPackageZip(t *testing.T, raw []byte) *zip.Reader {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	return zr
}

func readDebugPackageManifestV2(t *testing.T, zr *zip.Reader) debugPackageManifestV2ForTest {
	t.Helper()
	paths := debugPackageZipPaths(zr)
	entry := paths["manifest.yaml"]
	if entry == nil {
		t.Fatal("manifest.yaml missing")
	}
	var manifest debugPackageManifestV2ForTest
	if err := yaml.Unmarshal([]byte(readZipText(t, entry)), &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return manifest
}

func debugPackageZipPaths(zr *zip.Reader) map[string]*zip.File {
	out := make(map[string]*zip.File, len(zr.File))
	for _, entry := range zr.File {
		out[entry.Name] = entry
	}
	return out
}

func sortedDebugPackagePaths(paths map[string]*zip.File) []string {
	out := make([]string, 0, len(paths))
	for path := range paths {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func TestPortfolioDebugPackage_DownloadsZipWithParquet(t *testing.T) {
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
	portfolio := newFuturesDebugPackagePortfolioFake()
	s := newDebugPackageTestServer(t, fake, portfolio)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{
		"runtime_id":"rt-debug","strategy_id":42,
		"start_time_ms":1735689600000,
		"end_time_ms":1735689660000
	}`)), 42)
	rr := httptest.NewRecorder()

	s.handlePortfoliosByID().ServeHTTP(rr, req)

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
	dataPath := "data/streams/futures-btc-1m/binance/perpetual_futures/kline/BTCUSDT/1m.parquet"
	for _, name := range []string{"manifest.yaml", "wallet.yaml", "strategy.py.template", "README.md", dataPath} {
		if entries[name] == nil {
			t.Fatalf("missing zip entry %s", name)
		}
	}
	if entries[dataPath].UncompressedSize64 == 0 {
		t.Fatalf("stream parquet is empty")
	}
	manifest := readZipText(t, entries["manifest.yaml"])
	if !strings.Contains(manifest, "schema_version: 2") || !strings.Contains(manifest, "market: perpetual_futures") {
		t.Fatalf("manifest.yaml = %q", manifest)
	}
	wallet := readZipText(t, entries["wallet.yaml"])
	if !strings.Contains(wallet, "futures:") || !strings.Contains(wallet, "initial_balance: \"1000.00000000\"") {
		t.Fatalf("wallet.yaml = %q", wallet)
	}
	template := readZipText(t, entries["strategy.py.template"])
	for _, want := range []string{"Exchange.BINANCE", "Market.PERPETUAL_FUTURES", "ORDER_TARGETS", "data.exchange"} {
		if !strings.Contains(template, want) {
			t.Fatalf("strategy.py.template missing %q:\n%s", want, template)
		}
	}
	for _, forbidden := range []string{"data." + "market", `"market":"` + "futures" + `"`} {
		if strings.Contains(template, forbidden) {
			t.Fatalf("strategy.py.template contains forbidden %q:\n%s", forbidden, template)
		}
	}
	rows := readDebugPackageRows(t, entries[dataPath])
	if len(rows) != 1 {
		t.Fatalf("parquet rows = %d, want 1", len(rows))
	}
	if rows[0].TimestampMS != start || rows[0].Close != 100.5 {
		t.Fatalf("parquet row = %#v", rows[0])
	}
	if fake.lastValidateReq == nil {
		t.Fatal("coverage was not validated")
	}
	if got := fake.lastValidateReq.GetKey().GetMarket(); got != "futures" {
		t.Fatalf("market data key market = %q, want futures", got)
	}
	if fake.lastKlinesReq == nil {
		t.Fatal("kline rows were not queried")
	}
}

func TestPortfolioDebugPackage_RejectsIncompleteCoverage(t *testing.T) {
	fake := &fakeMarketDataClient{
		validateResp: &mdv1.ValidateMarketDataCoverageResponse{
			Key:           &mdv1.StreamKey{Exchange: "binance", Market: "futures", Kind: "kline", Symbol: "BTCUSDT", Interval: "1m"},
			Ok:            false,
			ExpectedCount: 2,
			ActualCount:   1,
			Reason:        "missing bars",
		},
	}
	portfolio := newFuturesDebugPackagePortfolioFake()
	s := newDebugPackageTestServer(t, fake, portfolio)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{
		"runtime_id":"rt-debug","strategy_id":42,
		"start_time_ms":1735689600000,
		"end_time_ms":1735689660000
	}`)), 42)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func TestPortfolioDebugPackage_RejectsStaleValidationWhenRowsIncomplete(t *testing.T) {
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
	portfolio := newFuturesDebugPackagePortfolioFake()
	s := newDebugPackageTestServer(t, fake, portfolio)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios/7/debug-package", strings.NewReader(`{
		"runtime_id":"rt-debug","strategy_id":42,
		"start_time_ms":1735689600000,
		"end_time_ms":1735689720000
	}`)), 42)
	rr := httptest.NewRecorder()

	s.handlePortfolioDebugPackage(rr, req, 7)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
}

func newFuturesDebugPackagePortfolioFake() *fakeDebugPackagePortfolioClient {
	return &fakeDebugPackagePortfolioClient{
		strategy: &portfoliov1.StrategyEntry{StrategyId: 42, UserId: 42, Name: "futures", Code: `
from hushine_strategy import Exchange, Market

class MyStrategy:
    INPUTS = [{"stream_id": "futures-btc-1m", "exchange": Exchange.BINANCE, "market": Market.PERPETUAL_FUTURES, "kind": "kline", "symbol": "BTCUSDT", "interval": "1m"}]
    ORDER_TARGETS = [{"exchange": Exchange.BINANCE, "market": Market.PERPETUAL_FUTURES, "symbol": "BTCUSDT"}]
    def on_market_data(self, data, wallet):
        tick = data.exchange[Exchange.BINANCE][Market.PERPETUAL_FUTURES].symbol["BTCUSDT"].interval["1m"]
        return None
`},
		snapshot: &portfoliov1.GetPortfolioSnapshotResponse{Snapshot: &portfoliov1.PortfolioSnapshot{
			PortfolioId: 7, UserId: 42,
			Wallet: &portfoliov1.PortfolioWalletState{Futures: &portfoliov1.FuturesWallet{
				InitialBalance: 1000, WalletBalance: 1000, AvailableBalance: 1000, MarginMode: "cross", PositionMode: "one_way",
			}},
		}},
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

func readZipText(t *testing.T, entry *zip.File) string {
	t.Helper()
	rc, err := entry.Open()
	if err != nil {
		t.Fatalf("open zip entry: %v", err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read zip entry: %v", err)
	}
	return string(data)
}

func timeFromMS(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}
