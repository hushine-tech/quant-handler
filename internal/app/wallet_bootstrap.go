package app

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
)

type spotAssetIn struct {
	Asset         string `json:"asset"`
	Free          string `json:"free"`
	Locked        string `json:"locked"`
	AvgEntryPrice string `json:"avg_entry_price"`
	Price         string `json:"price"`
}

type spotIn struct {
	Assets []spotAssetIn `json:"assets"`
}

var (
	spotAssetCodePattern = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)
	spotDecimalPattern   = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,8})?$`)
)

type futPosIn struct {
	Symbol         string  `json:"symbol"`
	Direction      int32   `json:"direction"`
	InitialBalance float64 `json:"initial_balance"`
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

func buildSpotWallet(in *spotIn) (*portfoliov1.SpotWallet, error) {
	if in == nil {
		return nil, nil
	}
	sw := &portfoliov1.SpotWallet{Assets: make([]*portfoliov1.SpotAsset, 0, len(in.Assets))}
	seen := make(map[string]struct{}, len(in.Assets))
	hasUSDT := false
	for index, input := range in.Assets {
		rawAsset := strings.TrimSpace(input.Asset)
		if !spotAssetCodePattern.MatchString(rawAsset) {
			return nil, fmt.Errorf("spot.assets[%d].asset is invalid", index)
		}
		assetCode := strings.ToUpper(rawAsset)
		if assetCode != "USDT" && strings.HasSuffix(assetCode, "USDT") {
			return nil, fmt.Errorf("spot.assets[%d].asset must be a base asset, not trading symbol %s", index, assetCode)
		}
		if _, exists := seen[assetCode]; exists {
			return nil, fmt.Errorf("spot.assets contains duplicate asset %s", assetCode)
		}
		seen[assetCode] = struct{}{}
		hasUSDT = hasUSDT || assetCode == "USDT"
		if err := validateSpotBootstrapDecimal(fmt.Sprintf("spot.assets[%d].free", index), input.Free); err != nil {
			return nil, err
		}
		if err := validateSpotBootstrapDecimal(fmt.Sprintf("spot.assets[%d].locked", index), input.Locked); err != nil {
			return nil, err
		}
		asset := &portfoliov1.SpotAsset{
			Asset: assetCode, FreeDecimal: input.Free, LockedDecimal: input.Locked,
		}
		if input.AvgEntryPrice != "" {
			if err := validateSpotBootstrapDecimal(fmt.Sprintf("spot.assets[%d].avg_entry_price", index), input.AvgEntryPrice); err != nil {
				return nil, err
			}
			asset.AvgEntryPriceDecimal = input.AvgEntryPrice
		}
		if input.Price != "" {
			if err := validateSpotBootstrapDecimal(fmt.Sprintf("spot.assets[%d].price", index), input.Price); err != nil {
				return nil, err
			}
			price := input.Price
			asset.PriceDecimal = &price
		}
		sw.Assets = append(sw.Assets, asset)
	}
	if !hasUSDT {
		return nil, fmt.Errorf("spot.assets must include USDT")
	}
	return sw, nil
}

func validateSpotBootstrapDecimal(field, raw string) error {
	if !spotDecimalPattern.MatchString(raw) {
		return fmt.Errorf("%s must be a non-negative decimal string with at most 8 fractional digits", field)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("%s is invalid", field)
	}
	return nil
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
		fr := p.FeeRate
		if fr == 0 {
			fr = 0.0004
		}
		fw.Positions = append(fw.Positions, &portfoliov1.FuturesPosition{
			Symbol:         sym,
			Direction:      p.Direction,
			InitialBalance: ib,
			FeeRate:        fr,
		})
	}
	return fw
}

func spotBootstrapValue(s *portfoliov1.SpotWallet) float64 {
	if s == nil {
		return 0
	}
	total := 0.0
	for _, asset := range s.GetAssets() {
		quantity := spotBootstrapDecimalValue(asset.GetFreeDecimal()) + spotBootstrapDecimalValue(asset.GetLockedDecimal())
		if strings.EqualFold(strings.TrimSpace(asset.GetAsset()), "USDT") {
			total += quantity
			continue
		}
		price := spotBootstrapDecimalValue(asset.GetAvgEntryPriceDecimal())
		if asset.PriceDecimal != nil {
			price = spotBootstrapDecimalValue(asset.GetPriceDecimal())
		}
		if price > 0 {
			total += quantity * price
		}
	}
	return total
}

func spotBootstrapDecimalValue(raw string) float64 {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
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

func buildWalletBootstrap(spotIn *spotIn, futuresIn *futIn) (walletBootstrap, error) {
	spot, err := buildSpotWallet(spotIn)
	if err != nil {
		return walletBootstrap{}, err
	}
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
			for _, asset := range spot.GetAssets() {
				if strings.EqualFold(strings.TrimSpace(asset.GetAsset()), "USDT") {
					bootstrap.AvailableBalance = spotBootstrapDecimalValue(asset.GetFreeDecimal())
					break
				}
			}
		}
	}
	return bootstrap, nil
}
