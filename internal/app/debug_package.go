package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
)

const (
	debugPackageKlineLimit          = int32(1000)
	debugPackageSchemaVersion       = 2
	debugPackageSpotCapability      = "offline_spot_usdt"
	debugPackageSpotFactsSchema     = "spot-risk-facts-v1"
	debugPackageDefaultKind         = "kline"
	debugPackageIntegrityAlgorithm  = "sha256"
	debugPackageDefaultWalletAsset  = "USDT"
	debugPackageDefaultDecimalValue = "0.00000000"
	debugPackageMaxStreams          = 128
	debugPackageMaxOrderTargets     = 128
	debugPackageMaxRequiredSymbols  = 128
	debugPackageMaxComponentBytes   = 128
	debugPackageMaxTotalBars        = int64(2_000_000)
	debugPackageMaxManifestBytes    = int64(4 * 1024 * 1024)
	debugPackageMaxStrategyBytes    = int64(1024 * 1024)
	debugPackageMaxWalletBytes      = int64(1024 * 1024)
	debugPackageMaxParquetBytes     = int64(256 * 1024 * 1024)
	debugPackageMaxOtherEntryBytes  = int64(4 * 1024 * 1024)
	debugPackageMaxTotalBytes       = int64(512 * 1024 * 1024)
)

var (
	errDebugPackageIncompleteCoverage = errors.New("requested range has incomplete market data")
	errDebugPackagePayloadTooLarge    = errors.New("debug package payload exceeds importer limits")
	debugPackagePathComponentPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	debugPackageSymbolPattern         = regexp.MustCompile(`^[A-Z0-9]{2,30}$`)
	debugPackageDecimalPattern        = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
)

type debugPackageHTTPError struct {
	status int
	msg    string
}

func (e *debugPackageHTTPError) Error() string { return e.msg }

type debugPackageBody struct {
	StrategyID  int64  `json:"strategy_id"`
	RuntimeID   string `json:"runtime_id"`
	StartTimeMS int64  `json:"start_time_ms"`
	EndTimeMS   int64  `json:"end_time_ms"`
}

type debugPackageInput struct {
	StreamID string `json:"stream_id" yaml:"stream_id"`
	Exchange string `json:"exchange" yaml:"exchange"`
	Market   string `json:"market" yaml:"market"`
	Kind     string `json:"kind" yaml:"kind"`
	Symbol   string `json:"symbol" yaml:"symbol"`
	Interval string `json:"interval" yaml:"interval"`
}

type debugPackageOrderTarget struct {
	Exchange string `json:"exchange" yaml:"exchange"`
	Market   string `json:"market" yaml:"market"`
	Symbol   string `json:"symbol" yaml:"symbol"`
}

type debugPackageDataFile struct {
	StreamID string `yaml:"stream_id"`
	Route    string `yaml:"route"`
	Path     string `yaml:"path"`
}

type debugPackageWalletAsset struct {
	Asset         string `yaml:"asset"`
	Free          string `yaml:"free"`
	Locked        string `yaml:"locked"`
	AvgEntryPrice string `yaml:"avg_entry_price,omitempty"`
}

type debugPackageFuturesWallet struct {
	InitialBalance   string `yaml:"initial_balance"`
	WalletBalance    string `yaml:"wallet_balance"`
	AvailableBalance string `yaml:"available_balance"`
	MarginMode       string `yaml:"margin_mode,omitempty"`
	PositionMode     string `yaml:"position_mode,omitempty"`
}

type debugPackageWallet struct {
	Assets  []debugPackageWalletAsset  `yaml:"assets"`
	Futures *debugPackageFuturesWallet `yaml:"futures,omitempty"`
}

type debugPackageSpotPermissionSet struct {
	Alternatives []string `json:"alternatives" yaml:"alternatives"`
}

type debugPackageSpotFilter struct {
	FilterType          string `json:"filter_type" yaml:"filter_type"`
	MinPrice            string `json:"min_price,omitempty" yaml:"min_price,omitempty"`
	MaxPrice            string `json:"max_price,omitempty" yaml:"max_price,omitempty"`
	TickSize            string `json:"tick_size,omitempty" yaml:"tick_size,omitempty"`
	MinQty              string `json:"min_qty,omitempty" yaml:"min_qty,omitempty"`
	MaxQty              string `json:"max_qty,omitempty" yaml:"max_qty,omitempty"`
	StepSize            string `json:"step_size,omitempty" yaml:"step_size,omitempty"`
	MinNotional         string `json:"min_notional,omitempty" yaml:"min_notional,omitempty"`
	MaxNotional         string `json:"max_notional,omitempty" yaml:"max_notional,omitempty"`
	ApplyToMarket       bool   `json:"apply_to_market" yaml:"apply_to_market"`
	ApplyMinToMarket    bool   `json:"apply_min_to_market" yaml:"apply_min_to_market"`
	ApplyMaxToMarket    bool   `json:"apply_max_to_market" yaml:"apply_max_to_market"`
	AvgPriceMins        int32  `json:"avg_price_mins" yaml:"avg_price_mins"`
	Limit               int64  `json:"limit" yaml:"limit"`
	MultiplierUp        string `json:"multiplier_up,omitempty" yaml:"multiplier_up,omitempty"`
	MultiplierDown      string `json:"multiplier_down,omitempty" yaml:"multiplier_down,omitempty"`
	BidMultiplierUp     string `json:"bid_multiplier_up,omitempty" yaml:"bid_multiplier_up,omitempty"`
	BidMultiplierDown   string `json:"bid_multiplier_down,omitempty" yaml:"bid_multiplier_down,omitempty"`
	AskMultiplierUp     string `json:"ask_multiplier_up,omitempty" yaml:"ask_multiplier_up,omitempty"`
	AskMultiplierDown   string `json:"ask_multiplier_down,omitempty" yaml:"ask_multiplier_down,omitempty"`
	RawJSON             string `json:"raw_json,omitempty" yaml:"raw_json,omitempty"`
	MaxPosition         string `json:"max_position,omitempty" yaml:"max_position,omitempty"`
	MaxNumOrders        int64  `json:"max_num_orders" yaml:"max_num_orders"`
	MaxNumAlgoOrders    int64  `json:"max_num_algo_orders" yaml:"max_num_algo_orders"`
	MaxNumIcebergOrders int64  `json:"max_num_iceberg_orders" yaml:"max_num_iceberg_orders"`
	MaxNumOrderAmends   int64  `json:"max_num_order_amends" yaml:"max_num_order_amends"`
	MaxNumOrderLists    int64  `json:"max_num_order_lists" yaml:"max_num_order_lists"`
}

type debugPackageSpotAssetFilter struct {
	FilterType string `json:"filter_type" yaml:"filter_type"`
	Asset      string `json:"asset" yaml:"asset"`
	Limit      string `json:"limit" yaml:"limit"`
}

type debugPackageSpotMetadata struct {
	Symbol              string                          `json:"symbol" yaml:"symbol"`
	Status              string                          `json:"status" yaml:"status"`
	BaseAsset           string                          `json:"base_asset" yaml:"base_asset"`
	QuoteAsset          string                          `json:"quote_asset" yaml:"quote_asset"`
	BaseAssetPrecision  int32                           `json:"base_asset_precision" yaml:"base_asset_precision"`
	QuoteAssetPrecision int32                           `json:"quote_asset_precision" yaml:"quote_asset_precision"`
	SpotTradingAllowed  bool                            `json:"spot_trading_allowed" yaml:"spot_trading_allowed"`
	PermissionSets      []debugPackageSpotPermissionSet `json:"permission_sets" yaml:"permission_sets"`
	OrderTypes          []string                        `json:"order_types" yaml:"order_types"`
	Filters             []debugPackageSpotFilter        `json:"filters" yaml:"filters"`
	SnapshotTimeMS      int64                           `json:"snapshot_time_ms" yaml:"snapshot_time_ms"`
}

type debugPackageSpotFact struct {
	VenueID               int64                         `json:"venue_id" yaml:"venue_id"`
	Exchange              string                        `json:"exchange" yaml:"exchange"`
	Market                string                        `json:"market" yaml:"market"`
	Symbol                string                        `json:"symbol" yaml:"symbol"`
	Metadata              debugPackageSpotMetadata      `json:"metadata" yaml:"metadata"`
	ExchangeFilters       []debugPackageSpotFilter      `json:"exchange_filters" yaml:"exchange_filters"`
	SymbolFilters         []debugPackageSpotFilter      `json:"symbol_filters" yaml:"symbol_filters"`
	AssetFilters          []debugPackageSpotAssetFilter `json:"asset_filters" yaml:"asset_filters"`
	ReferencePriceSource  string                        `json:"reference_price_source" yaml:"reference_price_source"`
	ReferencePriceDecimal string                        `json:"reference_price_decimal" yaml:"reference_price_decimal"`
	AveragePriceDecimal   string                        `json:"average_price_decimal" yaml:"average_price_decimal"`
	AveragePriceMins      int32                         `json:"average_price_mins" yaml:"average_price_mins"`
	CapturedAtMS          int64                         `json:"captured_at_ms" yaml:"captured_at_ms"`
}

