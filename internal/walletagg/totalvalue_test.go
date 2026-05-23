package walletagg

import (
	"math"
	"testing"

	accountv1 "github.com/hushine-tech/core-service/gen/accountv1"
)

func TestSpotEstimatedValue(t *testing.T) {
	p := 41000.0
	sw := &accountv1.SpotWallet{
		Free: 5000, Locked: 100,
		Assets: []*accountv1.SpotAsset{
			{Symbol: "BTCUSDT", Qty: 0.1, Price: &p},
		},
	}
	got := SpotEstimatedValue(sw)
	want := 5000 + 100 + 0.1*41000
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSpotEstimatedValueUsesAvgWhenNoPrice(t *testing.T) {
	sw := &accountv1.SpotWallet{
		Free: 100,
		Assets: []*accountv1.SpotAsset{
			{Symbol: "ETHUSDT", Qty: 2, AvgEntryPrice: 2500},
		},
	}
	got := SpotEstimatedValue(sw)
	want := 5100.0
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestTotalValueFlatIsolated(t *testing.T) {
	fw := &accountv1.FuturesWallet{
		MarginMode: "isolated", PositionMode: "one_way",
		Positions: []*accountv1.FuturesPosition{
			{Symbol: "BTCUSDT", InitialBalance: 2000, Leverage: 10, FeeRate: 0.0004},
			{Symbol: "ETHUSDT", InitialBalance: 1500, Leverage: 10, FeeRate: 0.0004},
		},
	}
	p := 3000.0
	sw := &accountv1.SpotWallet{
		Free: 1000,
		Assets: []*accountv1.SpotAsset{
			{Symbol: "ETHUSDT", Qty: 1, Price: &p},
		},
	}
	tv := TotalValue(fw, sw)
	want := 7500.0
	if math.Abs(tv-want) > 1e-6 {
		t.Fatalf("total got %v want %v", tv, want)
	}
}
