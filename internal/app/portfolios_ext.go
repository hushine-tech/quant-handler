package app

import (
	"encoding/json"
	"fmt"
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

type symbolCatalogEntryJSON struct {
	Symbol             string `json:"symbol"`
	BaseAsset          string `json:"base_asset"`
	QuoteAsset         string `json:"quote_asset"`
	Status             string `json:"status"`
	SpotTradingAllowed bool   `json:"spot_trading_allowed"`
}

type spotSymbolPermissionSetResponse struct {
	Alternatives []string `json:"alternatives"`
}

type spotSymbolFilterResponse struct {
	FilterType          string `json:"filter_type"`
	MinPrice            string `json:"min_price,omitempty"`
	MaxPrice            string `json:"max_price,omitempty"`
	TickSize            string `json:"tick_size,omitempty"`
	MinQty              string `json:"min_qty,omitempty"`
	MaxQty              string `json:"max_qty,omitempty"`
	StepSize            string `json:"step_size,omitempty"`
	MinNotional         string `json:"min_notional,omitempty"`
	MaxNotional         string `json:"max_notional,omitempty"`
	ApplyToMarket       bool   `json:"apply_to_market"`
	ApplyMinToMarket    bool   `json:"apply_min_to_market"`
	ApplyMaxToMarket    bool   `json:"apply_max_to_market"`
	AvgPriceMins        int32  `json:"avg_price_mins,omitempty"`
	Limit               int64  `json:"limit,omitempty"`
	MultiplierUp        string `json:"multiplier_up,omitempty"`
	MultiplierDown      string `json:"multiplier_down,omitempty"`
	BidMultiplierUp     string `json:"bid_multiplier_up,omitempty"`
	BidMultiplierDown   string `json:"bid_multiplier_down,omitempty"`
	AskMultiplierUp     string `json:"ask_multiplier_up,omitempty"`
	AskMultiplierDown   string `json:"ask_multiplier_down,omitempty"`
	RawJSON             string `json:"raw_json,omitempty"`
	MaxPosition         string `json:"max_position,omitempty"`
	MaxNumOrders        int64  `json:"max_num_orders,omitempty"`
	MaxNumAlgoOrders    int64  `json:"max_num_algo_orders,omitempty"`
	MaxNumIcebergOrders int64  `json:"max_num_iceberg_orders,omitempty"`
	MaxNumOrderAmends   int64  `json:"max_num_order_amends,omitempty"`
	MaxNumOrderLists    int64  `json:"max_num_order_lists,omitempty"`
}

type spotSymbolMetadataResponse struct {
	Symbol              string                            `json:"symbol"`
	Status              string                            `json:"status"`
	BaseAsset           string                            `json:"base_asset"`
	QuoteAsset          string                            `json:"quote_asset"`
	BaseAssetPrecision  int32                             `json:"base_asset_precision"`
	QuoteAssetPrecision int32                             `json:"quote_asset_precision"`
	SpotTradingAllowed  bool                              `json:"spot_trading_allowed"`
	PermissionSets      []spotSymbolPermissionSetResponse `json:"permission_sets"`
	OrderTypes          []string                          `json:"order_types"`
	Filters             []spotSymbolFilterResponse        `json:"filters"`
	SnapshotTimeMs      int64                             `json:"snapshot_time_ms"`
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
	entries := make([]symbolCatalogEntryJSON, 0, len(resp.GetEntries()))
	for _, entry := range resp.GetEntries() {
		if entry == nil {
			continue
		}
		entries = append(entries, symbolCatalogEntryJSON{
			Symbol:             entry.GetSymbol(),
			BaseAsset:          entry.GetBaseAsset(),
			QuoteAsset:         entry.GetQuoteAsset(),
			Status:             entry.GetStatus(),
			SpotTradingAllowed: entry.GetSpotTradingAllowed(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"symbols": syms,
		"entries": entries,
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
	requiredSymbols, err := parsePortfolioSnapshotRequiredSymbols(r.URL.Query()["required_symbol"])
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.portfolios.GetPortfolioSnapshot(r.Context(), &portfoliov1.GetPortfolioSnapshotRequest{
		PortfolioId:     portfolioID,
		UserId:          uid,
		RequiredSymbols: requiredSymbols,
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

func parsePortfolioSnapshotRequiredSymbols(values []string) ([]*portfoliov1.RequiredSymbol, error) {
	if len(values) > 128 {
		return nil, fmt.Errorf("required_symbol exceeds the 128-symbol limit")
	}
	out := make([]*portfoliov1.RequiredSymbol, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		parts := strings.Split(raw, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("required_symbol must use exchange:market:symbol")
		}
		exchange, err := venueExchangeCode(parts[0])
		if err != nil {
			return nil, fmt.Errorf("required_symbol exchange: %w", err)
		}
		market, err := venueMarketCode(parts[1])
		if err != nil {
			return nil, fmt.Errorf("required_symbol market: %w", err)
		}
		symbol := strings.ToUpper(strings.TrimSpace(parts[2]))
		if !validSnapshotSymbol(symbol) {
			return nil, fmt.Errorf("required_symbol symbol is invalid")
		}
		key := fmt.Sprintf("%d:%d:%s", exchange, market, symbol)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, &portfoliov1.RequiredSymbol{Exchange: exchange, Market: market, Symbol: symbol})
	}
	return out, nil
}

func validSnapshotSymbol(symbol string) bool {
	if len(symbol) < 2 || len(symbol) > 30 {
		return false
	}
	for _, char := range symbol {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func portfolioSnapshotToJSON(snapshot *portfoliov1.PortfolioSnapshot) map[string]any {
	updatedAt := ""
	if ts := snapshot.GetUpdatedAt(); ts != nil {
		updatedAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	items := make([]portfolioPortfolioSnapshotItemJSON, 0, len(snapshot.GetVenues()))
	portfolioSpotMetadata := make([]*portfoliov1.SpotSymbolMetadata, 0)
	for _, venueSnapshot := range snapshot.GetVenues() {
		portfolioSpotMetadata = append(portfolioSpotMetadata, venueSnapshot.GetSpotSymbols()...)
		item := portfolioPortfolioSnapshotItemJSON{
			Venue:    venueFromSnapshotToJSON(snapshot.GetUserId(), snapshot.GetPortfolioId(), venueSnapshot),
			Snapshot: venueSnapshotToJSON(venueSnapshot),
		}
		if wallet := venueSnapshot.GetWallet(); wallet != nil {
			item.Wallet = portfolioWalletStateToJSONWithSpotMetadata(wallet, venueSnapshot.GetSpotSymbols())
		}
		items = append(items, item)
	}
	return map[string]any{
		"portfolio_id":      snapshot.GetPortfolioId(),
		"user_id":           snapshot.GetUserId(),
		"total_value":       snapshot.GetTotalValue(),
		"wallet_balance":    snapshot.GetWalletBalance(),
		"available_balance": snapshot.GetAvailableBalance(),
		"updated_at":        updatedAt,
		"wallet":            portfolioWalletStateToJSONWithSpotMetadata(snapshot.GetWallet(), portfolioSpotMetadata),
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
		PortfolioID:      portfolioID,
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
	spotSymbols := make([]spotSymbolMetadataResponse, 0, len(snap.GetSpotSymbols()))
	for _, metadata := range snap.GetSpotSymbols() {
		if metadata != nil {
			spotSymbols = append(spotSymbols, protoSpotSymbolMetadataToJSON(metadata))
		}
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
		"spot_symbols":      spotSymbols,
	}
}

func protoSpotSymbolMetadataToJSON(metadata *portfoliov1.SpotSymbolMetadata) spotSymbolMetadataResponse {
	permissionSets := make([]spotSymbolPermissionSetResponse, 0, len(metadata.GetPermissionSets()))
	for _, permissionSet := range metadata.GetPermissionSets() {
		if permissionSet == nil {
			continue
		}
		permissionSets = append(permissionSets, spotSymbolPermissionSetResponse{
			Alternatives: append(make([]string, 0, len(permissionSet.GetAlternatives())), permissionSet.GetAlternatives()...),
		})
	}
	filters := make([]spotSymbolFilterResponse, 0, len(metadata.GetFilters()))
	for _, filter := range metadata.GetFilters() {
		if filter == nil {
			continue
		}
		filters = append(filters, spotSymbolFilterResponse{
			FilterType:          filter.GetFilterType(),
			MinPrice:            filter.GetMinPrice(),
			MaxPrice:            filter.GetMaxPrice(),
			TickSize:            filter.GetTickSize(),
			MinQty:              filter.GetMinQty(),
			MaxQty:              filter.GetMaxQty(),
			StepSize:            filter.GetStepSize(),
			MinNotional:         filter.GetMinNotional(),
			MaxNotional:         filter.GetMaxNotional(),
			ApplyToMarket:       filter.GetApplyToMarket(),
			ApplyMinToMarket:    filter.GetApplyMinToMarket(),
			ApplyMaxToMarket:    filter.GetApplyMaxToMarket(),
			AvgPriceMins:        filter.GetAvgPriceMins(),
			Limit:               filter.GetLimit(),
			MultiplierUp:        filter.GetMultiplierUp(),
			MultiplierDown:      filter.GetMultiplierDown(),
			BidMultiplierUp:     filter.GetBidMultiplierUp(),
			BidMultiplierDown:   filter.GetBidMultiplierDown(),
			AskMultiplierUp:     filter.GetAskMultiplierUp(),
			AskMultiplierDown:   filter.GetAskMultiplierDown(),
			RawJSON:             filter.GetRawJson(),
			MaxPosition:         filter.GetMaxPosition(),
			MaxNumOrders:        filter.GetMaxNumOrders(),
			MaxNumAlgoOrders:    filter.GetMaxNumAlgoOrders(),
			MaxNumIcebergOrders: filter.GetMaxNumIcebergOrders(),
			MaxNumOrderAmends:   filter.GetMaxNumOrderAmends(),
			MaxNumOrderLists:    filter.GetMaxNumOrderLists(),
		})
	}
	return spotSymbolMetadataResponse{
		Symbol:              metadata.GetSymbol(),
		Status:              metadata.GetStatus(),
		BaseAsset:           metadata.GetBaseAsset(),
		QuoteAsset:          metadata.GetQuoteAsset(),
		BaseAssetPrecision:  metadata.GetBaseAssetPrecision(),
		QuoteAssetPrecision: metadata.GetQuoteAssetPrecision(),
		SpotTradingAllowed:  metadata.GetSpotTradingAllowed(),
		PermissionSets:      permissionSets,
		OrderTypes:          append(make([]string, 0, len(metadata.GetOrderTypes())), metadata.GetOrderTypes()...),
		Filters:             filters,
		SnapshotTimeMs:      metadata.GetSnapshotTimeMs(),
	}
}

func portfolioWalletStateToJSON(wal *portfoliov1.PortfolioWalletState) map[string]any {
	return portfolioWalletStateToJSONWithSpotMetadata(wal, nil)
}

func portfolioWalletStateToJSONWithSpotMetadata(wal *portfoliov1.PortfolioWalletState, metadata []*portfoliov1.SpotSymbolMetadata) map[string]any {
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
		if len(metadata) > 0 {
			se = walletagg.SpotEstimatedValueWithMetadata(wal.GetSpot(), metadata, spotSymbolPricesFromWallet(wal.GetSpot(), metadata))
		} else {
			se = walletagg.SpotEstimatedValue(wal.GetSpot())
		}
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

func spotSymbolPricesFromWallet(wallet *portfoliov1.SpotWallet, metadata []*portfoliov1.SpotSymbolMetadata) map[string]float64 {
	marksByAsset := make(map[string]float64)
	if wallet != nil {
		for _, item := range wallet.GetAssets() {
			if item == nil {
				continue
			}
			asset := strings.ToUpper(strings.TrimSpace(item.GetAsset()))
			if asset == "" {
				asset = strings.ToUpper(strings.TrimSpace(item.GetSymbol()))
			}
			mark := item.GetAvgEntryPrice()
			if item.Price != nil {
				mark = item.GetPrice()
			}
			if asset != "" && mark > 0 {
				marksByAsset[asset] = mark
			}
		}
	}
	prices := make(map[string]float64, len(metadata))
	for _, item := range metadata {
		if item == nil || !strings.EqualFold(strings.TrimSpace(item.GetQuoteAsset()), "USDT") {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(item.GetSymbol()))
		asset := strings.ToUpper(strings.TrimSpace(item.GetBaseAsset()))
		if symbol != "" && asset != "" && marksByAsset[asset] > 0 {
			prices[symbol] = marksByAsset[asset]
		}
	}
	return prices
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

type spotAssetResponse struct {
	Asset         string `json:"asset"`
	Free          string `json:"free"`
	Locked        string `json:"locked"`
	AvgEntryPrice string `json:"avg_entry_price,omitempty"`
	Price         string `json:"price,omitempty"`
}

type spotWalletResponse struct {
	Assets []spotAssetResponse `json:"assets"`
}

func protoSpotToJSON(sw *portfoliov1.SpotWallet) any {
	if sw == nil {
		return nil
	}
	assets := make([]spotAssetResponse, 0, len(sw.GetAssets())+1)
	hasUSDT := false
	for _, item := range sw.GetAssets() {
		if item != nil && strings.EqualFold(strings.TrimSpace(firstNonEmpty(item.GetAsset(), item.GetSymbol())), "USDT") {
			hasUSDT = true
			break
		}
	}
	if !hasUSDT && (sw.GetFree() != 0 || sw.GetLocked() != 0) {
		assets = append(assets, spotAssetResponse{
			Asset: "USDT", Free: formatDecimalFloat(sw.GetFree()), Locked: formatDecimalFloat(sw.GetLocked()),
		})
	}
	for _, a := range sw.GetAssets() {
		if a == nil {
			continue
		}
		assetCode := strings.ToUpper(strings.TrimSpace(firstNonEmpty(a.GetAsset(), a.GetSymbol())))
		if assetCode == "" || (assetCode != "USDT" && strings.HasSuffix(assetCode, "USDT")) {
			continue
		}
		free := a.GetFree()
		if strings.TrimSpace(a.GetAsset()) == "" {
			free = a.GetQty()
		}
		row := spotAssetResponse{
			Asset: assetCode, Free: firstNonEmpty(strings.TrimSpace(a.GetFreeDecimal()), formatDecimalFloat(free)),
			Locked: firstNonEmpty(strings.TrimSpace(a.GetLockedDecimal()), formatDecimalFloat(a.GetLocked())),
		}
		if a.GetAvgEntryPrice() != 0 {
			row.AvgEntryPrice = formatDecimalFloat(a.GetAvgEntryPrice())
		}
		if a.Price != nil {
			row.Price = formatDecimalFloat(a.GetPrice())
		}
		assets = append(assets, row)
	}
	return spotWalletResponse{Assets: assets}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func formatDecimalFloat(value float64) string {
	if value == 0 {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
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
		PortfolioID: resp.GetPortfolioId(),
		Name:        resp.GetName(),
		Description: resp.GetDescription(),
		Environment: resp.GetEnvironment(),
		CreatedAt:   resp.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	})
}
