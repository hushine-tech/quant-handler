package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
	"github.com/hushine-tech/quant-handler/internal/walletagg"
)

type createPortfolioBodyExt struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Environment int32  `json:"environment"`
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
	resp, err := s.portfolios.ListSymbols(r.Context(), &portfoliov1.ListSymbolsRequest{
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

type portfolioPortfolioSnapshotItemJSON struct {
	Venue    venueJSON      `json:"venue"`
	Snapshot map[string]any `json:"snapshot"`
	Wallet   map[string]any `json:"wallet,omitempty"`
}

func (s *server) getPortfolioPortfolioSnapshot(w http.ResponseWriter, r *http.Request, portfolioID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	resp, err := s.portfolios.GetPortfolioSnapshot(r.Context(), &portfoliov1.GetPortfolioSnapshotRequest{
		PortfolioId: portfolioID,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	snapshot := resp.GetSnapshot()
	if snapshot == nil {
		writeErr(w, http.StatusNotFound, "no portfolio snapshot")
		return
	}
	writeJSON(w, http.StatusOK, portfolioSnapshotToJSON(snapshot))
}

func portfolioSnapshotToJSON(snapshot *portfoliov1.PortfolioSnapshot) map[string]any {
	updatedAt := ""
	if ts := snapshot.GetUpdatedAt(); ts != nil {
		updatedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	items := make([]portfolioPortfolioSnapshotItemJSON, 0, len(snapshot.GetVenues()))
	for _, venueSnapshot := range snapshot.GetVenues() {
		item := portfolioPortfolioSnapshotItemJSON{
			Venue:    venueFromSnapshotToJSON(snapshot.GetUserId(), snapshot.GetPortfolioId(), venueSnapshot),
			Snapshot: venueSnapshotToJSON(venueSnapshot),
		}
		if wallet := venueSnapshot.GetWallet(); wallet != nil {
			item.Wallet = portfolioWalletStateToJSON(wallet)
		}
		items = append(items, item)
	}
	return map[string]any{
		"portfolio_id":        snapshot.GetPortfolioId(),
		"user_id":           snapshot.GetUserId(),
		"total_value":       snapshot.GetTotalValue(),
		"wallet_balance":    snapshot.GetWalletBalance(),
		"available_balance": snapshot.GetAvailableBalance(),
		"updated_at":        updatedAt,
		"wallet":            portfolioWalletStateToJSON(snapshot.GetWallet()),
		"items":             items,
		"venue_count":       len(items),
		"successful":        len(items),
		"failed":            0,
	}
}

func venueFromSnapshotToJSON(userID, portfolioID int64, snap *portfoliov1.VenueSnapshot) venueJSON {
	if snap == nil {
		return venueJSON{}
	}
	return venueJSON{
		VenueID:          snap.GetVenueId(),
		UserID:           userID,
		PortfolioID:        portfolioID,
		Exchange:         snap.GetExchange(),
		ExchangeLabel:    orderExchangeLabel(snap.GetExchange()),
		Market:           snap.GetMarket(),
		MarketLabel:      orderMarketLabel(snap.GetMarket()),
		Environment:      snap.GetEnvironment(),
		EnvironmentLabel: venueEnvironmentLabel(snap.GetEnvironment()),
		Status:           1,
		StatusLabel:      "active",
		DisplayName:      "venue-" + strconv.FormatInt(snap.GetVenueId(), 10),
	}
}

func venueSnapshotToJSON(snap *portfoliov1.VenueSnapshot) map[string]any {
	if snap == nil {
		return nil
	}
	updatedAt := ""
	if ts := snap.GetUpdatedAt(); ts != nil {
		updatedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	balances := make([]map[string]any, 0, len(snap.GetBalances()))
	for _, balance := range snap.GetBalances() {
		balances = append(balances, map[string]any{
			"asset":             balance.GetAsset(),
			"wallet_balance":    balance.GetWalletBalance(),
			"available_balance": balance.GetAvailableBalance(),
			"locked":            balance.GetLocked(),
			"value_usdt":        balance.GetValueUsdt(),
		})
	}
	positions := make([]map[string]any, 0, len(snap.GetPositions()))
	for _, position := range snap.GetPositions() {
		positions = append(positions, map[string]any{
			"symbol":            position.GetSymbol(),
			"position_side":     position.GetPositionSide(),
			"qty":               position.GetQty(),
			"entry_price":       position.GetEntryPrice(),
			"mark_price":        position.GetMarkPrice(),
			"unrealized_pnl":    position.GetUnrealizedPnl(),
			"margin_balance":    position.GetMarginBalance(),
			"liquidation_price": position.GetLiquidationPrice(),
		})
	}
	return map[string]any{
		"venue_id":          snap.GetVenueId(),
		"exchange":          snap.GetExchange(),
		"exchange_label":    orderExchangeLabel(snap.GetExchange()),
		"environment":       snap.GetEnvironment(),
		"environment_label": venueEnvironmentLabel(snap.GetEnvironment()),
		"market":            snap.GetMarket(),
		"market_label":      orderMarketLabel(snap.GetMarket()),
		"total_value":       snap.GetTotalValue(),
		"wallet_balance":    snap.GetWalletBalance(),
		"available_balance": snap.GetAvailableBalance(),
		"updated_at":        updatedAt,
		"balances":          balances,
		"positions":         positions,
	}
}

func portfolioWalletStateToJSON(wal *portfoliov1.PortfolioWalletState) map[string]any {
	if wal == nil {
		return nil
	}
	updatedAt := ""
	if ts := wal.GetUpdatedAt(); ts != nil {
		updatedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	// wallet_balance and available_balance live on the FuturesWallet
	// sub-message in the canonical (post-Phase-B) proto layout. The older
	// shape that stored them at PortfolioWalletState top level was retired
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
		"futures_display_usd":     protoFuturesDisplayUSDToJSON(wal.GetEnvironment(), fw),
	}

	return map[string]any{
		// ── canonical runtime fields (authoritative for trading/risk) ──
		"environment":          wal.GetEnvironment(),
		"updated_at":           updatedAt,
		"wallet_balance":       fw.GetWalletBalance(),
		"margin_balance":       fw.GetMarginBalance(),
		"total_margin_balance": fw.GetTotalMarginBalance(),
		"available_balance":    fw.GetAvailableBalance(),
		"spot":                 protoSpotToJSON(wal.GetSpot()),
		"futures":              protoFuturesToJSON(fw),
		// ── namespaced display surface ──
		"display": display,
	}
}

func protoFuturesDisplayUSDToJSON(environment int32, fw *portfoliov1.FuturesWallet) any {
	if fw == nil {
		return nil
	}
	if environment != 1 && environment != 2 {
		return nil
	}
	return map[string]any{
		"wallet_balance": fw.GetDisplayWalletBalanceUsd(),
		"margin_balance": fw.GetDisplayMarginBalanceUsd(),
		"unrealized_pnl": fw.GetDisplayUnrealizedPnlUsd(),
	}
}

func protoSpotToJSON(sw *portfoliov1.SpotWallet) any {
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

func protoFuturesToJSON(fw *portfoliov1.FuturesWallet) any {
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

// decodeCreatePortfolioBody accepts portfolio metadata only. Credentials and wallet
// payloads belong to venues, so unknown JSON fields fail closed here.
func decodeCreatePortfolioBody(r *http.Request) (createPortfolioBodyExt, error) {
	var body createPortfolioBodyExt
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return body, err
	}
	return body, nil
}

func (s *server) createPortfolio(w http.ResponseWriter, r *http.Request) {
	body, err := decodeCreatePortfolioBody(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	ctx := r.Context()
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	environment := portfolioEnvironmentFromBody(body)
	resp, err := s.portfolios.CreatePortfolio(ctx, &portfoliov1.CreatePortfolioRequest{
		Name:        body.Name,
		Description: body.Description,
		Environment: environment,
		UserId:      uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}

	writeJSON(w, http.StatusCreated, portfolioJSON{
		PortfolioID:   resp.GetPortfolioId(),
		Name:        resp.GetName(),
		Description: resp.GetDescription(),
		Environment: resp.GetEnvironment(),
		CreatedAt:   resp.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	})
}