type debugPackageSpotSnapshot struct {
	SchemaVersion string                 `yaml:"schema_version"`
	GeneratedAtMS int64                  `yaml:"generated_at_ms"`
	SHA256        string                 `yaml:"sha256"`
	Symbols       []debugPackageSpotFact `yaml:"symbols"`
}

type debugPackageIntegrityFile struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
	Size   int64  `yaml:"size"`
}

type debugPackageIntegrity struct {
	Algorithm string                      `yaml:"algorithm"`
	Files     []debugPackageIntegrityFile `yaml:"files"`
}

type debugPackageManifestV2 struct {
	SchemaVersion          int                       `yaml:"schema_version"`
	GeneratedAtMS          int64                     `yaml:"generated_at_ms"`
	StrategyID             int64                     `yaml:"strategy_id"`
	StartTimeMS            int64                     `yaml:"start_time_ms"`
	EndTimeMS              int64                     `yaml:"end_time_ms"`
	Inputs                 []debugPackageInput       `yaml:"inputs"`
	OrderTargets           []debugPackageOrderTarget `yaml:"order_targets"`
	SymbolMetadataSnapshot debugPackageSpotSnapshot  `yaml:"symbol_metadata_snapshot"`
	DataFiles              []debugPackageDataFile    `yaml:"data_files"`
	Wallet                 debugPackageWallet        `yaml:"wallet"`
	Integrity              debugPackageIntegrity     `yaml:"integrity"`
}

type debugPackageStreamPayload struct {
	input          debugPackageInput
	referencePrice string
	path           string
	data           []byte
}

type debugPackagePayloadSize struct {
	path string
	size int64
}

func (s *server) handlePortfolioDebugPackage(w http.ResponseWriter, r *http.Request, portfolioID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.marketData == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service is not configured (debug package unavailable)")
		return
	}
	if s.portfolios == nil {
		writeErr(w, http.StatusServiceUnavailable, "core-service is not configured (debug package unavailable)")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	if !s.ensureDebugPackagePortfolioAccess(w, r, uid, portfolioID) {
		return
	}
	var body debugPackageBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	body.RuntimeID = strings.TrimSpace(body.RuntimeID)
	if uid <= 0 || portfolioID <= 0 || body.StrategyID <= 0 || body.RuntimeID == "" {
		writeErr(w, http.StatusBadRequest, "portfolio_id, strategy_id, and runtime_id are required")
		return
	}
	if body.StartTimeMS <= 0 || body.EndTimeMS <= body.StartTimeMS {
		writeErr(w, http.StatusBadRequest, "valid start_time_ms and end_time_ms are required")
		return
	}

	strategyResp, err := s.portfolios.GetStrategy(r.Context(), &portfoliov1.GetStrategyRequest{
		StrategyId: body.StrategyID,
		UserId:     uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	strategy := strategyResp.GetStrategy()
	if strategy == nil || strings.TrimSpace(strategy.GetCode()) == "" || strategy.GetArchived() {
		writeErr(w, http.StatusFailedDependency, "active strategy source is unavailable")
		return
	}
	inputs, targets, ok := s.resolveDebugPackageDeclarations(
		r.Context(), w, uid, portfolioID, body.RuntimeID, strategy.GetCode(),
	)
	if !ok {
		return
	}
	if err := validateDebugPackageExportBounds(inputs, targets, body.StartTimeMS, body.EndTimeMS); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if debugPackageHasSpot(inputs, targets) {
		if err := s.requireDebugPackageSpotCapability(r.Context()); err != nil {
			var httpErr *debugPackageHTTPError
			if errors.As(err, &httpErr) {
				writeErr(w, httpErr.status, httpErr.msg)
			} else {
				writeErr(w, http.StatusServiceUnavailable, err.Error())
			}
			return
		}
	}
	spotRiskSnapshots, err := s.loadDebugPackageSpotRiskFacts(
		r.Context(), uid, portfolioID, body.StrategyID, inputs, targets,
	)
	if err != nil {
		var httpErr *debugPackageHTTPError
		if errors.As(err, &httpErr) {
			writeErr(w, httpErr.status, httpErr.msg)
		} else {
			writeErr(w, http.StatusFailedDependency, err.Error())
		}
		return
	}

	snapshotReq, err := debugPackageSnapshotRequest(uid, portfolioID, inputs, targets)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	snapshotResp, err := s.portfolios.GetPortfolioSnapshot(r.Context(), snapshotReq)
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, "failed to load canonical wallet and symbol metadata: "+msg)
		return
	}
	snapshot := snapshotResp.GetSnapshot()
	if snapshot == nil {
		writeErr(w, http.StatusBadGateway, "core-service returned an empty portfolio snapshot")
		return
	}

	streams := make([]debugPackageStreamPayload, 0, len(inputs))
	for _, input := range inputs {
		key := &mdv1.StreamKey{
			Exchange: input.Exchange,
			Market:   debugPackageMarketDataMarketName(input.Market),
			Kind:     input.Kind,
			Symbol:   input.Symbol,
			Interval: input.Interval,
		}
		rows, fetchErr := s.fetchDebugPackageKlines(r, key, body.StartTimeMS, body.EndTimeMS)
		if fetchErr != nil {
			writeDebugPackageFetchError(w, input, fetchErr)
			return
		}
		if len(rows) == 0 {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("requested range has no market data for stream %s", input.StreamID))
			return
		}
		referencePrice := strconv.FormatFloat(rows[0].GetClose(), 'f', 8, 64)
		parquetBytes, encodeErr := encodeDebugPackageKlinesParquet(rows)
		if encodeErr != nil {
			writeErr(w, http.StatusInternalServerError, "failed to encode debug package data")
			return
		}
		streamPath := path.Join("data", "streams", input.StreamID, input.Exchange, input.Market, input.Kind, input.Symbol, input.Interval+".parquet")
		streams = append(streams, debugPackageStreamPayload{input: input, referencePrice: referencePrice, path: streamPath, data: parquetBytes})
	}

	spotSnapshot, err := buildDebugPackageSpotSnapshot(snapshot, spotRiskSnapshots, inputs, targets, streams, body.EndTimeMS)
	if err != nil {
		writeErr(w, http.StatusFailedDependency, err.Error())
		return
	}
	wallet, err := buildDebugPackageWallet(snapshot, spotSnapshot, inputs, targets)
	if err != nil {
		writeErr(w, http.StatusFailedDependency, err.Error())
		return
	}
	archive, err := buildDebugPackageArchive(body, strategy.GetCode(), inputs, targets, spotSnapshot, wallet, streams)
	if err != nil {
		if errors.Is(err, errDebugPackagePayloadTooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to assemble debug package")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="debug-package-strategy-%d-%d.zip"`, body.StrategyID, body.EndTimeMS))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(archive)
}

func (s *server) ensureDebugPackagePortfolioAccess(w http.ResponseWriter, r *http.Request, uid int64, portfolioID int64) bool {
	resp, err := s.portfolios.GetPortfolio(r.Context(), &portfoliov1.GetPortfolioRequest{PortfolioId: portfolioID, UserId: uid})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return false
	}
	if resp.GetPortfolio() == nil {
		writeErr(w, http.StatusNotFound, "portfolio not found")
		return false
	}
	return true
}

func (s *server) requireDebugPackageSpotCapability(ctx context.Context) error {
	resp, err := s.portfolios.GetProductCapabilities(ctx, &portfoliov1.GetProductCapabilitiesRequest{})
	if err != nil {
		return &debugPackageHTTPError{status: http.StatusServiceUnavailable, msg: "Spot debug package capability discovery is unavailable"}
	}
	for _, capability := range resp.GetCapabilities() {
		if strings.TrimSpace(capability.GetName()) != debugPackageSpotCapability {
			continue
		}
		if capability.GetEffective() {
			return nil
		}
		reason := strings.TrimSpace(capability.GetReason())
		if reason == "" {
			reason = "offline Spot debug packages are disabled"
		}
		return &debugPackageHTTPError{status: http.StatusPreconditionFailed, msg: reason}
	}
	return &debugPackageHTTPError{status: http.StatusPreconditionFailed, msg: "offline Spot debug packages are disabled"}
}

