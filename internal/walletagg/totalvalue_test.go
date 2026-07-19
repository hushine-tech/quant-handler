package walletagg

import (
	"math"
	"testing"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
)

func TestSpotEstimatedValue(t *testing.T) {
	p := 41000.0
	sw := &portfoliov1.SpotWallet{
		Assets: []*portfoliov1.SpotAsset{
			{Asset: "USDT", Free: 5000, Locked: 100},
			{Asset: "BTC", Free: 0.1, Price: &p},
		},
	}
	got := SpotEstimatedValue(sw)
	want := 5000 + 100 + 0.1*41000
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSpotEstimatedValueUsesAvgWhenNoPrice(t *testing.T) {
	sw := &portfoliov1.SpotWallet{
		Assets: []*portfoliov1.SpotAsset{
			{Asset: "USDT", Free: 100},
			{Asset: "ETH", Free: 2, AvgEntryPrice: 2500},
		},
	}
	got := SpotEstimatedValue(sw)
	want := 5100.0
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTotalValueFlatIsolated(t *testing.T) {
	fw := &portfoliov1.FuturesWallet{
		MarginMode: "isolated", PositionMode: "one_way",
		Positions: []*portfoliov1.FuturesPosition{
			{Symbol: "BTCUSDT", InitialBalance: 2000, Leverage: 10, FeeRate: 0.0004},
			{Symbol: "ETHUSDT", InitialBalance: 1500, Leverage: 10, FeeRate: 0.0004},
		},
	}
	p := 3000.0
	sw := &portfoliov1.SpotWallet{
		Assets: []*portfoliov1.SpotAsset{
			{Asset: "USDT", Free: 1000},
			{Asset: "ETH", Free: 1, Price: &p},
		},
	}
	tv := TotalValue(fw, sw)
	want := 7500.0
	if math.Abs(tv-want) > 1e-6 {
		t.Fatalf("total got %v want %v", tv, want)
	}
}

func TestTotalValuePreservesCanonicalUSDTOnlyWallet(t *testing.T) {
	sw := &portfoliov1.SpotWallet{Assets: []*portfoliov1.SpotAsset{{
		Asset: "USDT", Free: 1000, Locked: 25, FreeDecimal: "1000.00000000", LockedDecimal: "25.00000000",
	}}}
	if got := TotalValue(nil, sw); got != 1025 {
		t.Fatalf("canonical USDT-only total=%v want=1025", got)
	}
}

func TestSpotEstimatedValueWithMetadataMapsBaseAssetToSymbolPrice(t *testing.T) {
	sw := &portfoliov1.SpotWallet{Assets: []*portfoliov1.SpotAsset{
		{Asset: "USDT", Free: 100},
		{Asset: "BTC", Free: 0.1},
		{Asset: "1000SATS", Free: 2},
	}}
	metadata := []*portfoliov1.SpotSymbolMetadata{
		{Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "TRADING"},
		{Symbol: "1000SATSUSDT", BaseAsset: "1000SATS", QuoteAsset: "USDT", Status: "TRADING"},
	}
	prices := map[string]float64{"BTCUSDT": 41000, "1000SATSUSDT": 3.5}

	got := SpotEstimatedValueWithMetadata(sw, metadata, prices)
	want := 100 + 0.1*41000 + 2*3.5
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSpotEstimatedValueWithMetadataDoesNotInventSymbolFromAssetText(t *testing.T) {
	sw := &portfoliov1.SpotWallet{Assets: []*portfoliov1.SpotAsset{{Asset: "USDT", Free: 100}, {Asset: "BTC", Free: 1}}}
	got := SpotEstimatedValueWithMetadata(sw, nil, map[string]float64{"BTCUSDT": 41000})
	if got != 100 {
		t.Fatalf("valuation invented BTCUSDT without metadata: %v", got)
	}
}
