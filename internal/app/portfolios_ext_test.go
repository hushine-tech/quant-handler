package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/core-service/gen/portfoliov1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeWalletPortfoliosClient struct {
	portfoliov1.PortfolioServiceClient

	resp *portfoliov1.GetPortfolioSnapshotResponse
	err  error
}

func (f *fakeWalletPortfoliosClient) GetPortfolioSnapshot(_ context.Context, _ *portfoliov1.GetPortfolioSnapshotRequest, _ ...grpc.CallOption) (*portfoliov1.GetPortfolioSnapshotResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type fakePortfolioSnapshotClient struct {
	portfoliov1.PortfolioServiceClient

	lastReq *portfoliov1.GetPortfolioSnapshotRequest
	resp    *portfoliov1.GetPortfolioSnapshotResponse
	err     error
}

func (f *fakePortfolioSnapshotClient) GetPortfolioSnapshot(_ context.Context, req *portfoliov1.GetPortfolioSnapshotRequest, _ ...grpc.CallOption) (*portfoliov1.GetPortfolioSnapshotResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type fakeCreatePortfolioClient struct {
	portfoliov1.PortfolioServiceClient

	createPortfolioReq  *portfoliov1.CreatePortfolioRequest
	createVenueReq    *portfoliov1.CreateVenueRequest
	updateSnapshotReq *portfoliov1.UpdatePortfolioSnapshotRequest
}

func (f *fakeCreatePortfolioClient) CreatePortfolio(_ context.Context, req *portfoliov1.CreatePortfolioRequest, _ ...grpc.CallOption) (*portfoliov1.CreatePortfolioResponse, error) {
	f.createPortfolioReq = req
	return &portfoliov1.CreatePortfolioResponse{
		PortfolioId:   42,
		Name:        req.GetName(),
		Description: req.GetDescription(),
		Environment: req.GetEnvironment(),
		CreatedAt:   timestamppb.Now(),
	}, nil
}

func (f *fakeCreatePortfolioClient) CreateVenue(_ context.Context, req *portfoliov1.CreateVenueRequest, _ ...grpc.CallOption) (*portfoliov1.CreateVenueResponse, error) {
	f.createVenueReq = req
	return &portfoliov1.CreateVenueResponse{Venue: &portfoliov1.VenueEntry{VenueId: 88}}, nil
}

func (f *fakeCreatePortfolioClient) UpdatePortfolioSnapshot(_ context.Context, req *portfoliov1.UpdatePortfolioSnapshotRequest, _ ...grpc.CallOption) (*portfoliov1.UpdatePortfolioSnapshotResponse, error) {
	f.updateSnapshotReq = req
	return &portfoliov1.UpdatePortfolioSnapshotResponse{Snapshot: &portfoliov1.PortfolioSnapshot{PortfolioId: req.GetPortfolioId(), UserId: req.GetUserId()}}, nil
}

func TestCreatePortfolioCreatesPlainPortfolioContext(t *testing.T) {
	fake := &fakeCreatePortfolioClient{}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	body := []byte(`{
		"name":"demo-portfolio",
		"description":"portfolio context",
		"environment":1
	}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios", bytes.NewReader(body)), 7)
	rec := httptest.NewRecorder()

	s.createPortfolio(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fake.createPortfolioReq == nil {
		t.Fatal("CreatePortfolio was not called")
	}
	if fake.createPortfolioReq.GetUserId() != 7 || fake.createPortfolioReq.GetName() != "demo-portfolio" || fake.createPortfolioReq.GetEnvironment() != 1 {
		t.Fatalf("create portfolio request = %+v", fake.createPortfolioReq)
	}
	if fake.createVenueReq != nil || fake.updateSnapshotReq != nil {
		t.Fatalf("portfolio creation must not configure venues or wallet state: venue=%+v snapshot=%+v",
			fake.createVenueReq, fake.updateSnapshotReq)
	}
}

func TestCreateBacktestPortfolioRejectsLegacyWalletBootstrapPayload(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			"spot futures payload",
			`{
				"name":"backtest-portfolio",
				"environment":0,
				"spot":{"free":250},
				"futures":{"margin_mode":"cross","position_mode":"one_way","initial_balance":1000}
			}`,
		},
		{
			"initial balance payload",
			`{
				"name":"backtest-portfolio",
				"environment":0,
				"initial_balance":1000
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeCreatePortfolioClient{}
			s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios", bytes.NewReader([]byte(tc.body))), 7)
			rec := httptest.NewRecorder()

			s.createPortfolio(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if fake.createPortfolioReq != nil || fake.createVenueReq != nil || fake.updateSnapshotReq != nil {
				t.Fatalf("legacy wallet bootstrap must not call core-service: portfolio=%+v venue=%+v snapshot=%+v",
					fake.createPortfolioReq, fake.createVenueReq, fake.updateSnapshotReq)
			}
		})
	}
}

func TestCreatePortfolioRejectsDeprecatedPortfolioLevelRuntimePayload(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			"legacy credentials",
			`{
				"name":"demo-portfolio",
				"environment":1,
				"api_key":"demo-key",
				"api_secret":"demo-secret"
			}`,
		},
		{
			"deprecated mode field",
			`{
				"name":"legacy-portfolio",
				"mode":1
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeCreatePortfolioClient{}
			s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/portfolios", bytes.NewReader([]byte(tc.body))), 7)
			rec := httptest.NewRecorder()

			s.createPortfolio(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if fake.createPortfolioReq != nil || fake.createVenueReq != nil || fake.updateSnapshotReq != nil {
				t.Fatalf("deprecated payload should not call core-service: portfolio=%+v venue=%+v snapshot=%+v",
					fake.createPortfolioReq, fake.createVenueReq, fake.updateSnapshotReq)
			}
		})
	}
}

func TestPortfolioSnapshotEndpointReturnsVenues(t *testing.T) {
	now := timestamppb.Now()
	fake := &fakePortfolioSnapshotClient{
		resp: &portfoliov1.GetPortfolioSnapshotResponse{
			Snapshot: &portfoliov1.PortfolioSnapshot{
				PortfolioId:        42,
				UserId:           7,
				TotalValue:       2500,
				WalletBalance:    2000,
				AvailableBalance: 1500,
				UpdatedAt:        now,
				Wallet: &portfoliov1.PortfolioWalletState{
					Environment: 2,
					UpdatedAt:   now,
					TotalValue:  2500,
					Futures: &portfoliov1.FuturesWallet{
						WalletBalance:    2000,
						AvailableBalance: 1500,
					},
				},
				Venues: []*portfoliov1.VenueSnapshot{
					{
						VenueId:          88,
						Exchange:         1,
						Environment:      1,
						Market:           2,
						TotalValue:       2500,
						WalletBalance:    2000,
						AvailableBalance: 1500,
						UpdatedAt:        now,
						Balances: []*portfoliov1.BalanceEntry{
							{Asset: "USDT", WalletBalance: 2000, AvailableBalance: 1500, ValueUsdt: 2000},
						},
						Positions: []*portfoliov1.PositionEntry{
							{Symbol: "ETHUSDT", PositionSide: "BOTH", Qty: 0.5, EntryPrice: 3000, MarkPrice: 3100, UnrealizedPnl: 50},
						},
					},
				},
			},
		},
	}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/portfolios/42/portfolio-snapshot", nil), 7)
	rec := httptest.NewRecorder()

	s.getPortfolioPortfolioSnapshot(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastReq == nil || fake.lastReq.GetPortfolioId() != 42 || fake.lastReq.GetUserId() != 7 {
		t.Fatalf("snapshot request = %+v", fake.lastReq)
	}
	var body struct {
		PortfolioID        int64   `json:"portfolio_id"`
		TotalValue       float64 `json:"total_value"`
		WalletBalance    float64 `json:"wallet_balance"`
		AvailableBalance float64 `json:"available_balance"`
		Items            []struct {
			Venue struct {
				VenueID          int64  `json:"venue_id"`
				ExchangeLabel    string `json:"exchange_label"`
				MarketLabel      string `json:"market_label"`
				EnvironmentLabel string `json:"environment_label"`
			} `json:"venue"`
			Snapshot struct {
				TotalValue       float64 `json:"total_value"`
				WalletBalance    float64 `json:"wallet_balance"`
				AvailableBalance float64 `json:"available_balance"`
				Balances         []struct {
					Asset string `json:"asset"`
				} `json:"balances"`
				Positions []struct {
					Symbol string `json:"symbol"`
				} `json:"positions"`
			} `json:"snapshot"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PortfolioID != 42 || body.TotalValue != 2500 || body.WalletBalance != 2000 || body.AvailableBalance != 1500 {
		t.Fatalf("summary = %+v", body)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(body.Items))
	}
	item := body.Items[0]
	if item.Venue.VenueID != 88 || item.Venue.ExchangeLabel != "binance" || item.Venue.MarketLabel != "perpetual_futures" || item.Venue.EnvironmentLabel != "demo" {
		t.Fatalf("venue = %+v", item.Venue)
	}
	if item.Snapshot.TotalValue != 2500 || len(item.Snapshot.Balances) != 1 || item.Snapshot.Balances[0].Asset != "USDT" || len(item.Snapshot.Positions) != 1 || item.Snapshot.Positions[0].Symbol != "ETHUSDT" {
		t.Fatalf("snapshot = %+v", item.Snapshot)
	}
}

func TestPortfolioSnapshotWalletIncludesMarginBalanceFields(t *testing.T) {
	now := timestamppb.Now()
	fake := &fakeWalletPortfoliosClient{
		resp: &portfoliov1.GetPortfolioSnapshotResponse{
			Snapshot: &portfoliov1.PortfolioSnapshot{
				PortfolioId: 42,
				UserId:    7,
				Wallet: &portfoliov1.PortfolioWalletState{
					TotalValue:            20759.4682,
					Environment:           2,
					UpdatedAt:             now,
					SpotEstimatedValue:    9997.9,
					FuturesPositionEquity: 10761.5682,
					MetricsAuthoritative:  true,
					Spot: &portfoliov1.SpotWallet{
						Free: 5000,
						Assets: []*portfoliov1.SpotAsset{
							{Symbol: "USDC", Qty: 5000, Price: float64Ptr(1)},
						},
					},
					Futures: &portfoliov1.FuturesWallet{
						MarginMode:                 "cross",
						PositionMode:               "one_way",
						WalletBalance:              10000,
						MarginBalance:              10000,
						TotalMarginBalance:         10000,
						AvailableBalance:           9000,
						UnrealizedPnl:              0,
						TotalUnrealizedPnl:         0,
						TotalCrossWalletBalance:    10000,
						TotalCrossUnPnl:            0,
						MultiAssetsMode:            false,
						TotalPositionInitialMargin: 123.4,
						DisplayWalletBalanceUsd:    10000,
						DisplayMarginBalanceUsd:    10761.5682,
						DisplayUnrealizedPnlUsd:    761.5682,
						Positions: []*portfoliov1.FuturesPosition{
							{Symbol: "ETHUSDT", PositionSide: "BOTH", PositionQty: -0.021, Qty: -0.021, Leverage: 20},
						},
					},
				},
			},
		},
	}
	s := &server{
		portfolios:    fake,
		jwtSecret:   []byte("secret"),
		corsOrigins: []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/portfolios/42/portfolio-snapshot", nil), 7)
	rec := httptest.NewRecorder()

	s.getPortfolioPortfolioSnapshot(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Wallet struct {
			MarginBalance      float64 `json:"margin_balance"`
			TotalMarginBalance float64 `json:"total_margin_balance"`
			Display            struct {
				FuturesDisplayUSD struct {
					WalletBalance float64 `json:"wallet_balance"`
					MarginBalance float64 `json:"margin_balance"`
					UnrealizedPnl float64 `json:"unrealized_pnl"`
				} `json:"futures_display_usd"`
			} `json:"display"`
			Futures struct {
				MarginBalance      float64 `json:"margin_balance"`
				TotalMarginBalance float64 `json:"total_margin_balance"`
				MultiAssetsMode    bool    `json:"multi_assets_mode"`
				Positions          []struct {
					Symbol   string  `json:"symbol"`
					Leverage float64 `json:"leverage"`
				} `json:"positions"`
			} `json:"futures"`
		} `json:"wallet"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Wallet.MarginBalance != 10000 {
		t.Fatalf("wallet margin_balance = %v, want 10000", body.Wallet.MarginBalance)
	}
	if body.Wallet.TotalMarginBalance != 10000 {
		t.Fatalf("wallet total_margin_balance = %v, want 10000", body.Wallet.TotalMarginBalance)
	}
	if body.Wallet.Display.FuturesDisplayUSD.MarginBalance != 10761.5682 {
		t.Fatalf("display.futures_display_usd.margin_balance = %v, want 10761.5682", body.Wallet.Display.FuturesDisplayUSD.MarginBalance)
	}
	if body.Wallet.Display.FuturesDisplayUSD.UnrealizedPnl != 761.5682 {
		t.Fatalf("display.futures_display_usd.unrealized_pnl = %v, want 761.5682", body.Wallet.Display.FuturesDisplayUSD.UnrealizedPnl)
	}
	if body.Wallet.Futures.MarginBalance != 10000 {
		t.Fatalf("wallet.futures.margin_balance = %v, want 10000", body.Wallet.Futures.MarginBalance)
	}
	if body.Wallet.Futures.TotalMarginBalance != 10000 {
		t.Fatalf("wallet.futures.total_margin_balance = %v, want 10000", body.Wallet.Futures.TotalMarginBalance)
	}
	if body.Wallet.Futures.MultiAssetsMode {
		t.Fatal("expected wallet.futures.multi_assets_mode=false")
	}
	if len(body.Wallet.Futures.Positions) != 1 || body.Wallet.Futures.Positions[0].Symbol != "ETHUSDT" || body.Wallet.Futures.Positions[0].Leverage != 20 {
		t.Fatalf("wallet.futures.positions leverage not exposed: %+v", body.Wallet.Futures.Positions)
	}
}

func float64Ptr(v float64) *float64 {
	x := v
	return &x
}

// ── canonical-wallet-display-boundary (task 3.3) ───────────────────────────
//
// Prove the gateway response structurally separates canonical runtime values
// from display-only values. Any future refactor that flattens these views
// back together trips this test.

func TestPortfolioSnapshotWalletStructurallySeparatesCanonicalFromDisplay(t *testing.T) {
	now := timestamppb.Now()
	fake := &fakeWalletPortfoliosClient{
		resp: &portfoliov1.GetPortfolioSnapshotResponse{
			Snapshot: &portfoliov1.PortfolioSnapshot{
				PortfolioId: 42,
				UserId:    7,
				Wallet: &portfoliov1.PortfolioWalletState{
					TotalValue:            20759.4682,
					Environment:           2,
					UpdatedAt:             now,
					SpotEstimatedValue:    9997.9,
					FuturesPositionEquity: 10761.5682,
					MetricsAuthoritative:  true,
					Spot:                  &portfoliov1.SpotWallet{Free: 0, Assets: nil},
					Futures: &portfoliov1.FuturesWallet{
						MarginMode:              "cross",
						PositionMode:            "one_way",
						WalletBalance:           10000,
						MarginBalance:           10000,
						TotalMarginBalance:      10000,
						AvailableBalance:        9000,
						DisplayWalletBalanceUsd: 10100.12,
						DisplayMarginBalanceUsd: 10300.45,
						DisplayUnrealizedPnlUsd: 200.33,
					},
				},
			},
		},
	}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/portfolios/42/portfolio-snapshot", nil), 7)
	rec := httptest.NewRecorder()
	s.getPortfolioPortfolioSnapshot(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wallet, ok := body["wallet"].(map[string]any)
	if !ok {
		t.Fatalf("wallet must be an object, got %T", body["wallet"])
	}

	// 1. Canonical runtime fields exist at the wallet top level.
	for _, canonicalKey := range []string{
		"environment", "updated_at",
		"wallet_balance", "margin_balance", "total_margin_balance", "available_balance",
		"spot", "futures",
	} {
		if _, ok := wallet[canonicalKey]; !ok {
			t.Errorf("canonical field %q missing from wallet response", canonicalKey)
		}
	}

	// 2. Namespaced display surface exists and is an object.
	displayAny, ok := wallet["display"]
	if !ok {
		t.Fatal("wallet must include a namespaced 'display' object")
	}
	display, ok := displayAny.(map[string]any)
	if !ok {
		t.Fatalf("'display' must be an object, got %T", displayAny)
	}

	// 3. Every display-oriented field lives under display.* and not at the
	// wallet top level.
	for _, displayKey := range []string{
		"total_value", "spot_estimated_value", "futures_position_equity",
		"metrics_authoritative", "futures_display_usd",
	} {
		if _, ok := display[displayKey]; !ok {
			t.Errorf("display field %q missing from nested 'display' object", displayKey)
		}
		if _, ok := wallet[displayKey]; ok {
			t.Errorf("display field %q must not be duplicated at wallet top level", displayKey)
		}
	}

	// 4. ``futures_display_usd`` lives inside the display namespace with USD sums.
	fduAny, ok := display["futures_display_usd"]
	if !ok || fduAny == nil {
		t.Fatal("display.futures_display_usd missing")
	}
	fdu, ok := fduAny.(map[string]any)
	if !ok {
		t.Fatalf("display.futures_display_usd must be an object, got %T", fduAny)
	}
	for _, k := range []string{"wallet_balance", "margin_balance", "unrealized_pnl"} {
		if _, ok := fdu[k]; !ok {
			t.Errorf("display.futures_display_usd.%s missing", k)
		}
	}

	// 5. Canonical margin_balance (USDT, 10000) and the display USD
	//    `futures_display_usd.margin_balance` (10300.45) are DIFFERENT values.
	if wallet["margin_balance"] == fdu["margin_balance"] {
		t.Errorf("canonical margin_balance and display USD margin_balance "+
			"must be distinguishable values; got %v == %v",
			wallet["margin_balance"], fdu["margin_balance"])
	}
}
