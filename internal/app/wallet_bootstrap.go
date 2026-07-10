package app

import (
	"strings"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
)

type spotAssetIn struct {
	Symbol        string   `json:"symbol"`
	Qty           float64  `json:"qty"`
	Locked        float64  `json:"locked"`
	AvgEntryPrice float64  `json:"avg_entry_price"`
	Price         *float64 `json:"price"`
}

type spotIn struct {
	Free   float64       `json:"free"`
	Locked float64       `json:"locked"`
	Assets []spotAssetIn `json:"assets"`
}

type futPosIn struct {
	Symbol         string  `json:"symbol"`
	Direction      int32   `json:"direction"`
	InitialBalance float64 `json:"initial_balance"`
	Leverage       float64 `json:"leverage"`
	FeeRate        float64 `json:"fee_rate"`
}

type futIn struct {
	MarginMode     string     `json:"margin_mode"`
	PositionMode   string     `json:"position_mode"`
	InitialBalance float64    `json:"initial_balance"`
	Positions      []futPosIn `json:"positions"`
}

type walletBootstrap struct {
	Futures          *portfoliov1.FuturesWallet
	Spot             *portfoliov1.SpotWallet
	TotalValue       float64
	WalletBalance    float64
	AvailableBalance float64
}

func normPositionMode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	if s == "oneway" {
		return "one_way"
	}
	return s
}

func buildSpotWallet(in *spotIn, initialBalance float64) *portfoliov1.SpotWallet {
	if in == nil {
		if initialBalance <= 0 {
			return nil
		}
		return &portfoliov1.SpotWallet{Free: initialBalance}
	}
	sw := &portfoliov1.SpotWallet{Free: in.Free, Locked: in.Locked}
	for _, a := range in.Assets {
		sym := strings.ToUpper(strings.TrimSpace(a.Symbol))
		if sym == "" {
			continue
		}
		asset := &portfoliov1.SpotAsset{
			Symbol:        sym,
			Qty:           a.Qty,
			Locked:        a.Locked,
			AvgEntryPrice: a.AvgEntryPrice,
		}
		if a.Price != nil {
			asset.Price = a.Price
		}
		sw.Assets = append(sw.Assets, asset)
	}
	return sw
}

func buildFuturesWallet(in *futIn) *portfoliov1.FuturesWallet {
	if in == nil {
		return nil
	}
	mm := strings.ToLower(strings.TrimSpace(in.MarginMode))
	if mm == "" {
		mm = "isolated"
	}
	if mm != "isolated" && mm != "cross" {
		mm = "isolated"
	}
	pm := normPositionMode(in.PositionMode)
	if pm == "" {
		pm = "one_way"
	}
	if pm != "one_way" && pm != "hedge" {
		pm = "one_way"
	}
	fw := &portfoliov1.FuturesWallet{
		MarginMode:     mm,
		PositionMode:   pm,
		InitialBalance: in.InitialBalance,
	}
	if mm == "cross" && in.InitialBalance > 0 {
		fw.WalletBalance = in.InitialBalance
		fw.AvailableBalance = in.InitialBalance
		fw.MarginBalance = in.InitialBalance
		fw.TotalMarginBalance = in.InitialBalance
		fw.TotalCrossWalletBalance = in.InitialBalance
	}
	for _, p := range in.Positions {
		sym := strings.ToUpper(strings.TrimSpace(p.Symbol))
		if sym == "" {
			continue
		}
		ib := p.InitialBalance
		if ib == 0 {
			ib = 1000
		}
		lev := p.Leverage
		if lev == 0 {
			lev = 10
		}
		fr := p.FeeRate
		if fr == 0 {
			fr = 0.0004
		}
		fw.Positions = append(fw.Positions, &portfoliov1.FuturesPosition{
			Symbol:         sym,
			Direction:      p.Direction,
			InitialBalance: ib,
			Leverage:       lev,
			FeeRate:        fr,
		})
	}
	return fw
}

func spotBootstrapValue(s *portfoliov1.SpotWallet) float64 {
	if s == nil {
		return 0
	}
	total := s.GetFree() + s.GetLocked()
	for _, asset := range s.GetAssets() {
		price := asset.GetAvgEntryPrice()
		if asset.Price != nil {
			price = asset.GetPrice()
		}
		if price > 0 {
			total += (asset.GetQty() + asset.GetLocked()) * price
		}
	}
	return total
}

func futuresBootstrapValue(f *portfoliov1.FuturesWallet) float64 {
	if f == nil {
		return 0
	}
	if f.GetMarginBalance() != 0 {
		return f.GetMarginBalance()
	}
	if f.GetWalletBalance() != 0 {
		return f.GetWalletBalance()
	}
	if f.GetInitialBalance() != 0 {
		return f.GetInitialBalance()
	}
	var total float64
	for _, p := range f.GetPositions() {
		total += p.GetInitialBalance()
	}
	return total
}

func buildWalletBootstrap(spotIn *spotIn, futuresIn *futIn, initialBalance *float64) walletBootstrap {
	var initial float64
	if initialBalance != nil {
		initial = *initialBalance
	}
	spot := buildSpotWallet(spotIn, initial)
	futures := buildFuturesWallet(futuresIn)
	spotValue := spotBootstrapValue(spot)
	futuresValue := futuresBootstrapValue(futures)
	bootstrap := walletBootstrap{
		Futures:          futures,
		Spot:             spot,
		TotalValue:       spotValue + futuresValue,
		WalletBalance:    futuresValue,
		AvailableBalance: futuresValue,
	}
	if futures == nil {
		bootstrap.WalletBalance = spotValue
		if spot != nil {
			bootstrap.AvailableBalance = spot.GetFree()
		}
	}
	return bootstrap
}
