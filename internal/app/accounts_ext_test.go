package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/core-service/gen/accountv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeWalletAccountsClient struct {
	accountv1.AccountServiceClient

	resp *accountv1.GetOnlineAccountInfoResponse
	err  error
}

func (f *fakeWalletAccountsClient) GetOnlineAccountInfo(_ context.Context, _ *accountv1.GetOnlineAccountInfoRequest, _ ...grpc.CallOption) (*accountv1.GetOnlineAccountInfoResponse, error) {
	return f.resp, f.err
}

type fakeAccountVenueWalletClient struct {
	accountv1.AccountServiceClient

	listReqs   []*accountv1.ListVenuesRequest
	walletReqs []*accountv1.GetVenueOnlineInfoRequest
}

func (f *fakeAccountVenueWalletClient) ListVenues(_ context.Context, req *accountv1.ListVenuesRequest, _ ...grpc.CallOption) (*accountv1.ListVenuesResponse, error) {
	f.listReqs = append(f.listReqs, req)
	return &accountv1.ListVenuesResponse{
		Venues: []*accountv1.VenueEntry{
			{VenueId: 10, UserId: req.GetUserId(), AccountId: req.GetAccountId(), Exchange: 1, Market: 2, Environment: 1, Status: 1, DisplayName: "ok venue"},
			{VenueId: 11, UserId: req.GetUserId(), AccountId: req.GetAccountId(), Exchange: 1, Market: 2, Environment: 1, Status: 1, DisplayName: "bad venue"},
		},
		HasMore: false,
		Total:   2,
	}, nil
}

func (f *fakeAccountVenueWalletClient) GetVenueOnlineInfo(_ context.Context, req *accountv1.GetVenueOnlineInfoRequest, _ ...grpc.CallOption) (*accountv1.GetVenueOnlineInfoResponse, error) {
	f.walletReqs = append(f.walletReqs, req)
	if req.GetVenueId() == 11 {
		return nil, status.Error(codes.PermissionDenied, "invalid api key")
	}
	return &accountv1.GetVenueOnlineInfoResponse{
		Venue: &accountv1.VenueEntry{VenueId: req.GetVenueId(), UserId: req.GetUserId(), AccountId: 42, Exchange: 1, Market: 2, Environment: 1, Status: 1, DisplayName: "ok venue"},
		Wallet: &accountv1.AccountWalletState{
			Mode:                  2,
			UpdatedAt:             timestamppb.Now(),
			TotalValue:            123.45,
			SpotEstimatedValue:    0,
			FuturesPositionEquity: 123.45,
			MetricsAuthoritative:  true,
			Futures: &accountv1.FuturesWallet{
				WalletBalance:    120,
				MarginBalance:    123.45,
				AvailableBalance: 100,
			},
		},
	}, nil
}

type fakePortfolioSnapshotClient struct {
	accountv1.AccountServiceClient

	lastReq *accountv1.GetPortfolioSnapshotRequest
	resp    *accountv1.GetPortfolioSnapshotResponse
	err     error
}