func (s *server) loadDebugPackageSpotRiskFacts(
	ctx context.Context,
	uid int64,
	portfolioID int64,
	strategyID int64,
	inputs []debugPackageInput,
	targets []debugPackageOrderTarget,
) ([]*portfoliov1.SpotRiskFactSnapshot, error) {
	if !debugPackageHasSpot(inputs, targets) {
		return nil, nil
	}
	targetSet := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.Market != "spot" {
			continue
		}
		targetSet[strings.Join([]string{target.Exchange, target.Market, target.Symbol}, "/")] = struct{}{}
	}
	type requiredSpotSymbol struct {
		exchange string
		market   string
		symbol   string
	}
	requiredByKey := make(map[string]requiredSpotSymbol)
	for _, input := range inputs {
		if input.Market != "spot" {
			continue
		}
		key := strings.Join([]string{input.Exchange, input.Market, input.Symbol}, "/")
		requiredByKey[key] = requiredSpotSymbol{exchange: input.Exchange, market: input.Market, symbol: input.Symbol}
	}
	for _, target := range targets {
		if target.Market != "spot" {
			continue
		}
		key := strings.Join([]string{target.Exchange, target.Market, target.Symbol}, "/")
		requiredByKey[key] = requiredSpotSymbol{exchange: target.Exchange, market: target.Market, symbol: target.Symbol}
	}
	keys := make([]string, 0, len(requiredByKey))
	for key := range requiredByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	req := &portfoliov1.PreflightStrategySessionRequest{
		UserId: uid, PortfolioId: portfolioID, StrategyId: strategyID,
		RequiredRoutes: []*portfoliov1.RequiredRoute{{Exchange: 1, Market: 1}},
		DebugPackage:   true,
	}
	for _, key := range keys {
		item := requiredByKey[key]
		exchange, err := debugPackageExchangeCode(item.exchange)
		if err != nil {
			return nil, err
		}
		market, err := debugPackageMarketCode(item.market)
		if err != nil {
			return nil, err
		}
		_, orderTarget := targetSet[key]
		orderTypes := []string(nil)
		if orderTarget {
			orderTypes = []string{"LIMIT", "MARKET"}
		}
		req.RequiredSymbols = append(req.RequiredSymbols, &portfoliov1.RequiredSymbol{
			Exchange: exchange, Market: market, Symbol: item.symbol,
			OrderTarget: orderTarget, RequiredOrderTypes: orderTypes,
		})
	}
	resp, err := s.portfolios.PreflightStrategySession(ctx, req)
	if err != nil {
		return nil, &debugPackageHTTPError{status: http.StatusServiceUnavailable, msg: "Spot metadata/filter preflight is unavailable"}
	}
	if !resp.GetOk() && len(resp.GetIssues()) == 0 {
		return nil, &debugPackageHTTPError{status: http.StatusFailedDependency, msg: "Spot metadata/filter preflight returned a non-ok response without issues"}
	}
	for _, issue := range resp.GetIssues() {
		if strings.EqualFold(strings.TrimSpace(issue.GetCode()), "ACTIVE_SESSION_EXISTS") {
			continue
		}
		message := strings.TrimSpace(issue.GetMessage())
		if message == "" {
			message = strings.TrimSpace(issue.GetCode())
		}
		return nil, &debugPackageHTTPError{status: http.StatusFailedDependency, msg: "Spot metadata/filter preflight failed: " + message}
	}
	factsByRoute := make(map[string]*portfoliov1.SpotRiskFactSnapshot, len(resp.GetSpotRiskSnapshots()))
	for _, fact := range resp.GetSpotRiskSnapshots() {
		key := fmt.Sprintf("%d/%d/%s", fact.GetExchange(), fact.GetMarket(), strings.ToUpper(strings.TrimSpace(fact.GetSymbol())))
		if _, exists := factsByRoute[key]; exists {
			return nil, errors.New("core-service returned duplicate Spot risk facts")
		}
		factsByRoute[key] = fact
	}
	ordered := make([]*portfoliov1.SpotRiskFactSnapshot, 0, len(keys))
	for _, key := range keys {
		item := requiredByKey[key]
		exchange, _ := debugPackageExchangeCode(item.exchange)
		market, _ := debugPackageMarketCode(item.market)
		fact := factsByRoute[fmt.Sprintf("%d/%d/%s", exchange, market, item.symbol)]
		if fact == nil || fact.GetMetadata() == nil {
			return nil, fmt.Errorf("Spot metadata/filter preflight facts are missing for %s", key)
		}
		if err := validateDebugPackageSpotMetadata(convertDebugPackageSpotMetadata(fact.GetMetadata()), item.symbol); err != nil {
			return nil, fmt.Errorf("Spot metadata/filter preflight facts are incomplete for %s: %w", key, err)
		}
		ordered = append(ordered, fact)
	}
	return ordered, nil
}

func writeDebugPackageFetchError(w http.ResponseWriter, input debugPackageInput, err error) {
	prefix := fmt.Sprintf("stream %s (%s/%s/%s/%s/%s): ", input.StreamID, input.Exchange, input.Market, input.Kind, input.Symbol, input.Interval)
	if errors.Is(err, errDebugPackageIncompleteCoverage) {
		writeErr(w, http.StatusBadRequest, prefix+err.Error())
		return
	}
	var httpErr *debugPackageHTTPError
	if errors.As(err, &httpErr) {
		writeErr(w, httpErr.status, prefix+httpErr.msg)
		return
	}
	writeErr(w, http.StatusBadGateway, prefix+err.Error())
}

func (s *server) resolveDebugPackageDeclarations(
	ctx context.Context,
	w http.ResponseWriter,
	userID int64,
	portfolioID int64,
	runtimeID string,
	source string,
) ([]debugPackageInput, []debugPackageOrderTarget, bool) {
	if len([]byte(source)) > maxStrategySourceBytes {
		writeErr(w, http.StatusBadRequest, "strategy source exceeds 1 MiB limit")
		return nil, nil, false
	}
	policy, ok := s.strategyRoutePolicyForPortfolio(ctx, w, userID, portfolioID, runtimeID)
	if !ok {
		return nil, nil, false
	}
	cli, selectedRuntimeID, ok := s.strategyClient(ctx, w, userID, routeEnsure, runtimeID, policy)
	if !ok {
		return nil, nil, false
	}
	rpcCtx, cancel := context.WithTimeout(ctx, previewRunStrategyRPCTimeout)
	defer cancel()
	response, err := cli.ValidateStrategySource(rpcCtx, &strategyv1.ValidateStrategySourceRequest{
		Source:              source,
		UserId:              userID,
		RuntimeId:           selectedRuntimeID,
		IncludeDeclarations: true,
	})
	if err != nil {
		if writeRuntimeDependencyError(w, err) {
			return nil, nil, false
		}
		code, message := grpcToHTTP(err)
		writeErr(w, code, message)
		return nil, nil, false
	}
	if response == nil {
		writeErr(w, http.StatusBadGateway, "runtime returned an empty declaration response")
		return nil, nil, false
	}
	if response.GetOk() && len(response.GetIssues()) != 0 {
		writeErr(w, http.StatusBadGateway, "runtime returned an inconsistent declaration response")
		return nil, nil, false
	}
	if !response.GetOk() {
		message := "runtime rejected strategy declarations"
		for _, issue := range response.GetIssues() {
			if issue == nil {
				continue
			}
			code := strings.TrimSpace(issue.GetCode())
			detail := strings.TrimSpace(issue.GetMessage())
			if code != "" && detail != "" {
				message = code + ": " + detail
			} else if code != "" {
				message = code
			} else if detail != "" {
				message = detail
			}
			break
		}
		writeErr(w, http.StatusBadRequest, "invalid strategy declarations: "+message)
		return nil, nil, false
	}
	inputs := make([]debugPackageInput, 0, len(response.GetDeclaredInputs()))
	for _, item := range response.GetDeclaredInputs() {
		if item == nil {
			writeErr(w, http.StatusBadGateway, "runtime returned an invalid input declaration")
			return nil, nil, false
		}
		inputs = append(inputs, debugPackageInput{
			StreamID: item.GetStreamId(),
			Exchange: item.GetExchange(),
			Market:   item.GetMarket(),
			Kind:     item.GetKind(),
			Symbol:   item.GetSymbol(),
			Interval: item.GetInterval(),
		})
	}
	targets := make([]debugPackageOrderTarget, 0, len(response.GetDeclaredOrderTargets()))
	for _, item := range response.GetDeclaredOrderTargets() {
		if item == nil {
			writeErr(w, http.StatusBadGateway, "runtime returned an invalid order-target declaration")
			return nil, nil, false
		}
		targets = append(targets, debugPackageOrderTarget{
			Exchange: item.GetExchange(), Market: item.GetMarket(), Symbol: item.GetSymbol(),
		})
	}
	inputs, targets, err = normalizeDebugPackageDeclarations(inputs, targets)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "runtime returned invalid strategy declarations: "+err.Error())
		return nil, nil, false
	}
	return inputs, targets, true
}

