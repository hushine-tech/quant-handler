package walletagg

import (
	"math"
	"strings"

	accountv1 "github.com/hushine-tech/core-service/gen/accountv1"
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
func SpotEstimatedValue(sw *accountv1.SpotWallet) float64 {
	if sw == nil {
		return 0
	}
	ev := sw.GetFree() + sw.GetLocked()
	for _, a := range sw.GetAssets() {
		q := a.GetQty()
		if math.Abs(q) <= qtyEps {
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

func spotAssetMark(a *accountv1.SpotAsset) float64 {
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

// FuturesPositionEquity approximates the account-level futures equity directly
// from the protobuf wallet fields exposed by account-service.
func FuturesPositionEquity(fw *accountv1.FuturesWallet) float64 {
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

func isolatedWBRaw(p *accountv1.FuturesPosition) float64 {
	// Simplified: treat initial_balance as wallet shell regardless of position state.
	return p.GetInitialBalance()
}

// TotalValue matches strategy _compute_total_value: futures equity + spot estimated (spot falls back to free+locked if no priced assets).
func TotalValue(fw *accountv1.FuturesWallet, sw *accountv1.SpotWallet) float64 {
	feq := FuturesPositionEquity(fw)
	se := SpotEstimatedValue(sw)
	if sw != nil && len(sw.GetAssets()) > 0 {
		hasMark := false
		for _, a := range sw.GetAssets() {
			if math.Abs(a.GetQty()) <= qtyEps {
				continue
			}
			if a.Price != nil || a.GetAvgEntryPrice() > 0 {
				hasMark = true
				break
			}
		}
		if !hasMark {
			se = sw.GetFree() + sw.GetLocked()
		}
	}
	return feq + se
}

// FuturesWalletBalanceAndAvailable sets aggregates for UpdateAccountWalletState bootstrap (flat-book approximation).
func FuturesWalletBalanceAndAvailable(fw *accountv1.FuturesWallet) (wb, av float64) {
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