func (f *fakePortfolioSnapshotClient) GetPortfolioSnapshot(_ context.Context, req *accountv1.GetPortfolioSnapshotRequest, _ ...grpc.CallOption) (*accountv1.GetPortfolioSnapshotResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type fakeCreateAccountClient struct {
	accountv1.AccountServiceClient

	createAccountReq *accountv1.CreateAccountRequest
	createVenueReq   *accountv1.CreateVenueRequest
}

func (f *fakeCreateAccountClient) CreateAccount(_ context.Context, req *accountv1.CreateAccountRequest, _ ...grpc.CallOption) (*accountv1.CreateAccountResponse, error) {
	f.createAccountReq = req
	return &accountv1.CreateAccountResponse{
		AccountId:   42,
		Name:        req.GetName(),
		Description: req.GetDescription(),
		Environment: req.GetEnvironment(),
		CreatedAt:   timestamppb.Now(),
	}, nil
}

func (f *fakeCreateAccountClient) CreateVenue(_ context.Context, req *accountv1.CreateVenueRequest, _ ...grpc.CallOption) (*accountv1.CreateVenueResponse, error) {
	f.createVenueReq = req
	return &accountv1.CreateVenueResponse{Venue: &accountv1.VenueEntry{VenueId: 88}}, nil
}

func TestCreateAccountWithBootstrapCreatesVenueFromLegacyCredentials(t *testing.T) {
	fake := &fakeCreateAccountClient{}
	s := &server{accounts: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	body := []byte(`{
		"name":"demo-account",
		"description":"legacy form",
		"mode":2,
		"api_key":"demo-key",
		"api_secret":"demo-secret",
		"futures":{"margin_mode":"cross","position_mode":"one_way"}
	}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/accounts", bytes.NewReader(body)), 7)
	rec := httptest.NewRecorder()

	s.createAccountWithBootstrap(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fake.createAccountReq == nil {
		t.Fatal("CreateAccount was not called")
	}
	if fake.createVenueReq == nil {
		t.Fatal("CreateVenue was not called for legacy exchange credentials")
	}
	if fake.createVenueReq.GetUserId() != 7 || fake.createVenueReq.GetAccountId() != 42 {
		t.Fatalf("venue owner/account mismatch: %+v", fake.createVenueReq)
	}
	if fake.createVenueReq.GetExchange() != 1 || fake.createVenueReq.GetMarket() != 2 || fake.createVenueReq.GetEnvironment() != 1 {
		t.Fatalf("venue route mismatch: exchange=%d market=%d environment=%d",
			fake.createVenueReq.GetExchange(), fake.createVenueReq.GetMarket(), fake.createVenueReq.GetEnvironment())
	}
	if fake.createVenueReq.GetApiKey() != "demo-key" {
		t.Fatalf("api_key = %q", fake.createVenueReq.GetApiKey())
	}
	var credential map[string]string
	if err := json.Unmarshal([]byte(fake.createVenueReq.GetCredentialJson()), &credential); err != nil {
		t.Fatalf("credential_json invalid: %v", err)
	}
	if credential["api_key"] != "demo-key" || credential["api_secret"] != "demo-secret" {
		t.Fatalf("credential_json = %+v", credential)
	}
	if fake.createVenueReq.GetMarginMode() != 1 || fake.createVenueReq.GetPositionMode() != 1 {
		t.Fatalf("venue modes = margin:%d position:%d", fake.createVenueReq.GetMarginMode(), fake.createVenueReq.GetPositionMode())
	}
}

func TestGetAccountVenueWalletsAggregatesPartialFailures(t *testing.T) {
	fake := &fakeAccountVenueWalletClient{}
	s := &server{accounts: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/accounts/42/venue-wallets", nil), 7)
	rec := httptest.NewRecorder()

	s.getAccountVenueWallets(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(fake.listReqs) != 1 || fake.listReqs[0].GetAccountId() != 42 || fake.listReqs[0].GetUserId() != 7 {
		t.Fatalf("list reqs = %+v", fake.listReqs)
	}
	if len(fake.walletReqs) != 2 {
		t.Fatalf("wallet req count = %d, want 2", len(fake.walletReqs))
	}
	var body struct {
		TotalValue float64 `json:"total_value"`
		Successful int     `json:"successful"`
		Failed     int     `json:"failed"`
		Items      []struct {
			Error  string `json:"error"`
			Wallet any    `json:"wallet"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.TotalValue != 123.45 || body.Successful != 1 || body.Failed != 1 {
		t.Fatalf("summary = total:%v successful:%d failed:%d", body.TotalValue, body.Successful, body.Failed)
	}
	if len(body.Items) != 2 || body.Items[1].Error != "invalid api key" {
		t.Fatalf("items = %+v", body.Items)
	}
}

func TestPortfolioSnapshotEndpointReturnsVenues(t *testing.T) {
	now := timestamppb.Now()
	fake := &fakePortfolioSnapshotClient{
		resp: &accountv1.GetPortfolioSnapshotResponse{
			Snapshot: &accountv1.PortfolioSnapshot{
				AccountId:        42,
				UserId:           7,
				TotalValue:       2500,
				WalletBalance:    2000,
				AvailableBalance: 1500,
				UpdatedAt:        now,
				Wallet: &accountv1.AccountWalletState{
					Mode:       2,
					UpdatedAt:  now,
					TotalValue: 2500,
					Futures: &accountv1.FuturesWallet{
						WalletBalance:    2000,
						AvailableBalance: 1500,
					},
				},
				Venues: []*accountv1.VenueSnapshot{
					{
						VenueId:          88,
						Exchange:         1,
						Environment:      1,
						Market:           2,
						TotalValue:       2500,
						WalletBalance:    2000,
						AvailableBalance: 1500,
						UpdatedAt:        now,
						Balances: []*accountv1.BalanceEntry{
							{Asset: "USDT", WalletBalance: 2000, AvailableBalance: 1500, ValueUsdt: 2000},
						},
						Positions: []*accountv1.PositionEntry{
							{Symbol: "ETHUSDT", PositionSide: "BOTH", Qty: 0.5, EntryPrice: 3000, MarkPrice: 3100, UnrealizedPnl: 50},
						},
					},
				},
			},
		},
	}
	s := &server{accounts: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/accounts/42/portfolio-snapshot", nil), 7)
	rec := httptest.NewRecorder()

	s.getAccountPortfolioSnapshot(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastReq == nil || fake.lastReq.GetAccountId() != 42 || fake.lastReq.GetUserId() != 7 {
		t.Fatalf("snapshot request = %+v", fake.lastReq)
	}
	var body struct {
		AccountID        int64   `json:"account_id"`
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
	if body.AccountID != 42 || body.TotalValue != 2500 || body.WalletBalance != 2000 || body.AvailableBalance != 1500 {
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

func TestGetWalletIncludesMarginBalanceFields(t *testing.T) {
	fake := &fakeWalletAccountsClient{
		resp: &accountv1.GetOnlineAccountInfoResponse{
			Wallet: &accountv1.AccountWalletState{
				TotalValue:            20759.4682,
				Mode:                  2,
				UpdatedAt:             timestamppb.Now(),
				SpotEstimatedValue:    9997.9,
				FuturesPositionEquity: 10761.5682,
				MetricsAuthoritative:  true,
				Spot: &accountv1.SpotWallet{
					Free: 5000,
					Assets: []*accountv1.SpotAsset{
						{Symbol: "USDC", Qty: 5000, Price: float64Ptr(1)},
					},
				},
				Futures: &accountv1.FuturesWallet{
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
					Positions: []*accountv1.FuturesPosition{
						{Symbol: "ETHUSDT", PositionSide: "BOTH", PositionQty: -0.021, Qty: -0.021, Leverage: 20},
					},
				},
			},
		},
	}
	s := &server{
		accounts:    fake,
		jwtSecret:   []byte("secret"),
		corsOrigins: []string{"*"},
	}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/accounts/42/wallet", nil), 7)
	rec := httptest.NewRecorder()

	s.getWallet(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		MarginBalance      float64 `json:"margin_balance"`
		TotalMarginBalance float64 `json:"total_margin_balance"`
		FuturesDisplayUSD  struct {
			WalletBalance float64 `json:"wallet_balance"`
			MarginBalance float64 `json:"margin_balance"`
			UnrealizedPnl float64 `json:"unrealized_pnl"`
		} `json:"futures_display_usd"`
		Futures struct {
			MarginBalance      float64 `json:"margin_balance"`
			TotalMarginBalance float64 `json:"total_margin_balance"`
			MultiAssetsMode    bool    `json:"multi_assets_mode"`
			Positions          []struct {
				Symbol   string  `json:"symbol"`
				Leverage float64 `json:"leverage"`
			} `json:"positions"`
		} `json:"futures"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.MarginBalance != 10000 {
		t.Fatalf("top-level margin_balance = %v, want 10000", body.MarginBalance)
	}
	if body.TotalMarginBalance != 10000 {
		t.Fatalf("top-level total_margin_balance = %v, want 10000", body.TotalMarginBalance)
	}
	if body.FuturesDisplayUSD.MarginBalance != 10761.5682 {
		t.Fatalf("futures_display_usd.margin_balance = %v, want 10761.5682", body.FuturesDisplayUSD.MarginBalance)
	}
	if body.FuturesDisplayUSD.UnrealizedPnl != 761.5682 {
		t.Fatalf("futures_display_usd.unrealized_pnl = %v, want 761.5682", body.FuturesDisplayUSD.UnrealizedPnl)
	}
	if body.Futures.MarginBalance != 10000 {
		t.Fatalf("futures.margin_balance = %v, want 10000", body.Futures.MarginBalance)
	}
	if body.Futures.TotalMarginBalance != 10000 {
		t.Fatalf("futures.total_margin_balance = %v, want 10000", body.Futures.TotalMarginBalance)
	}
	if body.Futures.MultiAssetsMode {
		t.Fatal("expected futures.multi_assets_mode=false")
	}
	if len(body.Futures.Positions) != 1 || body.Futures.Positions[0].Symbol != "ETHUSDT" || body.Futures.Positions[0].Leverage != 20 {
		t.Fatalf("futures.positions leverage not exposed: %+v", body.Futures.Positions)
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

func TestGetWallet_StructurallySeparatesCanonicalFromDisplay(t *testing.T) {
	fake := &fakeWalletAccountsClient{
		resp: &accountv1.GetOnlineAccountInfoResponse{
			Wallet: &accountv1.AccountWalletState{
				TotalValue:            20759.4682,
				Mode:                  2,
				UpdatedAt:             timestamppb.Now(),
				SpotEstimatedValue:    9997.9,
				FuturesPositionEquity: 10761.5682,
				MetricsAuthoritative:  true,
				Spot:                  &accountv1.SpotWallet{Free: 0, Assets: nil},
				Futures: &accountv1.FuturesWallet{
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
	}
	s := &server{accounts: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/accounts/42/wallet", nil), 7)
	rec := httptest.NewRecorder()
	s.getWallet(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 1. Canonical runtime fields exist at the top level.
	for _, canonicalKey := range []string{
		"mode", "updated_at",
		"wallet_balance", "margin_balance", "total_margin_balance", "available_balance",
		"spot", "futures",
	} {
		if _, ok := body[canonicalKey]; !ok {
			t.Errorf("canonical field %q missing from top-level response", canonicalKey)
		}
	}

	// 2. Namespaced display surface exists and is an object.
	displayAny, ok := body["display"]
	if !ok {
		t.Fatal("response must include a namespaced 'display' object to separate display values from canonical ones")
	}
	display, ok := displayAny.(map[string]any)
	if !ok {
		t.Fatalf("'display' must be an object, got %T", displayAny)
	}

	// 3. Every display-oriented field lives under display.*.
	for _, displayKey := range []string{
		"total_value", "spot_estimated_value", "futures_position_equity",
		"metrics_authoritative", "futures_display_usd",
	} {
		if _, ok := display[displayKey]; !ok {
			t.Errorf("display field %q missing from nested 'display' object", displayKey)
		}
	}

	// 4. Numeric parity between display.* and the legacy flat duplicates
	//    (so the nested surface is truly authoritative for display reads —
	//    it's not just extra keys that diverge from the flat values).
	if display["total_value"] != body["total_value"] {
		t.Errorf("display.total_value (%v) != top-level total_value (%v)",
			display["total_value"], body["total_value"])
	}
	if display["spot_estimated_value"] != body["spot_estimated_value"] {
		t.Errorf("display.spot_estimated_value != top-level spot_estimated_value")
	}

	// 5. ``futures_display_usd`` lives inside the display namespace with USD sums.
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

	// 6. Canonical margin_balance (USDT, 10000) and the display USD
	//    `futures_display_usd.margin_balance` (10300.45) are DIFFERENT values —
	//    they must not accidentally collapse to the same number. This is the
	//    structural test for "no caller needs to infer which is which".
	if body["margin_balance"] == fdu["margin_balance"] {
		t.Errorf("canonical margin_balance and display USD margin_balance "+
			"must be distinguishable values; got %v == %v",
			body["margin_balance"], fdu["margin_balance"])
	}
}