func normalizeDebugPackageDeclarations(inputs []debugPackageInput, targets []debugPackageOrderTarget) ([]debugPackageInput, []debugPackageOrderTarget, error) {
	if len(inputs) == 0 {
		return nil, nil, errors.New("INPUTS must declare at least one stream")
	}
	streamIDs := make(map[string]struct{}, len(inputs))
	identities := make(map[string]struct{}, len(inputs))
	for i := range inputs {
		input := &inputs[i]
		input.Exchange = strings.ToLower(strings.TrimSpace(input.Exchange))
		input.Market = strings.ToLower(strings.TrimSpace(input.Market))
		input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
		if input.Kind == "" {
			input.Kind = debugPackageDefaultKind
		}
		input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
		input.Interval = strings.TrimSpace(input.Interval)
		input.StreamID = strings.TrimSpace(input.StreamID)
		if input.StreamID == "" {
			input.StreamID = strings.ToLower(strings.Join([]string{input.Exchange, input.Market, input.Kind, input.Symbol, input.Interval}, "-"))
		}
		if err := validateDebugPackageInput(*input); err != nil {
			return nil, nil, fmt.Errorf("INPUTS[%d]: %w", i, err)
		}
		if _, exists := streamIDs[input.StreamID]; exists {
			return nil, nil, fmt.Errorf("duplicate stream_id %q", input.StreamID)
		}
		streamIDs[input.StreamID] = struct{}{}
		identity := debugPackageInputIdentity(*input)
		if _, exists := identities[identity]; exists {
			return nil, nil, fmt.Errorf("duplicate full stream identity %q", identity)
		}
		identities[identity] = struct{}{}
	}
	targetKeys := make(map[string]struct{}, len(targets))
	for i := range targets {
		target := &targets[i]
		target.Exchange = strings.ToLower(strings.TrimSpace(target.Exchange))
		target.Market = strings.ToLower(strings.TrimSpace(target.Market))
		target.Symbol = strings.ToUpper(strings.TrimSpace(target.Symbol))
		if err := validateDebugPackageOrderTarget(*target); err != nil {
			return nil, nil, fmt.Errorf("ORDER_TARGETS[%d]: %w", i, err)
		}
		key := strings.Join([]string{target.Exchange, target.Market, target.Symbol}, "/")
		if _, exists := targetKeys[key]; exists {
			return nil, nil, fmt.Errorf("duplicate order target %q", key)
		}
		targetKeys[key] = struct{}{}
	}
	sort.Slice(inputs, func(i, j int) bool {
		return debugPackageInputIdentity(inputs[i]) < debugPackageInputIdentity(inputs[j])
	})
	sort.Slice(targets, func(i, j int) bool {
		return strings.Join([]string{targets[i].Exchange, targets[i].Market, targets[i].Symbol}, "/") < strings.Join([]string{targets[j].Exchange, targets[j].Market, targets[j].Symbol}, "/")
	})
	return inputs, targets, nil
}

func validateDebugPackageInput(input debugPackageInput) error {
	if input.Exchange != "binance" {
		return fmt.Errorf("unsupported exchange %q", input.Exchange)
	}
	if input.Market != "spot" && input.Market != "perpetual_futures" {
		return fmt.Errorf("unsupported market %q", input.Market)
	}
	if input.Kind != debugPackageDefaultKind {
		return fmt.Errorf("unsupported kind %q; package v2 currently supports kline only", input.Kind)
	}
	if !debugPackageSymbolPattern.MatchString(input.Symbol) {
		return fmt.Errorf("symbol %q must contain 2-30 upper-case alphanumeric characters", input.Symbol)
	}
	components := []struct{ name, value string }{
		{name: "stream_id", value: input.StreamID},
		{name: "exchange", value: input.Exchange},
		{name: "market", value: input.Market},
		{name: "kind", value: input.Kind},
		{name: "interval", value: input.Interval},
	}
	for _, component := range components {
		if len(component.value) > debugPackageMaxComponentBytes {
			return fmt.Errorf("%s exceeds %d ASCII bytes", component.name, debugPackageMaxComponentBytes)
		}
		if component.value == "" || !debugPackagePathComponentPattern.MatchString(component.value) || component.value == "." || component.value == ".." {
			return fmt.Errorf("%s %q is not a safe package path component", component.name, component.value)
		}
	}
	if _, err := debugPackageIntervalMS(input.Interval); err != nil {
		return err
	}
	return nil
}

func validateDebugPackageOrderTarget(target debugPackageOrderTarget) error {
	if target.Exchange != "binance" {
		return fmt.Errorf("unsupported exchange %q", target.Exchange)
	}
	if target.Market != "spot" && target.Market != "perpetual_futures" {
		return fmt.Errorf("unsupported market %q", target.Market)
	}
	if !debugPackageSymbolPattern.MatchString(target.Symbol) {
		return fmt.Errorf("symbol %q must contain 2-30 upper-case alphanumeric characters", target.Symbol)
	}
	return nil
}

func debugPackageInputIdentity(input debugPackageInput) string {
	return strings.Join([]string{input.StreamID, input.Exchange, input.Market, input.Kind, input.Symbol, input.Interval}, "/")
}

func debugPackageHasSpot(inputs []debugPackageInput, targets []debugPackageOrderTarget) bool {
	for _, input := range inputs {
		if input.Market == "spot" {
			return true
		}
	}
	for _, target := range targets {
		if target.Market == "spot" {
			return true
		}
	}
	return false
}

func validateDebugPackageExportBounds(inputs []debugPackageInput, targets []debugPackageOrderTarget, startMS, endMS int64) error {
	if len(inputs) > debugPackageMaxStreams {
		return fmt.Errorf("debug package declares too many streams: %d exceeds %d", len(inputs), debugPackageMaxStreams)
	}
	if len(targets) > debugPackageMaxOrderTargets {
		return fmt.Errorf("debug package declares too many order targets: %d exceeds %d", len(targets), debugPackageMaxOrderTargets)
	}
	requiredSymbols := make(map[string]struct{}, len(inputs)+len(targets))
	for _, input := range inputs {
		requiredSymbols[strings.Join([]string{input.Exchange, input.Market, input.Symbol}, "/")] = struct{}{}
	}
	for _, target := range targets {
		requiredSymbols[strings.Join([]string{target.Exchange, target.Market, target.Symbol}, "/")] = struct{}{}
	}
	if len(requiredSymbols) > debugPackageMaxRequiredSymbols {
		return fmt.Errorf("debug package declares too many required symbols: %d exceeds %d", len(requiredSymbols), debugPackageMaxRequiredSymbols)
	}
	totalBars := int64(0)
	for _, input := range inputs {
		stepMS, err := debugPackageIntervalMS(input.Interval)
		if err != nil {
			return err
		}
		if startMS%stepMS != 0 || endMS%stepMS != 0 {
			return fmt.Errorf("start_time_ms and end_time_ms must align to interval %q", input.Interval)
		}
		count := (endMS - startMS) / stepMS
		if count > debugPackageMaxTotalBars-totalBars {
			return fmt.Errorf("debug package requests too many bars: limit is %d across all streams", debugPackageMaxTotalBars)
		}
		totalBars += count
	}
	return nil
}

func debugPackageSnapshotRequest(uid, portfolioID int64, inputs []debugPackageInput, targets []debugPackageOrderTarget) (*portfoliov1.GetPortfolioSnapshotRequest, error) {
	targetSet := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		targetSet[strings.Join([]string{target.Exchange, target.Market, target.Symbol}, "/")] = struct{}{}
	}
	type required struct {
		exchange string
		market   string
		symbol   string
	}
	requiredByKey := make(map[string]required)
	for _, input := range inputs {
		key := strings.Join([]string{input.Exchange, input.Market, input.Symbol}, "/")
		requiredByKey[key] = required{exchange: input.Exchange, market: input.Market, symbol: input.Symbol}
	}
	for _, target := range targets {
		key := strings.Join([]string{target.Exchange, target.Market, target.Symbol}, "/")
		requiredByKey[key] = required{exchange: target.Exchange, market: target.Market, symbol: target.Symbol}
	}
	keys := make([]string, 0, len(requiredByKey))
	for key := range requiredByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	req := &portfoliov1.GetPortfolioSnapshotRequest{UserId: uid, PortfolioId: portfolioID}
	for _, key := range keys {
		item := requiredByKey[key]
		exchange, err := debugPackageExchangeCode(item.exchange)
		if err != nil {
			return nil, err
		}
		market, err := debugPackageMarketCode(item.market)
		if err != nil {
			return nil, err
		}
		_, orderTarget := targetSet[key]
		requiredOrderTypes := []string(nil)
		if orderTarget {
			requiredOrderTypes = []string{"LIMIT", "MARKET"}
		}
		req.RequiredSymbols = append(req.RequiredSymbols, &portfoliov1.RequiredSymbol{
			Exchange: exchange, Market: market, Symbol: item.symbol, OrderTarget: orderTarget, RequiredOrderTypes: requiredOrderTypes,
		})
	}
	return req, nil
}

