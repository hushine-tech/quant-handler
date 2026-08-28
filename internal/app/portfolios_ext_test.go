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

type fakeSymbolCatalogClient struct {
	portfoliov1.PortfolioServiceClient

	response *portfoliov1.ListSymbolsResponse
	request  *portfoliov1.ListSymbolsRequest
}

func (f *fakeSymbolCatalogClient) ListSymbols(_ context.Context, request *portfoliov1.ListSymbolsRequest, _ ...grpc.CallOption) (*portfoliov1.ListSymbolsResponse, error) {
	f.request = request
	return f.response, nil
}

func TestSymbolsEndpointPreservesCanonicalAssetMetadata(t *testing.T) {
	fake := &fakeSymbolCatalogClient{response: &portfoliov1.ListSymbolsResponse{
		Symbols: []string{"BTCUSDT"},
		Entries: []*portfoliov1.SymbolCatalogEntry{{
			Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "TRADING", SpotTradingAllowed: true,
		}},
	}}
	s := &server{portfolios: fake}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/symbols?market=spot&q=btc&limit=7", nil), 42)
	rec := httptest.NewRecorder()

	s.handleSymbols(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.request == nil || fake.request.GetMarket() != "spot" || fake.request.GetQuery() != "btc" || fake.request.GetLimit() != 7 {
		t.Fatalf("request=%#v", fake.request)
	}
	var body struct {
		Symbols []string `json:"symbols"`
		Entries []struct {
			Symbol             string `json:"symbol"`
			BaseAsset          string `json:"base_asset"`
			QuoteAsset         string `json:"quote_asset"`
			Status             string `json:"status"`
			SpotTradingAllowed bool   `json:"spot_trading_allowed"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Symbols) != 1 || body.Symbols[0] != "BTCUSDT" || len(body.Entries) != 1 {
		t.Fatalf("response=%#v body=%s", body, rec.Body.String())
	}
	entry := body.Entries[0]
	if entry.Symbol != "BTCUSDT" || entry.BaseAsset != "BTC" || entry.QuoteAsset != "USDT" || entry.Status != "TRADING" || !entry.SpotTradingAllowed {
		t.Fatalf("entry=%#v", entry)
	}
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

func TestPortfolioSnapshotEndpointForwardsAuthoritativeRouteQualifiedSymbols(t *testing.T) {
	fake := &fakePortfolioSnapshotClient{resp: &portfoliov1.GetPortfolioSnapshotResponse{Snapshot: &portfoliov1.PortfolioSnapshot{
		PortfolioId: 42, UserId: 7,
	}}}
	s := &server{portfolios: fake}
	req := withUID(httptest.NewRequest(http.MethodGet,
		"/api/portfolios/42/portfolio-snapshot?required_symbol=binance:spot:BTCUSDT&required_symbol=binance:spot:ETHUSDT", nil), 7)
	rec := httptest.NewRecorder()

	s.getPortfolioPortfolioSnapshot(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastReq == nil || len(fake.lastReq.GetRequiredSymbols()) != 2 {
		t.Fatalf("snapshot request=%#v", fake.lastReq)
	}
	first, second := fake.lastReq.GetRequiredSymbols()[0], fake.lastReq.GetRequiredSymbols()[1]
	if first.GetExchange() != 1 || first.GetMarket() != 1 || first.GetSymbol() != "BTCUSDT" ||
		second.GetExchange() != 1 || second.GetMarket() != 1 || second.GetSymbol() != "ETHUSDT" {
		t.Fatalf("required symbols=%+v", fake.lastReq.GetRequiredSymbols())
	}
}

func TestPortfolioSnapshotEndpointRejectsMalformedRequiredSymbolBeforeCore(t *testing.T) {
	fake := &fakePortfolioSnapshotClient{}
	s := &server{portfolios: fake}
	req := withUID(httptest.NewRequest(http.MethodGet,
		"/api/portfolios/42/portfolio-snapshot?required_symbol=BTCUSDT", nil), 7)
	rec := httptest.NewRecorder()

	s.getPortfolioPortfolioSnapshot(rec, req, 42)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=400 body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastReq != nil {
		t.Fatalf("malformed symbol reached core: %#v", fake.lastReq)
	}
}

type fakeCreatePortfolioClient struct {
	portfoliov1.PortfolioServiceClient

	createPortfolioReq *portfoliov1.CreatePortfolioRequest
	createVenueReq     *portfoliov1.CreateVenueRequest
}

func (f *fakeCreatePortfolioClient) CreatePortfolio(_ context.Context, req *portfoliov1.CreatePortfolioRequest, _ ...grpc.CallOption) (*portfoliov1.CreatePortfolioResponse, error) {
	f.createPortfolioReq = req
	return &portfoliov1.CreatePortfolioResponse{
		PortfolioId: 42,
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
	if fake.createVenueReq != nil {
		t.Fatalf("portfolio creation must not configure venues: %+v", fake.createVenueReq)
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
			if fake.createPortfolioReq != nil || fake.createVenueReq != nil {
				t.Fatalf("legacy wallet bootstrap must not call core-service: portfolio=%+v venue=%+v",
					fake.createPortfolioReq, fake.createVenueReq)
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
			if fake.createPortfolioReq != nil || fake.createVenueReq != nil {
				t.Fatalf("deprecated payload should not call core-service: portfolio=%+v venue=%+v",
					fake.createPortfolioReq, fake.createVenueReq)
			}
		})
	}
}

func TestPortfolioSnapshotEndpointReturnsVenues(t *testing.T) {
	now := timestamppb.Now()
	fake := &fakePortfolioSnapshotClient{
		resp: &portfoliov1.GetPortfolioSnapshotResponse{
			Snapshot: &portfoliov1.PortfolioSnapshot{
				PortfolioId:      42,
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
							{Symbol: "ETHUSDT", PositionSide: portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_BOTH, Qty: 0.5, EntryPrice: 3000, MarkPrice: 3100, UnrealizedPnl: 50},
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
		PortfolioID      int64   `json:"portfolio_id"`
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
				UserId:      7,
				Wallet: &portfoliov1.PortfolioWalletState{
					TotalValue:            20759.4682,
					Environment:           2,
					UpdatedAt:             now,
					SpotEstimatedValue:    9997.9,
					FuturesPositionEquity: 10761.5682,
					MetricsAuthoritative:  true,
					Spot: &portfoliov1.SpotWallet{
						Assets: []*portfoliov1.SpotAsset{
							{Asset: "USDT", FreeDecimal: "5000", LockedDecimal: "0"},
							{Asset: "USDC", FreeDecimal: "5000", LockedDecimal: "0", PriceDecimal: func() *string { value := "1"; return &value }()},
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
							{Symbol: "ETHUSDT", PositionSide: portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_BOTH, PositionQty: -0.021, Qty: -0.021, Leverage: 20},
						},
					},
				},
			},
		},
	}
	s := &server{
		portfolios:  fake,
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

func TestProtoSpotToJSONEmitsCanonicalExactAssetShape(t *testing.T) {
	price := "42000.50000000"
	encoded, err := json.Marshal(protoSpotToJSON(&portfoliov1.SpotWallet{Assets: []*portfoliov1.SpotAsset{
		{Asset: "USDT", FreeDecimal: "1000.00000000", LockedDecimal: "0.00000000"},
		{Asset: "BTC", FreeDecimal: "0.01000000", LockedDecimal: "0.00100000", AvgEntryPriceDecimal: "40000.00000000", PriceDecimal: &price},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Assets []struct {
			Asset         string `json:"asset"`
			Free          string `json:"free"`
			Locked        string `json:"locked"`
			AvgEntryPrice string `json:"avg_entry_price"`
			Price         string `json:"price"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Assets) != 2 || body.Assets[0].Asset != "USDT" || body.Assets[0].Free != "1000.00000000" || body.Assets[0].Locked != "0.00000000" {
		t.Fatalf("USDT asset=%#v JSON=%s", body.Assets, encoded)
	}
	btc := body.Assets[1]
	if btc.Asset != "BTC" || btc.Free != "0.01000000" || btc.Locked != "0.00100000" || btc.AvgEntryPrice != "40000.00000000" || btc.Price != "42000.50000000" {
		t.Fatalf("BTC asset=%#v JSON=%s", btc, encoded)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["free"]; exists {
		t.Fatalf("legacy wallet free leaked: %s", encoded)
	}
	if bytes.Contains(encoded, []byte(`"symbol"`)) || bytes.Contains(encoded, []byte(`"qty"`)) {
		t.Fatalf("legacy asset fields leaked: %s", encoded)
	}
}

func TestProtoSpotToJSONPreservesExactZeroAndOmitsMissingPrice(t *testing.T) {
	price := "0"
	encoded, err := json.Marshal(protoSpotToJSON(&portfoliov1.SpotWallet{
		Assets: []*portfoliov1.SpotAsset{
			{Asset: "USDT", FreeDecimal: "0", LockedDecimal: "0"},
			{Asset: "BTC", FreeDecimal: "0", LockedDecimal: "0", PriceDecimal: &price},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Assets []spotAssetResponse `json:"assets"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Assets) != 2 {
		t.Fatalf("assets=%#v JSON=%s", body.Assets, encoded)
	}
	if body.Assets[0] != (spotAssetResponse{Asset: "USDT", Free: "0", Locked: "0"}) {
		t.Fatalf("exact USDT=%#v", body.Assets[0])
	}
	if body.Assets[1] != (spotAssetResponse{Asset: "BTC", Free: "0", Locked: "0", Price: "0"}) {
		t.Fatalf("exact BTC=%#v", body.Assets[1])
	}
}

func TestVenueSnapshotJSONExposesCanonicalSpotSymbolMetadata(t *testing.T) {
	encoded, err := json.Marshal(venueSnapshotToJSON(&portfoliov1.VenueSnapshot{
		VenueId: 88,
		SpotSymbols: []*portfoliov1.SpotSymbolMetadata{{
			Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "TRADING",
			BaseAssetPrecision: 8, QuoteAssetPrecision: 8, SpotTradingAllowed: true,
			PermissionSets: []*portfoliov1.SpotSymbolPermissionSet{{Alternatives: []string{"SPOT"}}},
			OrderTypes:     []string{"LIMIT", "MARKET"},
			Filters: []*portfoliov1.SpotSymbolFilter{{
				FilterType: "LOT_SIZE", MinQty: "0.00001000", MaxQty: "9000.00000000", StepSize: "0.00001000",
			}},
			SnapshotTimeMs: 123456789,
		}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		SpotSymbols []struct {
			Symbol              string   `json:"symbol"`
			BaseAsset           string   `json:"base_asset"`
			QuoteAsset          string   `json:"quote_asset"`
			Status              string   `json:"status"`
			BaseAssetPrecision  int32    `json:"base_asset_precision"`
			QuoteAssetPrecision int32    `json:"quote_asset_precision"`
			SpotTradingAllowed  bool     `json:"spot_trading_allowed"`
			OrderTypes          []string `json:"order_types"`
			PermissionSets      []struct {
				Alternatives []string `json:"alternatives"`
			} `json:"permission_sets"`
			Filters []struct {
				FilterType string `json:"filter_type"`
				MinQty     string `json:"min_qty"`
				MaxQty     string `json:"max_qty"`
				StepSize   string `json:"step_size"`
			} `json:"filters"`
			SnapshotTimeMs int64 `json:"snapshot_time_ms"`
		} `json:"spot_symbols"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.SpotSymbols) != 1 {
		t.Fatalf("spot symbols=%#v JSON=%s", body.SpotSymbols, encoded)
	}
	metadata := body.SpotSymbols[0]
	if metadata.Symbol != "BTCUSDT" || metadata.BaseAsset != "BTC" || metadata.QuoteAsset != "USDT" || metadata.Status != "TRADING" ||
		metadata.BaseAssetPrecision != 8 || metadata.QuoteAssetPrecision != 8 || !metadata.SpotTradingAllowed || metadata.SnapshotTimeMs != 123456789 {
		t.Fatalf("metadata=%#v JSON=%s", metadata, encoded)
	}
	if len(metadata.OrderTypes) != 2 || len(metadata.PermissionSets) != 1 || metadata.PermissionSets[0].Alternatives[0] != "SPOT" ||
		len(metadata.Filters) != 1 || metadata.Filters[0].FilterType != "LOT_SIZE" || metadata.Filters[0].MinQty != "0.00001000" ||
		metadata.Filters[0].MaxQty != "9000.00000000" || metadata.Filters[0].StepSize != "0.00001000" {
		t.Fatalf("metadata contract=%#v JSON=%s", metadata, encoded)
	}
}

func TestPortfolioSnapshotValuationDoesNotInventSpotSymbolWithoutMetadataMapping(t *testing.T) {
	price := "50000"
	wallet := &portfoliov1.PortfolioWalletState{
		Environment: 0,
		TotalValue:  100,
		Spot: &portfoliov1.SpotWallet{Assets: []*portfoliov1.SpotAsset{
			{Asset: "USDT", FreeDecimal: "100", LockedDecimal: "0"},
			{Asset: "BTC", FreeDecimal: "0.1", LockedDecimal: "0", PriceDecimal: &price},
		}},
	}
	snapshot := &portfoliov1.PortfolioSnapshot{
		Wallet: wallet,
		Venues: []*portfoliov1.VenueSnapshot{{
			VenueId: 7,
			Wallet:  wallet,
			SpotSymbols: []*portfoliov1.SpotSymbolMetadata{{
				Symbol: "ETHUSDT", BaseAsset: "ETH", QuoteAsset: "USDT",
			}},
		}},
	}

	body := portfolioSnapshotToJSON(snapshot)
	items := body["items"].([]portfolioPortfolioSnapshotItemJSON)
	display := items[0].Wallet["display"].(map[string]any)
	if got := display["spot_estimated_value"]; got != float64(100) {
		t.Fatalf("unmapped BTC was valued without metadata: got=%v wallet=%#v", got, items[0].Wallet)
	}

	snapshot.Venues[0].SpotSymbols = []*portfoliov1.SpotSymbolMetadata{{
		Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
	}}
	body = portfolioSnapshotToJSON(snapshot)
	items = body["items"].([]portfolioPortfolioSnapshotItemJSON)
	display = items[0].Wallet["display"].(map[string]any)
	if got := display["spot_estimated_value"]; got != float64(5100) {
		t.Fatalf("metadata-mapped BTC was not valued: got=%v wallet=%#v", got, items[0].Wallet)
	}
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
				UserId:      7,
				Wallet: &portfoliov1.PortfolioWalletState{
					TotalValue:            20759.4682,
					Environment:           2,
					UpdatedAt:             now,
					SpotEstimatedValue:    9997.9,
					FuturesPositionEquity: 10761.5682,
					MetricsAuthoritative:  true,
					Spot:                  &portfoliov1.SpotWallet{Assets: nil},
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
