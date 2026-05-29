package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	accountv1 "github.com/hushine-tech/core-service/gen/accountv1"
	"github.com/hushine-tech/quant-handler/internal/walletagg"
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

type createAccountBodyExt struct {
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	Mode           int32   `json:"mode"`
	Environment    int32   `json:"environment"`
	APIKey         string  `json:"api_key"`
	APISecret      string  `json:"api_secret"`
	InitialBalance float64 `json:"initial_balance"`
	Spot           *spotIn `json:"spot"`
	Futures        *futIn  `json:"futures"`
}

func normPositionMode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "-", "_")
	if s == "oneway" {
		return "one_way"
	}
	return s
}

func buildSpotWallet(in *spotIn, initialBalance float64) *accountv1.SpotWallet {
	if in == nil {
		if initialBalance <= 0 {
			return nil
		}
		return &accountv1.SpotWallet{Free: initialBalance}
	}
	sw := &accountv1.SpotWallet{Free: in.Free, Locked: in.Locked}
	for _, a := range in.Assets {
		sym := strings.ToUpper(strings.TrimSpace(a.Symbol))
		if sym == "" {
			continue
		}
		asset := &accountv1.SpotAsset{
			Symbol: sym, Qty: a.Qty, Locked: a.Locked, AvgEntryPrice: a.AvgEntryPrice,
		}
		if a.Price != nil {
			asset.Price = a.Price
		}
		sw.Assets = append(sw.Assets, asset)
	}
	return sw
}

func buildFuturesWallet(in *futIn) *accountv1.FuturesWallet {
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
	fw := &accountv1.FuturesWallet{
		MarginMode: mm, PositionMode: pm, InitialBalance: in.InitialBalance,
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
		fw.Positions = append(fw.Positions, &accountv1.FuturesPosition{
			Symbol: sym, Direction: p.Direction, InitialBalance: ib, Leverage: lev, FeeRate: fr,
		})
	}
	return fw
}