func debugPackageExchangeCode(exchange string) (int32, error) {
	switch exchange {
	case "binance":
		return 1, nil
	case "okx":
		return 2, nil
	default:
		return 0, fmt.Errorf("unsupported exchange %q", exchange)
	}
}

func debugPackageMarketCode(market string) (int32, error) {
	switch market {
	case "spot":
		return 1, nil
	case "perpetual_futures":
		return 2, nil
	case "delivery_futures":
		return 3, nil
	default:
		return 0, fmt.Errorf("unsupported market %q", market)
	}
}

func debugPackageMarketDataMarketName(market string) string {
	switch market {
	case "perpetual_futures":
		return "futures"
	default:
		return market
	}
}

func buildDebugPackageSpotSnapshot(snapshot *portfoliov1.PortfolioSnapshot, riskSnapshots []*portfoliov1.SpotRiskFactSnapshot, inputs []debugPackageInput, targets []debugPackageOrderTarget, streams []debugPackageStreamPayload, fallbackGeneratedAtMS int64) (debugPackageSpotSnapshot, error) {
	type routeKey struct{ exchange, market, symbol string }
	required := make(map[routeKey]struct{})
	for _, input := range inputs {
		if input.Market == "spot" {
			required[routeKey{input.Exchange, input.Market, input.Symbol}] = struct{}{}
		}
	}
	for _, target := range targets {
		if target.Market == "spot" {
			required[routeKey{target.Exchange, target.Market, target.Symbol}] = struct{}{}
		}
	}
	keys := make([]routeKey, 0, len(required))
	for key := range required {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return strings.Join([]string{keys[i].exchange, keys[i].market, keys[i].symbol}, "/") < strings.Join([]string{keys[j].exchange, keys[j].market, keys[j].symbol}, "/")
	})
	out := debugPackageSpotSnapshot{SchemaVersion: debugPackageSpotFactsSchema, GeneratedAtMS: fallbackGeneratedAtMS, Symbols: make([]debugPackageSpotFact, 0, len(keys))}
	for _, key := range keys {
		venueID, metadata, risk := findDebugPackageSpotMetadata(snapshot, riskSnapshots, key.exchange, key.market, key.symbol)
		if metadata == nil {
			return debugPackageSpotSnapshot{}, fmt.Errorf("Spot metadata/filter snapshot is missing for %s/%s/%s", key.exchange, key.market, key.symbol)
		}
		converted := convertDebugPackageSpotMetadata(metadata)
		if err := validateDebugPackageSpotMetadata(converted, key.symbol); err != nil {
			return debugPackageSpotSnapshot{}, fmt.Errorf("Spot metadata/filter snapshot is incomplete for %s: %w", key.symbol, err)
		}
		if venueID <= 0 {
			return debugPackageSpotSnapshot{}, fmt.Errorf("Spot metadata venue is missing for %s", key.symbol)
		}
		fact := debugPackageSpotFact{
			VenueID: venueID, Exchange: key.exchange, Market: key.market, Symbol: key.symbol, Metadata: converted,
			ReferencePriceSource: "core_preflight_snapshot", CapturedAtMS: converted.SnapshotTimeMS,
		}
		if risk != nil {
			fact.ExchangeFilters = convertDebugPackageSpotFilters(risk.GetExchangeFilters())
			fact.SymbolFilters = convertDebugPackageSpotFilters(risk.GetSymbolFilters())
			fact.AssetFilters = convertDebugPackageSpotAssetFilters(risk.GetAssetFilters())
			fact.ReferencePriceDecimal = strings.TrimSpace(risk.GetReferencePriceDecimal())
			fact.AveragePriceDecimal = strings.TrimSpace(risk.GetAveragePriceDecimal())
			fact.AveragePriceMins = risk.GetAveragePriceMins()
			if risk.GetCapturedAt() != nil {
				fact.CapturedAtMS = risk.GetCapturedAt().AsTime().UTC().UnixMilli()
			}
		}
		if fact.ReferencePriceDecimal == "" {
			fact.ReferencePriceSource = "replay_event_close"
			fact.ReferencePriceDecimal = debugPackageStreamReferencePrice(streams, key.exchange, key.market, key.symbol)
		}
		if fact.ReferencePriceDecimal == "" {
			return debugPackageSpotSnapshot{}, fmt.Errorf("Spot reference-price fact is missing for %s", key.symbol)
		}
		if fact.CapturedAtMS == 0 {
			fact.CapturedAtMS = fallbackGeneratedAtMS
		}
		if fact.CapturedAtMS > out.GeneratedAtMS {
			out.GeneratedAtMS = fact.CapturedAtMS
		}
		if err := validateDebugPackageSpotFact(fact); err != nil {
			return debugPackageSpotSnapshot{}, fmt.Errorf("Spot risk facts are incomplete for %s: %w", key.symbol, err)
		}
		out.Symbols = append(out.Symbols, fact)
	}
	canonical, err := debugPackageCanonicalJSON(out.Symbols)
	if err != nil {
		return debugPackageSpotSnapshot{}, err
	}
	out.SHA256 = debugPackageSHA256(canonical)
	return out, nil
}

func findDebugPackageSpotMetadata(snapshot *portfoliov1.PortfolioSnapshot, riskSnapshots []*portfoliov1.SpotRiskFactSnapshot, exchange, market, symbol string) (int64, *portfoliov1.SpotSymbolMetadata, *portfoliov1.SpotRiskFactSnapshot) {
	exchangeCode, _ := debugPackageExchangeCode(exchange)
	marketCode, _ := debugPackageMarketCode(market)
	var found *portfoliov1.SpotSymbolMetadata
	var risk *portfoliov1.SpotRiskFactSnapshot
	var venueID int64
	for _, candidate := range riskSnapshots {
		if candidate.GetExchange() != exchangeCode || candidate.GetMarket() != marketCode || !strings.EqualFold(strings.TrimSpace(candidate.GetSymbol()), symbol) {
			continue
		}
		risk = candidate
		venueID = candidate.GetVenueId()
		if candidate.GetMetadata() != nil {
			found = candidate.GetMetadata()
		}
		break
	}
	for _, venue := range snapshot.GetVenues() {
		if venue.GetExchange() != exchangeCode || venue.GetMarket() != marketCode {
			continue
		}
		for _, metadata := range venue.GetSpotSymbols() {
			if strings.EqualFold(strings.TrimSpace(metadata.GetSymbol()), symbol) {
				if found == nil {
					found = metadata
				}
				if venueID == 0 {
					venueID = venue.GetVenueId()
				}
				break
			}
		}
		candidate := venue.GetSpotRiskSnapshot()
		if risk == nil && candidate != nil && strings.EqualFold(strings.TrimSpace(candidate.GetSymbol()), symbol) {
			risk = candidate
			if candidate.GetVenueId() > 0 {
				venueID = candidate.GetVenueId()
			} else if venueID == 0 {
				venueID = venue.GetVenueId()
			}
			if found == nil && candidate.GetMetadata() != nil {
				found = candidate.GetMetadata()
			}
		}
	}
	return venueID, found, risk
}

func convertDebugPackageSpotMetadata(value *portfoliov1.SpotSymbolMetadata) debugPackageSpotMetadata {
	out := debugPackageSpotMetadata{
		Symbol: strings.ToUpper(strings.TrimSpace(value.GetSymbol())), Status: strings.TrimSpace(value.GetStatus()),
		BaseAsset: strings.ToUpper(strings.TrimSpace(value.GetBaseAsset())), QuoteAsset: strings.ToUpper(strings.TrimSpace(value.GetQuoteAsset())),
		BaseAssetPrecision: value.GetBaseAssetPrecision(), QuoteAssetPrecision: value.GetQuoteAssetPrecision(),
		SpotTradingAllowed: value.GetSpotTradingAllowed(), SnapshotTimeMS: value.GetSnapshotTimeMs(),
		OrderTypes: append([]string(nil), value.GetOrderTypes()...), Filters: convertDebugPackageSpotFilters(value.GetFilters()),
	}
	sort.Strings(out.OrderTypes)
	for _, set := range value.GetPermissionSets() {
		alternatives := append([]string(nil), set.GetAlternatives()...)
		sort.Strings(alternatives)
		out.PermissionSets = append(out.PermissionSets, debugPackageSpotPermissionSet{Alternatives: alternatives})
	}
	sort.Slice(out.PermissionSets, func(i, j int) bool {
		return strings.Join(out.PermissionSets[i].Alternatives, "\x00") < strings.Join(out.PermissionSets[j].Alternatives, "\x00")
	})
	return out
}

