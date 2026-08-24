package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type coveragePreviewRequest struct {
	StartTimeMS int64  `json:"start_time_ms"`
	EndTimeMS   int64  `json:"end_time_ms"`
	RuntimeID   string `json:"runtime_id"`
}

type coveragePreviewResponse struct {
	Complete        bool                       `json:"complete"`
	CanAutoDownload bool                       `json:"can_auto_download"`
	Inputs          []coveragePreviewInputJSON `json:"inputs"`
}

type coveragePreviewInputJSON struct {
	Key                   streamKeyJSON             `json:"key"`
	Complete              bool                      `json:"complete"`
	ExpectedCount         int64                     `json:"expected_count"`
	CoveredCount          int64                     `json:"covered_count"`
	MissingSegments       []marketDataTimeRangeJSON `json:"missing_segments"`
	NonDownloadableReason string                    `json:"non_downloadable_reason,omitempty"`
}

func (s *server) handleCoveragePreview(w http.ResponseWriter, r *http.Request, portfolioID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.marketData == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service is not configured (market-data control plane unavailable)")
		return
	}
	body, ok := decodeCoveragePreviewRequest(w, r)
	if !ok {
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	rpcCtx, cancel := context.WithTimeout(r.Context(), previewRunStrategyRPCTimeout)
	defer cancel()

	resp, ok := s.previewStrategyForCoverage(rpcCtx, w, r, uid, portfolioID, body)
	if !ok {
		return
	}
	if strings.ToLower(strings.TrimSpace(resp.GetProfile())) != "backtest" {
		writeErr(w, http.StatusBadRequest, "coverage preview only supports backtest profile")
		return
	}

	out, ok := s.buildCoveragePreview(rpcCtx, w, resp.GetDeclaredInputs(), body)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func decodeCoveragePreviewRequest(w http.ResponseWriter, r *http.Request) (coveragePreviewRequest, bool) {
	var body coveragePreviewRequest
	if err := decodeStrategyRequestBody(r.Body, &body, false); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return coveragePreviewRequest{}, false
	}
	if body.StartTimeMS <= 0 || body.EndTimeMS <= 0 {
		writeErr(w, http.StatusBadRequest, "start_time_ms and end_time_ms are required")
		return coveragePreviewRequest{}, false
	}
	if body.EndTimeMS <= body.StartTimeMS {
		writeErr(w, http.StatusBadRequest, "end_time_ms must be greater than start_time_ms")
		return coveragePreviewRequest{}, false
	}
	return body, true
}

func (s *server) previewStrategyForCoverage(ctx context.Context, w http.ResponseWriter, r *http.Request, uid int64, portfolioID int64, body coveragePreviewRequest) (*strategyv1.PreviewRunStrategyResponse, bool) {
	runtimeID := strings.TrimSpace(body.RuntimeID)
	if runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime selection required")
		return nil, false
	}
	policy, ok := s.strategyRoutePolicyForPortfolio(ctx, w, uid, portfolioID, runtimeID)
	if !ok {
		return nil, false
	}
	cli, _, ok := s.strategyClient(ctx, w, uid, routeEnsure, runtimeID, policy)
	if !ok {
		return nil, false
	}
	resp, err := cli.PreviewRunStrategy(ctx, &strategyv1.PreviewRunStrategyRequest{
		PortfolioId: portfolioID,
		StartTimeMs: body.StartTimeMS,
		EndTimeMs:   body.EndTimeMS,
		UserId:      uid,
		RuntimeId:   runtimeID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return nil, false
	}
	return resp, true
}

func (s *server) buildCoveragePreview(ctx context.Context, w http.ResponseWriter, declared []*strategyv1.LiveStreamBinding, body coveragePreviewRequest) (coveragePreviewResponse, bool) {
	out := coveragePreviewResponse{
		Complete:        true,
		CanAutoDownload: true,
		Inputs:          make([]coveragePreviewInputJSON, 0, len(declared)),
	}
	for _, binding := range declared {
		key := marketDataKeyFromLiveBinding(binding)
		displayKey := streamKeyJSON{
			Exchange: key.GetExchange(),
			Market:   marketDataMarketToStrategyMarket(binding.GetMarket()),
			Kind:     key.GetKind(),
			Symbol:   key.GetSymbol(),
			Interval: key.GetInterval(),
		}
		resp, err := s.marketData.QueryMarketDataCoverage(ctx, &mdv1.QueryMarketDataCoverageRequest{
			Key:     key,
			StartAt: timestamppb.New(time.UnixMilli(body.StartTimeMS).UTC()),
			EndAt:   timestamppb.New(time.UnixMilli(body.EndTimeMS).UTC()),
		})
		if err != nil {
			code, msg := grpcToHTTP(err)
			writeErr(w, code, msg)
			return out, false
		}
		item := coveragePreviewInputJSON{
			Key:                   displayKey,
			Complete:              resp.GetComplete(),
			ExpectedCount:         resp.GetExpectedCount(),
			CoveredCount:          resp.GetCoveredCount(),
			MissingSegments:       make([]marketDataTimeRangeJSON, 0, len(resp.GetMissingSegments())),
			NonDownloadableReason: resp.GetNonDownloadableReason(),
		}
		for _, missing := range resp.GetMissingSegments() {
			item.MissingSegments = append(item.MissingSegments, timeRangeToJSON(missing))
		}
		if !item.Complete {
			out.Complete = false
		}
		if item.NonDownloadableReason != "" {
			out.CanAutoDownload = false
		}
		out.Inputs = append(out.Inputs, item)
	}
	if out.Complete {
		out.CanAutoDownload = false
	}
	return out, true
}

func marketDataKeyFromLiveBinding(binding *strategyv1.LiveStreamBinding) *mdv1.StreamKey {
	if binding == nil {
		return &mdv1.StreamKey{Exchange: "binance", Kind: "kline"}
	}
	exchange := strings.TrimSpace(binding.GetExchange())
	if exchange == "" {
		exchange = "binance"
	}
	kind := strings.TrimSpace(binding.GetKind())
	if kind == "" {
		kind = "kline"
	}
	return &mdv1.StreamKey{
		Exchange: exchange,
		Market:   strategyMarketToMarketDataMarket(binding.GetMarket()),
		Kind:     kind,
		Symbol:   binding.GetSymbol(),
		Interval: binding.GetInterval(),
	}
}
