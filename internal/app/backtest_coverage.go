package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type coveragePreviewRequest struct {
	StrategyPath string `json:"strategy_path"`
	StartTimeMS  int64  `json:"start_time_ms"`
	EndTimeMS    int64  `json:"end_time_ms"`
	RuntimeID    string `json:"runtime_id"`
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

func (s *server) handleCoveragePreview(w http.ResponseWriter, r *http.Request, accountID int64) {
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
	resp, ok := s.previewStrategyForCoverage(w, r, uid, accountID, body)
	if !ok {
		return
	}
	if strings.ToLower(strings.TrimSpace(resp.GetProfile())) != "backtest" {
		writeErr(w, http.StatusBadRequest, "coverage preview only supports backtest profile")
		return
	}

	out, ok := s.buildCoveragePreview(w, r, resp.GetDeclaredInputs(), body)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func decodeCoveragePreviewRequest(w http.ResponseWriter, r *http.Request) (coveragePreviewRequest, bool) {
	var body coveragePreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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

func (s *server) previewStrategyForCoverage(w http.ResponseWriter, r *http.Request, uid int64, accountID int64, body coveragePreviewRequest) (*strategyv1.PreviewRunStrategyResponse, bool) {
	runtimeID := strings.TrimSpace(body.RuntimeID)
	if s.controlPanelRouteFeature && runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime selection required")
		return nil, false
	}
	policy, ok := s.strategyRoutePolicyForAccount(r.Context(), w, uid, accountID, runtimeID)
	if !ok {
		return nil, false
	}
	cli, callerToken, _, ok := s.strategyClient(r.Context(), w, uid, modeEnsure, runtimeID, policy)
	if !ok {
		return nil, false
	}
	ctx := withCallerToken(r.Context(), callerToken)
	resp, err := cli.PreviewRunStrategy(ctx, &strategyv1.PreviewRunStrategyRequest{
		AccountId:    accountID,
		StrategyPath: body.StrategyPath,
		StartTimeMs:  body.StartTimeMS,
		EndTimeMs:    body.EndTimeMS,
		UserId:       uid,
		RuntimeId:    runtimeID,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return nil, false
	}
	return resp, true
}

func (s *server) buildCoveragePreview(w http.ResponseWriter, r *http.Request, declared []*strategyv1.LiveStreamBinding, body coveragePreviewRequest) (coveragePreviewResponse, bool) {
	out := coveragePreviewResponse{
		Complete:        true,
		CanAutoDownload: true,
		Inputs:          make([]coveragePreviewInputJSON, 0, len(declared)),
	}
	for _, binding := range declared {
		key := marketDataKeyFromLiveBinding(binding)
		resp, err := s.marketData.QueryMarketDataCoverage(r.Context(), &mdv1.QueryMarketDataCoverageRequest{
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
			Key:                   streamKeyToJSON(resp.GetKey()),
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
		Market:   binding.GetMarket(),
		Kind:     kind,
		Symbol:   binding.GetSymbol(),
		Interval: binding.GetInterval(),
	}
}