func convertDebugPackageSpotFilters(values []*portfoliov1.SpotSymbolFilter) []debugPackageSpotFilter {
	out := make([]debugPackageSpotFilter, 0, len(values))
	for _, value := range values {
		out = append(out, debugPackageSpotFilter{
			FilterType: value.GetFilterType(), MinPrice: value.GetMinPrice(), MaxPrice: value.GetMaxPrice(), TickSize: value.GetTickSize(),
			MinQty: value.GetMinQty(), MaxQty: value.GetMaxQty(), StepSize: value.GetStepSize(), MinNotional: value.GetMinNotional(), MaxNotional: value.GetMaxNotional(),
			ApplyToMarket: value.GetApplyToMarket(), ApplyMinToMarket: value.GetApplyMinToMarket(), ApplyMaxToMarket: value.GetApplyMaxToMarket(),
			AvgPriceMins: value.GetAvgPriceMins(), Limit: value.GetLimit(), MultiplierUp: value.GetMultiplierUp(), MultiplierDown: value.GetMultiplierDown(),
			BidMultiplierUp: value.GetBidMultiplierUp(), BidMultiplierDown: value.GetBidMultiplierDown(), AskMultiplierUp: value.GetAskMultiplierUp(), AskMultiplierDown: value.GetAskMultiplierDown(),
			RawJSON: value.GetRawJson(), MaxPosition: value.GetMaxPosition(), MaxNumOrders: value.GetMaxNumOrders(), MaxNumAlgoOrders: value.GetMaxNumAlgoOrders(),
			MaxNumIcebergOrders: value.GetMaxNumIcebergOrders(), MaxNumOrderAmends: value.GetMaxNumOrderAmends(), MaxNumOrderLists: value.GetMaxNumOrderLists(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left, _ := json.Marshal(out[i])
		right, _ := json.Marshal(out[j])
		return string(left) < string(right)
	})
	return out
}

func convertDebugPackageSpotAssetFilters(values []*portfoliov1.SpotAssetFilter) []debugPackageSpotAssetFilter {
	out := make([]debugPackageSpotAssetFilter, 0, len(values))
	for _, value := range values {
		out = append(out, debugPackageSpotAssetFilter{FilterType: value.GetFilterType(), Asset: strings.ToUpper(strings.TrimSpace(value.GetAsset())), Limit: value.GetLimit()})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join([]string{out[i].FilterType, out[i].Asset, out[i].Limit}, "\x00") < strings.Join([]string{out[j].FilterType, out[j].Asset, out[j].Limit}, "\x00")
	})
	return out
}

func validateDebugPackageSpotMetadata(metadata debugPackageSpotMetadata, expectedSymbol string) error {
	if metadata.Symbol != strings.ToUpper(strings.TrimSpace(expectedSymbol)) {
		return errors.New("metadata symbol does not match the declared route")
	}
	if !strings.EqualFold(strings.TrimSpace(metadata.Status), "TRADING") || !metadata.SpotTradingAllowed {
		return errors.New("symbol is not enabled for Spot trading")
	}
	if metadata.BaseAsset == "" || !strings.EqualFold(metadata.QuoteAsset, "USDT") {
		return errors.New("base/USDT quote metadata is missing")
	}
	if metadata.Symbol != metadata.BaseAsset+metadata.QuoteAsset {
		return errors.New("metadata base and quote assets do not compose the symbol")
	}
	if metadata.BaseAssetPrecision < 0 || metadata.QuoteAssetPrecision < 0 {
		return errors.New("asset precision is invalid")
	}
	if metadata.SnapshotTimeMS < 0 {
		return errors.New("metadata snapshot time is invalid")
	}
	if len(metadata.PermissionSets) == 0 || len(metadata.OrderTypes) == 0 || len(metadata.Filters) == 0 {
		return errors.New("permission, order type, or filter facts are missing")
	}
	for _, permissionSet := range metadata.PermissionSets {
		if len(permissionSet.Alternatives) == 0 {
			return errors.New("permission set is empty")
		}
		for _, alternative := range permissionSet.Alternatives {
			if strings.TrimSpace(alternative) == "" {
				return errors.New("permission alternative is empty")
			}
		}
	}
	for _, orderType := range metadata.OrderTypes {
		if strings.TrimSpace(orderType) == "" {
			return errors.New("order type is empty")
		}
	}
	for _, filter := range metadata.Filters {
		if strings.TrimSpace(filter.FilterType) == "" {
			return errors.New("filter type is empty")
		}
	}
	return nil
}

func validateDebugPackageSpotFact(fact debugPackageSpotFact) error {
	if fact.VenueID <= 0 || fact.CapturedAtMS <= 0 {
		return errors.New("venue or capture time is missing")
	}
	if fact.ReferencePriceSource != "core_preflight_snapshot" && fact.ReferencePriceSource != "replay_event_close" {
		return errors.New("reference price source is unsupported")
	}
	if !debugPackagePositiveDecimal(fact.ReferencePriceDecimal) {
		return errors.New("reference price must be a positive exact decimal")
	}
	if fact.AveragePriceDecimal != "" && !debugPackagePositiveDecimal(fact.AveragePriceDecimal) {
		return errors.New("average price must be a positive exact decimal")
	}
	if fact.AveragePriceMins < 0 {
		return errors.New("average price window is invalid")
	}
	for _, filters := range [][]debugPackageSpotFilter{fact.ExchangeFilters, fact.SymbolFilters} {
		for _, filter := range filters {
			if strings.TrimSpace(filter.FilterType) == "" {
				return errors.New("filter type is empty")
			}
		}
	}
	for _, filter := range fact.AssetFilters {
		if strings.TrimSpace(filter.FilterType) == "" {
			return errors.New("filter type is empty")
		}
	}
	return nil
}

func debugPackagePositiveDecimal(value string) bool {
	value = strings.TrimSpace(value)
	if !debugPackageDecimalPattern.MatchString(value) {
		return false
	}
	parsed, ok := new(big.Rat).SetString(value)
	return ok && parsed.Sign() > 0
}

func debugPackageStreamReferencePrice(streams []debugPackageStreamPayload, exchange, market, symbol string) string {
	for _, stream := range streams {
		if stream.input.Exchange != exchange || stream.input.Market != market || stream.input.Symbol != symbol || stream.referencePrice == "" {
			continue
		}
		return stream.referencePrice
	}
	return ""
}

func buildDebugPackageWallet(snapshot *portfoliov1.PortfolioSnapshot, spot debugPackageSpotSnapshot, inputs []debugPackageInput, targets []debugPackageOrderTarget) (debugPackageWallet, error) {
	walletState := snapshot.GetWallet()
	if walletState == nil {
		return debugPackageWallet{}, errors.New("canonical portfolio wallet snapshot is missing")
	}
	needsSpot := debugPackageHasSpot(inputs, targets)
	needsFutures := false
	for _, input := range inputs {
		if input.Market == "perpetual_futures" {
			needsFutures = true
		}
	}
	for _, target := range targets {
		if target.Market == "perpetual_futures" {
			needsFutures = true
		}
	}
	if needsSpot && walletState.GetSpot() == nil {
		return debugPackageWallet{}, errors.New("canonical Spot wallet snapshot is missing")
	}
	if needsFutures && walletState.GetFutures() == nil {
		return debugPackageWallet{}, errors.New("canonical Futures wallet snapshot is missing")
	}
	assets := make(map[string]debugPackageWalletAsset)
	if spotWallet := walletState.GetSpot(); spotWallet != nil {
		for _, value := range spotWallet.GetAssets() {
			asset := strings.ToUpper(strings.TrimSpace(value.GetAsset()))
			if asset == "" {
				return debugPackageWallet{}, errors.New("canonical Spot wallet contains an asset without an asset code")
			}
			if _, exists := assets[asset]; exists {
				return debugPackageWallet{}, fmt.Errorf("canonical Spot wallet contains duplicate asset %s", asset)
			}
			assets[asset] = debugPackageWalletAsset{
				Asset: asset, Free: debugPackageDecimal(value.GetFreeDecimal(), value.GetFree()), Locked: debugPackageDecimal(value.GetLockedDecimal(), value.GetLocked()),
				AvgEntryPrice: debugPackageOptionalDecimal(value.GetAvgEntryPrice()),
			}
		}
	}
	for _, fact := range spot.Symbols {
		for _, asset := range []string{fact.Metadata.BaseAsset, fact.Metadata.QuoteAsset} {
			if _, exists := assets[asset]; !exists {
				assets[asset] = debugPackageWalletAsset{Asset: asset, Free: debugPackageDefaultDecimalValue, Locked: debugPackageDefaultDecimalValue}
			}
		}
	}
	out := debugPackageWallet{}
	if futures := walletState.GetFutures(); futures != nil {
		out.Futures = &debugPackageFuturesWallet{
			InitialBalance: debugPackageDecimal("", futures.GetInitialBalance()), WalletBalance: debugPackageDecimal("", futures.GetWalletBalance()),
			AvailableBalance: debugPackageDecimal("", futures.GetAvailableBalance()), MarginMode: strings.TrimSpace(futures.GetMarginMode()), PositionMode: strings.TrimSpace(futures.GetPositionMode()),
		}
		if len(assets) == 0 {
			assets[debugPackageDefaultWalletAsset] = debugPackageWalletAsset{Asset: debugPackageDefaultWalletAsset, Free: out.Futures.AvailableBalance, Locked: debugPackageDefaultDecimalValue}
		}
	}
	if len(assets) == 0 {
		return debugPackageWallet{}, errors.New("canonical portfolio wallet has no assets or Futures balance")
	}
	assetNames := make([]string, 0, len(assets))
	for asset := range assets {
		assetNames = append(assetNames, asset)
	}
	sort.Strings(assetNames)
	for _, asset := range assetNames {
		out.Assets = append(out.Assets, assets[asset])
	}
	if err := validateDebugPackageWallet(out, spot); err != nil {
		return debugPackageWallet{}, err
	}
	return out, nil
}

func validateDebugPackageWallet(wallet debugPackageWallet, spot debugPackageSpotSnapshot) error {
	tradingSymbols := make(map[string]struct{}, len(spot.Symbols))
	for _, fact := range spot.Symbols {
		tradingSymbols[fact.Symbol] = struct{}{}
	}
	for _, asset := range wallet.Assets {
		if len(asset.Asset) > debugPackageMaxComponentBytes {
			return fmt.Errorf("canonical wallet asset code exceeds %d ASCII bytes", debugPackageMaxComponentBytes)
		}
		if asset.Asset == "." || asset.Asset == ".." || !debugPackagePathComponentPattern.MatchString(asset.Asset) {
			return fmt.Errorf("canonical wallet asset code %q is invalid", asset.Asset)
		}
		if _, exists := tradingSymbols[asset.Asset]; exists {
			return fmt.Errorf("canonical wallet must store assets, not the trading symbol %s", asset.Asset)
		}
		for field, value := range map[string]string{
			"free": asset.Free, "locked": asset.Locked,
		} {
			if !debugPackageNonNegativeDecimal(value) {
				return fmt.Errorf("canonical wallet asset %s %s must be a non-negative exact decimal", asset.Asset, field)
			}
		}
		if asset.AvgEntryPrice != "" && !debugPackageNonNegativeDecimal(asset.AvgEntryPrice) {
			return fmt.Errorf("canonical wallet asset %s average entry price must be a non-negative exact decimal", asset.Asset)
		}
	}
	if wallet.Futures != nil {
		for field, value := range map[string]string{
			"initial balance":   wallet.Futures.InitialBalance,
			"wallet balance":    wallet.Futures.WalletBalance,
			"available balance": wallet.Futures.AvailableBalance,
		} {
			if !debugPackageNonNegativeDecimal(value) {
				return fmt.Errorf("canonical Futures %s must be a non-negative exact decimal", field)
			}
		}
	}
	return nil
}

func debugPackageNonNegativeDecimal(value string) bool {
	value = strings.TrimSpace(value)
	if !debugPackageDecimalPattern.MatchString(value) {
		return false
	}
	parsed, ok := new(big.Rat).SetString(value)
	return ok && parsed.Sign() >= 0
}

func debugPackageDecimal(exact string, fallback float64) string {
	if value := strings.TrimSpace(exact); value != "" {
		return value
	}
	return strconv.FormatFloat(fallback, 'f', 8, 64)
}

func debugPackageOptionalDecimal(value float64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatFloat(value, 'f', 8, 64)
}

func buildDebugPackageArchive(body debugPackageBody, strategyCode string, inputs []debugPackageInput, targets []debugPackageOrderTarget, spot debugPackageSpotSnapshot, wallet debugPackageWallet, streams []debugPackageStreamPayload) ([]byte, error) {
	if err := validateDebugPackageStreamFiles(inputs, streams); err != nil {
		return nil, err
	}
	payloads := make(map[string][]byte, len(streams)+3)
	for _, stream := range streams {
		if _, exists := payloads[stream.path]; exists {
			return nil, fmt.Errorf("duplicate archive path %q", stream.path)
		}
		payloads[stream.path] = stream.data
	}
	walletYAML, err := yaml.Marshal(wallet)
	if err != nil {
		return nil, err
	}
	payloads["wallet.yaml"] = walletYAML
	payloads["strategy.py.template"] = []byte(strategyCode)
	payloads["README.md"] = []byte("Import offline with: hushine-debug import ./debug-package.zip\nNo network access is required for package-v2 replay.\n")

	dataFiles := make([]debugPackageDataFile, 0, len(streams))
	for _, stream := range streams {
		dataFiles = append(dataFiles, debugPackageDataFile{
			StreamID: stream.input.StreamID,
			Route:    strings.Join([]string{stream.input.Exchange, stream.input.Market, stream.input.Kind, stream.input.Symbol, stream.input.Interval}, "/"),
			Path:     stream.path,
		})
	}
	sort.Slice(dataFiles, func(i, j int) bool { return dataFiles[i].Path < dataFiles[j].Path })

	payloadPaths := make([]string, 0, len(payloads))
	for payloadPath := range payloads {
		payloadPaths = append(payloadPaths, payloadPath)
	}
	sort.Strings(payloadPaths)
	integrityFiles := make([]debugPackageIntegrityFile, 0, len(payloadPaths))
	for _, payloadPath := range payloadPaths {
		payload := payloads[payloadPath]
		integrityFiles = append(integrityFiles, debugPackageIntegrityFile{Path: payloadPath, SHA256: debugPackageSHA256(payload), Size: int64(len(payload))})
	}
	manifest := debugPackageManifestV2{
		SchemaVersion: debugPackageSchemaVersion, GeneratedAtMS: body.EndTimeMS, StrategyID: body.StrategyID,
		StartTimeMS: body.StartTimeMS, EndTimeMS: body.EndTimeMS, Inputs: inputs, OrderTargets: targets,
		SymbolMetadataSnapshot: spot, DataFiles: dataFiles, Wallet: wallet,
		Integrity: debugPackageIntegrity{Algorithm: debugPackageIntegrityAlgorithm, Files: integrityFiles},
	}
	manifestYAML, err := yaml.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	payloadSizes := make([]debugPackagePayloadSize, 0, len(payloads)+1)
	payloadSizes = append(payloadSizes, debugPackagePayloadSize{path: "manifest.yaml", size: int64(len(manifestYAML))})
	for _, payloadPath := range payloadPaths {
		payload := payloads[payloadPath]
		payloadSizes = append(payloadSizes, debugPackagePayloadSize{path: payloadPath, size: int64(len(payload))})
	}
	if err := validateDebugPackagePayloadSizes(payloadSizes); err != nil {
		return nil, err
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	if err := addZipFile(zw, "manifest.yaml", manifestYAML); err != nil {
		_ = zw.Close()
		return nil, err
	}
	for _, payloadPath := range payloadPaths {
		if err := addZipFile(zw, payloadPath, payloads[payloadPath]); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return archive.Bytes(), nil
}

func validateDebugPackagePayloadSizes(payloads []debugPackagePayloadSize) error {
	total := int64(0)
	for _, payload := range payloads {
		if payload.size < 0 {
			return fmt.Errorf("debug package entry %q has an invalid negative size", payload.path)
		}
		limit := debugPackageMaxOtherEntryBytes
		switch {
		case payload.path == "manifest.yaml":
			limit = debugPackageMaxManifestBytes
		case payload.path == "strategy.py.template":
			limit = debugPackageMaxStrategyBytes
		case payload.path == "wallet.yaml":
			limit = debugPackageMaxWalletBytes
		case strings.HasSuffix(payload.path, ".parquet"):
			limit = debugPackageMaxParquetBytes
		}
		if payload.size > limit {
			return fmt.Errorf(
				"%w: entry %s is %d bytes; limit is %d",
				errDebugPackagePayloadTooLarge,
				payload.path,
				payload.size,
				limit,
			)
		}
		if payload.size > debugPackageMaxTotalBytes-total {
			return fmt.Errorf(
				"%w: total uncompressed size exceeds %d bytes",
				errDebugPackagePayloadTooLarge,
				debugPackageMaxTotalBytes,
			)
		}
		total += payload.size
	}
	return nil
}

func validateDebugPackageStreamFiles(inputs []debugPackageInput, streams []debugPackageStreamPayload) error {
	if len(inputs) != len(streams) {
		return fmt.Errorf("declaration-to-file mismatch: %d declared streams but %d files", len(inputs), len(streams))
	}
	expected := make(map[string]debugPackageInput, len(inputs))
	for _, input := range inputs {
		identity := debugPackageInputIdentity(input)
		if _, exists := expected[identity]; exists {
			return fmt.Errorf("declaration-to-file mismatch: duplicate declared identity %q", identity)
		}
		expected[identity] = input
	}
	seen := make(map[string]struct{}, len(streams))
	for _, stream := range streams {
		identity := debugPackageInputIdentity(stream.input)
		declared, exists := expected[identity]
		if !exists || declared != stream.input {
			return fmt.Errorf("declaration-to-file mismatch: stream identity %q is undeclared", identity)
		}
		if _, exists := seen[identity]; exists {
			return fmt.Errorf("declaration-to-file mismatch: duplicate file for %q", identity)
		}
		seen[identity] = struct{}{}
		expectedPath := path.Join("data", "streams", stream.input.StreamID, stream.input.Exchange, stream.input.Market, stream.input.Kind, stream.input.Symbol, stream.input.Interval+".parquet")
		if stream.path != expectedPath {
			return fmt.Errorf("declaration-to-file mismatch: stream %q path is %q, want %q", identity, stream.path, expectedPath)
		}
	}
	return nil
}

func debugPackageSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func debugPackageCanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var canonicalValue any
	if err := decoder.Decode(&canonicalValue); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(canonicalValue); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

func addZipFile(zw *zip.Writer, name string, data []byte) error {
	if !debugPackageSafeArchivePath(name) {
		return fmt.Errorf("unsafe archive path %q", name)
	}
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o644)
	header.SetModTime(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC))
	f, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

func debugPackageSafeArchivePath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || path.Clean(name) != name {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func (s *server) fetchDebugPackageKlines(r *http.Request, key *mdv1.StreamKey, startMS int64, endMS int64) ([]*mdv1.MarketDataKline, error) {
	validate, err := s.marketData.ValidateMarketDataCoverage(r.Context(), &mdv1.ValidateMarketDataCoverageRequest{
		Key: key, StartAt: timestamppb.New(time.UnixMilli(startMS).UTC()), EndAt: timestamppb.New(time.UnixMilli(endMS).UTC()),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		return nil, &debugPackageHTTPError{status: code, msg: "coverage validation failed: " + msg}
	}
	if !debugPackageStreamKeyEqual(validate.GetKey(), key) {
		return nil, errors.New("market data coverage response key does not match the requested stream")
	}
	if !validate.GetOk() {
		return nil, errDebugPackageIncompleteCoverage
	}

	stepMS, err := debugPackageIntervalMS(key.GetInterval())
	if err != nil {
		return nil, &debugPackageHTTPError{status: http.StatusBadRequest, msg: err.Error()}
	}
	if startMS < 0 || endMS <= startMS {
		return nil, &debugPackageHTTPError{status: http.StatusBadRequest, msg: "invalid market data range"}
	}
	expectedCount := (endMS-startMS-1)/stepMS + 1
	if expectedCount <= 0 || expectedCount > debugPackageMaxTotalBars {
		return nil, &debugPackageHTTPError{status: http.StatusBadRequest, msg: "market data range exceeds debug package bounds"}
	}
	rows := make([]*mdv1.MarketDataKline, 0, int(expectedCount))
	cursor := startMS
	for cursor < endMS {
		resp, err := s.marketData.QueryMarketDataKlines(r.Context(), &mdv1.QueryMarketDataKlinesRequest{
			Key: key, StartAt: timestamppb.New(time.UnixMilli(cursor).UTC()), EndAt: timestamppb.New(time.UnixMilli(endMS).UTC()), Limit: debugPackageKlineLimit,
		})
		if err != nil {
			code, msg := grpcToHTTP(err)
			return nil, &debugPackageHTTPError{status: code, msg: "failed to load requested market data: " + msg}
		}
		if !debugPackageStreamKeyEqual(resp.GetKey(), key) {
			return nil, errors.New("market data kline response key does not match the requested stream")
		}
		batch := resp.GetRows()
		if len(batch) == 0 {
			break
		}
		if int64(len(batch)) > expectedCount-int64(len(rows)) {
			return nil, errDebugPackageIncompleteCoverage
		}
		rows = append(rows, batch...)
		last := batch[len(batch)-1]
		next := cursorFromKline(last, stepMS)
		if next <= cursor {
			return nil, fmt.Errorf("market data query did not advance cursor")
		}
		cursor = next
		if !resp.GetTruncated() {
			break
		}
	}
	if err := validateDebugPackageKlineRows(rows, startMS, endMS, stepMS); err != nil {
		return nil, err
	}
	return rows, nil
}

func debugPackageStreamKeyEqual(left, right *mdv1.StreamKey) bool {
	if left == nil || right == nil {
		return false
	}
	return left.GetExchange() == right.GetExchange() &&
		left.GetMarket() == right.GetMarket() &&
		left.GetKind() == right.GetKind() &&
		left.GetSymbol() == right.GetSymbol() &&
		left.GetInterval() == right.GetInterval()
}

func validateDebugPackageKlineRows(rows []*mdv1.MarketDataKline, startMS int64, endMS int64, stepMS int64) error {
	if startMS < 0 || endMS <= startMS || stepMS <= 0 {
		return errDebugPackageIncompleteCoverage
	}
	expectedCount := (endMS-startMS-1)/stepMS + 1
	if int64(len(rows)) != expectedCount {
		return errDebugPackageIncompleteCoverage
	}
	for index, row := range rows {
		if row.GetOpenTime() == nil {
			return errDebugPackageIncompleteCoverage
		}
		openMS := row.GetOpenTime().AsTime().UTC().UnixMilli()
		expectedMS := startMS + int64(index)*stepMS
		if openMS != expectedMS {
			return errDebugPackageIncompleteCoverage
		}
		open, high, low, close, volume := row.GetOpen(), row.GetHigh(), row.GetLow(), row.GetClose(), row.GetVolume()
		if !debugPackageFinite(open) || !debugPackageFinite(high) || !debugPackageFinite(low) || !debugPackageFinite(close) ||
			open <= 0 || high <= 0 || low <= 0 || close <= 0 ||
			high < low || high < open || high < close || low > open || low > close ||
			!debugPackageFinite(volume) || volume < 0 {
			return errors.New("market data contains invalid kline values")
		}
	}
	return nil
}

func debugPackageFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func cursorFromKline(row *mdv1.MarketDataKline, fallbackStepMS int64) int64 {
	if row.GetCloseTime() != nil {
		return row.GetCloseTime().AsTime().UTC().UnixMilli()
	}
	if row.GetOpenTime() != nil {
		return row.GetOpenTime().AsTime().UTC().UnixMilli() + fallbackStepMS
	}
	return 0
}

func debugPackageIntervalMS(interval string) (int64, error) {
	interval = strings.TrimSpace(interval)
	switch interval {
	case "1s":
		return int64(time.Second / time.Millisecond), nil
	case "5s":
		return int64(5 * time.Second / time.Millisecond), nil
	case "10s":
		return int64(10 * time.Second / time.Millisecond), nil
	case "30s":
		return int64(30 * time.Second / time.Millisecond), nil
	case "1m":
		return int64(time.Minute / time.Millisecond), nil
	case "3m":
		return int64(3 * time.Minute / time.Millisecond), nil
	case "5m":
		return int64(5 * time.Minute / time.Millisecond), nil
	case "15m":
		return int64(15 * time.Minute / time.Millisecond), nil
	case "30m":
		return int64(30 * time.Minute / time.Millisecond), nil
	case "1h":
		return int64(time.Hour / time.Millisecond), nil
	case "2h":
		return int64(2 * time.Hour / time.Millisecond), nil
	case "4h":
		return int64(4 * time.Hour / time.Millisecond), nil
	case "6h":
		return int64(6 * time.Hour / time.Millisecond), nil
	case "8h":
		return int64(8 * time.Hour / time.Millisecond), nil
	case "12h":
		return int64(12 * time.Hour / time.Millisecond), nil
	case "1d":
		return int64(24 * time.Hour / time.Millisecond), nil
	case "3d":
		return int64(3 * 24 * time.Hour / time.Millisecond), nil
	default:
		return 0, fmt.Errorf("unsupported interval %q", interval)
	}
}
