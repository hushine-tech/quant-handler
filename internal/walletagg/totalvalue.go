package walletagg

import (
	"math"
	"strconv"
	"strings"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
)

const qtyEps = 1e-12

// TotalsMatch checks whether spot+futures (or any partial sum) matches total_value within float tolerance.
func TotalsMatch(sum, total float64) bool {
	const absTol = 0.05
	const relTol = 1e-8
	d := math.Abs(sum - total)
	if d <= absTol {
		return true
	}
	s := math.Max(math.Abs(sum), math.Abs(total))
	if s <= absTol {
		return d <= absTol
	}
	return d/s <= relTol
}

// SpotEstimatedValue mirrors strategy SpotWallet.get_estimated_value when all priced assets have marks;
// assets with qty>0 but no price are skipped (partial sum). Always includes free+locked.
func SpotEstimatedValue(sw *portfoliov1.SpotWallet) float64 {
	if sw == nil {
		return 0
	}
	ev := 0.0
	for _, a := range sw.GetAssets() {
		asset, free := spotAssetIdentity(a)
		q := free + spotDecimalValue(a.GetLockedDecimal())
		if math.Abs(q) <= qtyEps {
			continue
		}
		if asset == "USDT" {
			ev += q
			continue
		}
		mark := spotAssetMark(a)
		if mark <= 0 {
			continue
		}
		ev += q * mark
	}
	return ev
}

// SpotEstimatedValueWithMetadata values canonical base assets using the exact
// symbol relation supplied by Binance metadata. It intentionally never builds
// a symbol by appending USDT to an asset code or by stripping a symbol suffix.
func SpotEstimatedValueWithMetadata(sw *portfoliov1.SpotWallet, metadata []*portfoliov1.SpotSymbolMetadata, symbolPrices map[string]float64) float64 {
	if sw == nil {
		return 0
	}
	marksByAsset := make(map[string]float64, len(metadata))
	for _, item := range metadata {
		if item == nil || !strings.EqualFold(strings.TrimSpace(item.GetQuoteAsset()), "USDT") {
			continue
		}
		asset := strings.ToUpper(strings.TrimSpace(item.GetBaseAsset()))
		symbol := strings.ToUpper(strings.TrimSpace(item.GetSymbol()))
		price := symbolPrices[symbol]
		if asset != "" && symbol != "" && price > 0 && !math.IsNaN(price) && !math.IsInf(price, 0) {
			marksByAsset[asset] = price
		}
	}
	total := 0.0
	for _, item := range sw.GetAssets() {
		asset, free := spotAssetIdentity(item)
		quantity := free + spotDecimalValue(item.GetLockedDecimal())
		if asset == "USDT" {
			total += quantity
			continue
		}
		if price := marksByAsset[asset]; price > 0 {
			total += quantity * price
		}
	}
	return total
}

func spotAssetIdentity(a *portfoliov1.SpotAsset) (string, float64) {
	if a == nil {
		return "", 0
	}
	asset := strings.ToUpper(strings.TrimSpace(a.GetAsset()))
	return asset, spotDecimalValue(a.GetFreeDecimal())
}

func spotAssetMark(a *portfoliov1.SpotAsset) float64 {
	if a == nil {
		return 0
	}
	if a.PriceDecimal != nil {
		return spotDecimalValue(a.GetPriceDecimal())
	}
	if value := spotDecimalValue(a.GetAvgEntryPriceDecimal()); value > 0 {
		return value
	}
	return 0
}

func spotDecimalValue(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

// FuturesPositionEquity approximates the portfolio-level futures equity directly
// from the protobuf wallet fields exposed by core-service.
func FuturesPositionEquity(fw *portfoliov1.FuturesWallet) float64 {
	if fw == nil {
		return 0
	}
	pos := fw.GetPositions()
	if len(pos) == 0 {
		return fw.GetInitialBalance()
	}
	crossEquity := fw.GetWalletBalance()
	hasCross := false
	isolatedEquity := 0.0
	for _, p := range pos {
		switch strings.ToLower(strings.TrimSpace(p.GetMarginMode())) {
		case "cross":
			hasCross = true
			if math.Abs(p.GetQty()) <= qtyEps || p.GetLeverage() <= 0 {
				continue
			}
			mark := p.GetMarkPrice()
			if mark == 0 {
				mark = p.GetEntryPrice()
			}
			crossEquity += p.GetUnrealizedPnl() + math.Abs(p.GetQty())*mark/p.GetLeverage()
		case "isolated":
			if math.Abs(p.GetQty()) <= qtyEps {
				isolatedEquity += p.GetInitialBalance()
				continue
			}
			im := 0.0
			if p.GetLeverage() > 0 && p.GetEntryPrice() > 0 {
				im = math.Abs(p.GetQty()) * p.GetEntryPrice() / p.GetLeverage()
			}
			isolatedEquity += im + isolatedWBRaw(p) + p.GetUnrealizedPnl()
		}
	}
	if !hasCross && crossEquity == 0 && fw.GetInitialBalance() > 0 {
		crossEquity = fw.GetInitialBalance()
	}
	return crossEquity + isolatedEquity
}

func isolatedWBRaw(p *portfoliov1.FuturesPosition) float64 {
	// Simplified: treat initial_balance as wallet shell regardless of position state.
	return p.GetInitialBalance()
}

// TotalValue matches strategy _compute_total_value: futures equity plus Spot
// estimated value from canonical Spot assets.
func TotalValue(fw *portfoliov1.FuturesWallet, sw *portfoliov1.SpotWallet) float64 {
	return FuturesPositionEquity(fw) + SpotEstimatedValue(sw)
}

// FuturesWalletBalanceAndAvailable sets bootstrap aggregates with a flat-book approximation.
func FuturesWalletBalanceAndAvailable(fw *portfoliov1.FuturesWallet) (wb, av float64) {
	if fw == nil {
		return 0, 0
	}
	cross := fw.GetInitialBalance()
	isolated := 0.0
	for _, p := range fw.GetPositions() {
		if strings.EqualFold(strings.TrimSpace(p.GetMarginMode()), "isolated") {
			isolated += p.GetInitialBalance()
		}
	}
	return cross + isolated, cross + isolated
}