func (s *server) handleSymbols(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	market := strings.TrimSpace(r.URL.Query().Get("market"))
	if market == "" {
		writeErr(w, http.StatusBadRequest, "query parameter market is required (spot or usdm_futures)")
		return
	}
	q := r.URL.Query().Get("q")
	limit := 80
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	resp, err := s.accounts.ListSymbols(r.Context(), &accountv1.ListSymbolsRequest{
		Market: market, Query: q, Limit: int32(limit),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	syms := resp.GetSymbols()
	if syms == nil {
		syms = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"symbols": syms,
		"stale":   resp.GetStale(),
	})
}

func (s *server) getWallet(w http.ResponseWriter, r *http.Request, id int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	ctx := r.Context()
	resp, err := s.accounts.GetOnlineAccountInfo(ctx, &accountv1.GetOnlineAccountInfoRequest{
		AccountId: id,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	wal := resp.GetWallet()
	if wal == nil {
		writeErr(w, http.StatusNotFound, "no wallet")
		return
	}
	updatedAt := ""
	if ts := wal.GetUpdatedAt(); ts != nil {
		updatedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	// wallet_balance and available_balance live on the FuturesWallet
	// sub-message in the canonical (post-Phase-B) proto layout. The older
	// shape that stored them at AccountWalletState top level was retired
	// when strategy-service ↔ core-service moved to the canonical
	// contract. GetFutures() is nil-safe; the protobuf-generated getters
	// return 0 when the receiver is nil.
	fw := wal.GetFutures()
	se := wal.GetSpotEstimatedValue()
	feq := wal.GetFuturesPositionEquity()
	auth := wal.GetMetricsAuthoritative()
	if !auth || !walletagg.TotalsMatch(se+feq, wal.GetTotalValue()) {
		se = walletagg.SpotEstimatedValue(wal.GetSpot())
		feq = walletagg.FuturesPositionEquity(fw)
		auth = false
	}

	// Explicitly-namespaced display surface (canonical-wallet-display-boundary).
	// Everything here is display-derived and MUST NOT feed runtime decisions —
	// the frontend reads these for exchange-aligned UI presentation; strategy
	// / risk / reconciliation services read the canonical sub-objects instead.
	display := map[string]any{
		"total_value":             wal.GetTotalValue(),
		"spot_estimated_value":    se,
		"futures_position_equity": feq,
		"metrics_authoritative":   auth,
		"futures_display_usd":     protoFuturesDisplayUSDToJSON(wal.GetMode(), fw),
	}

	writeJSON(w, http.StatusOK, map[string]any{
		// ── canonical runtime fields (authoritative for trading/risk) ──
		"mode":                 wal.GetMode(),
		"updated_at":           updatedAt,
		"wallet_balance":       fw.GetWalletBalance(),
		"margin_balance":       fw.GetMarginBalance(),
		"total_margin_balance": fw.GetTotalMarginBalance(),
		"available_balance":    fw.GetAvailableBalance(),
		"spot":                 protoSpotToJSON(wal.GetSpot()),
		"futures":              protoFuturesToJSON(fw),
		// ── namespaced display surface ──
		// Prefer ``display.*`` for new UI code. The flat top-level display
		// duplicates below are kept for backward compat with existing
		// frontend readers; they are deprecated and will be removed in a
		// follow-up.
		"display": display,
		// Legacy flat display fields (deprecated — read from ``display.*``).
		"total_value":             wal.GetTotalValue(),
		"spot_estimated_value":    se,
		"futures_position_equity": feq,
		"metrics_authoritative":   auth,
		"futures_display_usd":     protoFuturesDisplayUSDToJSON(wal.GetMode(), fw),
	})
}

func protoFuturesDisplayUSDToJSON(mode int32, fw *accountv1.FuturesWallet) any {
	if fw == nil {
		return nil
	}
	if mode != 1 && mode != 2 {
		return nil
	}
	return map[string]any{
		"wallet_balance": fw.GetDisplayWalletBalanceUsd(),
		"margin_balance": fw.GetDisplayMarginBalanceUsd(),
		"unrealized_pnl": fw.GetDisplayUnrealizedPnlUsd(),
	}
}

func protoSpotToJSON(sw *accountv1.SpotWallet) any {
	if sw == nil {
		return nil
	}
	type asset struct {
		Symbol        string   `json:"symbol"`
		Qty           float64  `json:"qty"`
		Locked        float64  `json:"locked"`
		AvgEntryPrice float64  `json:"avg_entry_price"`
		Price         *float64 `json:"price,omitempty"`
	}
	out := map[string]any{
		"free": sw.GetFree(), "locked": sw.GetLocked(),
	}
	var assets []asset
	for _, a := range sw.GetAssets() {
		assets = append(assets, asset{
			Symbol: a.GetSymbol(), Qty: a.GetQty(), Locked: a.GetLocked(),
			AvgEntryPrice: a.GetAvgEntryPrice(), Price: a.Price,
		})
	}
	out["assets"] = assets
	return out
}

func protoFuturesToJSON(fw *accountv1.FuturesWallet) any {
	if fw == nil {
		return nil
	}
	type pos struct {
		Symbol         string   `json:"symbol"`
		Direction      int32    `json:"direction"`
		InitialBalance float64  `json:"initial_balance"`
		Leverage       float64  `json:"leverage"`
		FeeRate        float64  `json:"fee_rate"`
		Qty            float64  `json:"qty"`
		EntryPrice     float64  `json:"entry_price"`
		MarkPrice      float64  `json:"mark_price"`
		UnrealizedPnl  float64  `json:"unrealized_pnl"`
		PositionSide   string   `json:"position_side"`
		DisplayEquity  *float64 `json:"display_equity,omitempty"`
	}
	out := map[string]any{
		"margin_mode":                     fw.GetMarginMode(),
		"position_mode":                   fw.GetPositionMode(),
		"initial_balance":                 fw.GetInitialBalance(),
		"wallet_balance":                  fw.GetWalletBalance(),
		"margin_balance":                  fw.GetMarginBalance(),
		"total_margin_balance":            fw.GetTotalMarginBalance(),
		"available_balance":               fw.GetAvailableBalance(),
		"unrealized_pnl":                  fw.GetUnrealizedPnl(),
		"total_unrealized_pnl":            fw.GetTotalUnrealizedPnl(),
		"total_position_initial_margin":   fw.GetTotalPositionInitialMargin(),
		"total_open_order_initial_margin": fw.GetTotalOpenOrderInitialMargin(),
		"total_maint_margin":              fw.GetTotalMaintMargin(),
		"total_cross_wallet_balance":      fw.GetTotalCrossWalletBalance(),
		"total_cross_un_pnl":              fw.GetTotalCrossUnPnl(),
		"multi_assets_mode":               fw.GetMultiAssetsMode(),
		"portfolio_margin":                fw.GetPortfolioMargin(),
	}
	var ps []pos
	for _, p := range fw.GetPositions() {
		row := pos{
			Symbol: p.GetSymbol(), Direction: p.GetDirection(), InitialBalance: p.GetInitialBalance(),
			Leverage: p.GetLeverage(), FeeRate: p.GetFeeRate(), Qty: p.GetQty(), EntryPrice: p.GetEntryPrice(),
			MarkPrice: p.GetMarkPrice(), UnrealizedPnl: p.GetUnrealizedPnl(), PositionSide: p.GetPositionSide(),
		}
		if p.DisplayEquity != nil {
			v := *p.DisplayEquity
			row.DisplayEquity = &v
		}
		ps = append(ps, row)
	}
	out["positions"] = ps
	return out
}

// decodeCreateAccountBody accepts extended JSON for wallet wizard.
func decodeCreateAccountBody(r *http.Request) (createAccountBodyExt, error) {
	var body createAccountBodyExt
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return body, err
	}
	return body, nil
}

func (s *server) createAccountWithBootstrap(w http.ResponseWriter, r *http.Request) {
	body, err := decodeCreateAccountBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := validateFuturesPayload(body.Futures); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	environment := accountEnvironmentFromBody(body)
	resp, err := s.accounts.CreateAccount(ctx, &accountv1.CreateAccountRequest{
		Name:           body.Name,
		Description:    body.Description,
		Environment:    environment,
		InitialBalance: body.InitialBalance,
		UserId:         uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	if shouldApplyWalletBootstrap(body) {
		sw := buildSpotWallet(body.Spot, body.InitialBalance)
		fw := buildFuturesWallet(body.Futures)
		if sw == nil {
			sw = &accountv1.SpotWallet{}
		}
		if fw == nil {
			fw = &accountv1.FuturesWallet{MarginMode: "isolated", PositionMode: "one_way"}
		}
		tv := walletagg.TotalValue(fw, sw)
		wb, av := walletagg.FuturesWalletBalanceAndAvailable(fw)
		if tv > 0 && wb == 0 && av == 0 {
			wb, av = tv, tv
		}
		_, err = s.accounts.UpdateAccountWalletState(ctx, &accountv1.UpdateAccountWalletStateRequest{
			AccountId:        resp.GetAccountId(),
			Futures:          fw,
			Spot:             sw,
			TotalValue:       tv,
			WalletBalance:    wb,
			AvailableBalance: av,
		})
		if err != nil {
			code, msg := grpcToHTTP(err)
			writeErr(w, code, "create ok but wallet bootstrap failed: "+msg)
			return
		}
	}

	writeJSON(w, http.StatusCreated, accountJSON{
		AccountID:   resp.GetAccountId(),
		Name:        resp.GetName(),
		Description: resp.GetDescription(),
		Mode:        legacyAccountModeFromEnvironment(resp.GetEnvironment()),
		Environment: resp.GetEnvironment(),
		CreatedAt:   resp.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	})
}
