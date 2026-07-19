package walletagg

import (
	"math"
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
	hasUSDT := false
	for _, a := range sw.GetAssets() {
		asset, free := spotAssetIdentity(a)
		q := free + a.GetLocked()
		if math.Abs(q) <= qtyEps {
			continue
		}
		if asset == "USDT" {
			hasUSDT = true
			ev += q
			continue
		}
		mark := spotAssetMark(a)
		if mark <= 0 {
			continue
		}
		ev += q * mark
	}
	if !hasUSDT {
		ev += sw.GetFree() + sw.GetLocked()
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
	hasUSDT := false
	for _, item := range sw.GetAssets() {
		asset, free := spotAssetIdentity(item)
		quantity := free + item.GetLocked()
		if asset == "USDT" {
			hasUSDT = true
			total += quantity
			continue
		}
		if price := marksByAsset[asset]; price > 0 {
			total += quantity * price
		}
	}
	if !hasUSDT {
		total += sw.GetFree() + sw.GetLocked()
	}
	return total
}

func spotAssetIdentity(a *portfoliov1.SpotAsset) (string, float64) {
	if a == nil {
		return "", 0
	}
	asset := strings.ToUpper(strings.TrimSpace(a.GetAsset()))
	free := a.GetFree()
	if asset == "" {
		asset = strings.ToUpper(strings.TrimSpace(a.GetSymbol()))
		free = a.GetQty()
	}
	return asset, free
}

func spotAssetMark(a *portfoliov1.SpotAsset) float64 {
	if a == nil {
		return 0
	}
	if a.Price != nil {
		return *a.Price
	}
	if a.GetAvgEntryPrice() > 0 {
		return a.GetAvgEntryPrice()
	}
	return 0
}

// FuturesPositionEquity approximates the portfolio-level futures equity directly
// from the protobuf wallet fields exposed by core-service.
func FuturesPositionEquity(fw *portfoliov1.FuturesWallet) float64 {
	if fw == nil {
		return 0
	}
	mode := strings.ToLower(strings.TrimSpace(fw.GetMarginMode()))
	pos := fw.GetPositions()
	switch mode {
	case "cross":
		if len(pos) == 0 {
			return fw.GetInitialBalance()
		}
		wb := fw.GetWalletBalance()
		upnl := fw.GetTotalUnrealizedPnl()
		im := 0.0
		for _, p := range pos {
			if math.Abs(p.GetQty()) <= qtyEps {
				continue
			}
			lev := p.GetLeverage()
			if lev <= 0 {
				continue
			}
			mark := p.GetMarkPrice()
			if mark == 0 {
				mark = p.GetEntryPrice()
			}
			im += math.Abs(p.GetQty()) * mark / lev
		}
		if wb == 0 && upnl == 0 && im == 0 && fw.GetInitialBalance() > 0 {
			return fw.GetInitialBalance()
		}
		return wb + upnl + im
	default: // isolated
		sum := 0.0
		for _, p := range pos {
			if math.Abs(p.GetQty()) <= qtyEps {
				sum += p.GetInitialBalance()
				continue
			}
			im := 0.0
			if p.GetLeverage() > 0 && p.GetEntryPrice() > 0 {
				im = math.Abs(p.GetQty()) * p.GetEntryPrice() / p.GetLeverage()
			}
			sum += im + isolatedWBRaw(p) + p.GetUnrealizedPnl()
		}
		return sum
	}
}

func isolatedWBRaw(p *portfoliov1.FuturesPosition) float64 {
	// Simplified: treat initial_balance as wallet shell regardless of position state.
	return p.GetInitialBalance()
}

// TotalValue matches strategy _compute_total_value: futures equity plus Spot
// estimated value. SpotEstimatedValue already applies the rolling legacy cash
// fallback without discarding canonical USDT assets.
func TotalValue(fw *portfoliov1.FuturesWallet, sw *portfoliov1.SpotWallet) float64 {
	return FuturesPositionEquity(fw) + SpotEstimatedValue(sw)
}

// FuturesWalletBalanceAndAvailable sets bootstrap aggregates with a flat-book approximation.
func FuturesWalletBalanceAndAvailable(fw *portfoliov1.FuturesWallet) (wb, av float64) {
	if fw == nil {
		return 0, 0
	}
	mode := strings.ToLower(strings.TrimSpace(fw.GetMarginMode()))
	if mode == "cross" {
		v := fw.GetInitialBalance()
		if v == 0 && len(fw.GetPositions()) == 0 {
			return 0, 0
		}
		if v == 0 {
			for _, p := range fw.GetPositions() {
				v += p.GetInitialBalance()
			}
		}
		return v, v
	}
	sum := 0.0
	for _, p := range fw.GetPositions() {
		sum += p.GetInitialBalance()
	}
	return sum, sum
}
