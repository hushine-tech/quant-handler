package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/core-service/gen/accountv1"
	"google.golang.org/grpc"
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
