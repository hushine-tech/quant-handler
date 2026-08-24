package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type downloadRunJobStatus string

const (
	downloadRunPending downloadRunJobStatus = "pending"
	downloadRunRunning downloadRunJobStatus = "running"
	downloadRunReady   downloadRunJobStatus = "ready"
	downloadRunError   downloadRunJobStatus = "error"
)

type downloadAndRunRequest struct {
	Interval        string  `json:"interval"`
	StartTimeMS     int64   `json:"start_time_ms"`
	EndTimeMS       int64   `json:"end_time_ms"`
	RuntimeID       string  `json:"runtime_id"`
	MaxLossClosePct float64 `json:"max_loss_close_pct"`
}

type downloadRunJob struct {
	JobID          string                             `json:"job_id"`
	Status         downloadRunJobStatus               `json:"status"`
	Progress       float64                            `json:"progress"`
	Message        string                             `json:"message,omitempty"`
	Requests       []marketDataRequestJSON            `json:"requests,omitempty"`
	SessionID      string                             `json:"session_id,omitempty"`
	Error          string                             `json:"error,omitempty"`
	RuntimeError   *runtimeDependencyHTTPError        `json:"runtime_error,omitempty"`
	Failures       []preflightFailureJSON             `json:"failures,omitempty"`
	TargetResults  []strategyLeverageTargetResultJSON `json:"target_results,omitempty"`
	Code           string                             `json:"code,omitempty"`
	RollbackFailed bool                               `json:"rollback_failed"`
	CreatedAt      time.Time                          `json:"created_at"`
	UpdatedAt      time.Time                          `json:"updated_at"`
}

type downloadRunJobStore struct {
	mu   sync.RWMutex
	seq  int64
	jobs map[string]downloadRunJob
}

func newDownloadRunJobStore() *downloadRunJobStore {
	return &downloadRunJobStore{jobs: make(map[string]downloadRunJob)}
}

func (s *server) downloadJobs() *downloadRunJobStore {
	if s.downloadRunJobs != nil {
		return s.downloadRunJobs
	}
	s.downloadRunJobs = newDownloadRunJobStore()
	return s.downloadRunJobs
}

func (s *downloadRunJobStore) create() downloadRunJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	now := time.Now().UTC()
	job := downloadRunJob{
		JobID:     fmt.Sprintf("download-run-%d-%d", now.UnixNano(), s.seq),
		Status:    downloadRunPending,
		Progress:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.jobs[job.JobID] = job
	return job
}

func (s *downloadRunJobStore) get(jobID string) (downloadRunJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[jobID]
	job.RuntimeError = cloneRuntimeDependencyHTTPError(job.RuntimeError)
	return job, ok
}

func (s *downloadRunJobStore) update(jobID string, mutate func(*downloadRunJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return
	}
	mutate(&job)
	job.UpdatedAt = time.Now().UTC()
	s.jobs[jobID] = job
}

func (s *server) handleDownloadAndRun(w http.ResponseWriter, r *http.Request, portfolioID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.marketData == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service is not configured (market-data control plane unavailable)")
		return
	}
	var body downloadAndRunRequest
	if err := decodeStrategyRequestBody(r.Body, &body, false); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.StartTimeMS <= 0 || body.EndTimeMS <= 0 {
		writeErr(w, http.StatusBadRequest, "start_time_ms and end_time_ms are required")
		return
	}
	if body.EndTimeMS <= body.StartTimeMS {
		writeErr(w, http.StatusBadRequest, "end_time_ms must be greater than start_time_ms")
		return
	}
	if strings.TrimSpace(body.Interval) == "" {
		body.Interval = "1m"
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}

	runtimeID := strings.TrimSpace(body.RuntimeID)
	if runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime selection required")
		return
	}
	policy, ok := s.strategyRoutePolicyForPortfolio(r.Context(), w, uid, portfolioID, runtimeID)
	if !ok {
		return
	}
	cli, _, ok := s.strategyClient(r.Context(), w, uid, routeEnsure, runtimeID, policy)
	if !ok {
		return
	}

	job := s.downloadJobs().create()
	go s.runDownloadAndRunJob(context.Background(), job.JobID, cli, uid, portfolioID, runtimeID, body)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *server) handleDownloadRunJobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jobID := strings.TrimPrefix(r.URL.Path, "/api/strategy/download-and-run-jobs/")
	jobID = strings.Trim(jobID, "/")
	if jobID == "" {
		writeErr(w, http.StatusBadRequest, "job_id is required")
		return
	}
	job, ok := s.downloadJobs().get(jobID)
	if !ok {
		writeErr(w, http.StatusNotFound, "download-and-run job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *server) runDownloadAndRunJob(ctx context.Context, jobID string, cli strategyv1.StrategyServiceClient, uid int64, portfolioID int64, runtimeID string, body downloadAndRunRequest) {
	store := s.downloadJobs()
	fail := func(err error) {
		store.update(jobID, func(job *downloadRunJob) {
			job.Status = downloadRunError
			job.SessionID = ""
			job.Failures = nil
			job.TargetResults = nil
			job.Code = ""
			job.RollbackFailed = false
			if runtimeErr, ok := runtimeDependencyErrorFromGRPC(err); ok {
				job.Error = runtimeErr.Message
				job.RuntimeError = cloneRuntimeDependencyHTTPError(runtimeErr)
				return
			}
			job.Error = err.Error()
			job.RuntimeError = nil
		})
	}
	failStructured := func(message, code string, failures []preflightFailureJSON, results []strategyLeverageTargetResultJSON, rollbackFailed bool) {
		store.update(jobID, func(job *downloadRunJob) {
			job.Status = downloadRunError
			job.SessionID = ""
			job.Error = strings.TrimSpace(message)
			job.RuntimeError = nil
			job.Failures = failures
			job.TargetResults = results
			job.Code = strings.TrimSpace(code)
			job.RollbackFailed = rollbackFailed
		})
	}
	store.update(jobID, func(job *downloadRunJob) {
		job.Status = downloadRunRunning
		job.Progress = 0.05
	})

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	preview, err := cli.PreviewRunStrategy(ctx, &strategyv1.PreviewRunStrategyRequest{
		PortfolioId:     portfolioID,
		StartTimeMs:     body.StartTimeMS,
		EndTimeMs:       body.EndTimeMS,
		UserId:          uid,
		RuntimeId:       runtimeID,
		MaxLossClosePct: body.MaxLossClosePct,
	})
	if err != nil {
		fail(err)
		return
	}
	if preview == nil {
		failStructured("runtime returned an empty strategy preview", "STRATEGY_PREVIEW_EMPTY", nil, nil, false)
		return
	}
	if !preview.GetOk() {
		failures := preflightFailuresToJSON(preview.GetFailures())
		message := "strategy preflight failed"
		code := "STRATEGY_PREFLIGHT_FAILED"
		if len(failures) > 0 {
			if strings.TrimSpace(failures[0].Reason) != "" {
				message = failures[0].Reason
			}
			if strings.TrimSpace(failures[0].Code) != "" {
				code = failures[0].Code
			}
		}
		failStructured(message, code, failures, nil, false)
		return
	}
	if strings.ToLower(strings.TrimSpace(preview.GetProfile())) != "backtest" {
		fail(fmt.Errorf("download-and-run only supports backtest profile"))
		return
	}
	declared := preview.GetDeclaredInputs()
	if len(declared) == 0 {
		fail(fmt.Errorf("strategy preview returned no declared inputs"))
		return
	}
	store.update(jobID, func(job *downloadRunJob) { job.Progress = 0.15 })

	requestIDs, err := s.createMissingCoverageRequests(ctx, uid, portfolioID, declared, body)
	if err != nil {
		fail(err)
		return
	}
	store.update(jobID, func(job *downloadRunJob) { job.Progress = 0.35 })

	if err := s.waitForCoverageValidation(ctx, jobID, uid, requestIDs, declared, body); err != nil {
		fail(err)
		return
	}
	store.update(jobID, func(job *downloadRunJob) {
		job.Progress = 0.9
		job.Message = "historical coverage is ready; starting backtest"
	})

	run, err := cli.RunStrategy(ctx, &strategyv1.RunStrategyRequest{
		PortfolioId:     portfolioID,
		Interval:        body.Interval,
		StartTimeMs:     body.StartTimeMS,
		EndTimeMs:       body.EndTimeMS,
		UserId:          uid,
		RuntimeId:       runtimeID,
		MaxLossClosePct: body.MaxLossClosePct,
	})
	if err != nil {
		fail(err)
		return
	}
	if run == nil {
		failStructured("runtime returned an empty strategy start result", "STRATEGY_START_RESULT_EMPTY", nil, nil, false)
		return
	}
	failures := preflightFailuresToJSON(run.GetFailures())
	targetResults := strategyLeverageTargetResultsToJSON(run.GetTargetResults())
	if !run.GetOk() {
		message := "strategy start failed"
		if len(failures) > 0 && strings.TrimSpace(failures[0].Reason) != "" {
			message = failures[0].Reason
		}
		code := strings.TrimSpace(run.GetCode())
		if code == "" {
			code = "STRATEGY_START_FAILED"
		}
		failStructured(message, code, failures, targetResults, run.GetRollbackFailed())
		return
	}
	if strings.TrimSpace(run.GetSessionId()) == "" {
		failStructured("strategy start returned no Session ID", "STRATEGY_SESSION_ID_MISSING", failures, targetResults, run.GetRollbackFailed())
		return
	}
	store.update(jobID, func(job *downloadRunJob) {
		job.Status = downloadRunReady
		job.Progress = 1
		job.Message = "backtest session started"
		job.SessionID = run.GetSessionId()
		job.Error = ""
		job.RuntimeError = nil
		job.Failures = failures
		job.TargetResults = targetResults
		job.Code = strings.TrimSpace(run.GetCode())
		job.RollbackFailed = run.GetRollbackFailed()
	})
}

func preflightFailuresToJSON(failures []*strategyv1.PreflightFailureProto) []preflightFailureJSON {
	result := make([]preflightFailureJSON, 0, len(failures))
	for _, failure := range failures {
		if failure != nil {
			result = append(result, preflightFailureToJSON(failure))
		}
	}
	return result
}

func (s *server) createMissingCoverageRequests(ctx context.Context, uid int64, portfolioID int64, declared []*strategyv1.LiveStreamBinding, body downloadAndRunRequest) (map[int64]struct{}, error) {
	requestIDs := make(map[int64]struct{})
	for _, binding := range declared {
		key := marketDataKeyFromLiveBinding(binding)
		coverage, err := s.marketData.QueryMarketDataCoverage(ctx, &mdv1.QueryMarketDataCoverageRequest{
			Key:     key,
			StartAt: timestamppb.New(time.UnixMilli(body.StartTimeMS).UTC()),
			EndAt:   timestamppb.New(time.UnixMilli(body.EndTimeMS).UTC()),
		})
		if err != nil {
			return nil, err
		}
		if coverage.GetNonDownloadableReason() != "" {
			return nil, fmt.Errorf("%s %s %s is not downloadable: %s", key.GetMarket(), key.GetSymbol(), key.GetInterval(), coverage.GetNonDownloadableReason())
		}
		for _, missing := range coverage.GetMissingSegments() {
			resp, err := s.marketData.CreateMarketDataRequest(ctx, &mdv1.CreateMarketDataRequestRequest{
				UserId:            uid,
				PortfolioId:       portfolioID,
				Key:               key,
				Scope:             "historical",
				NeedsLiveDelivery: false,
				RequestedStartAt:  missing.GetStartAt(),
				RequestedEndAt:    missing.GetEndAt(),
			})
			if err != nil {
				return nil, err
			}
			if id := resp.GetRequest().GetRequestId(); id > 0 {
				requestIDs[id] = struct{}{}
			}
		}
	}
	return requestIDs, nil
}

func (s *server) waitForCoverageValidation(ctx context.Context, jobID string, uid int64, requestIDs map[int64]struct{}, declared []*strategyv1.LiveStreamBinding, body downloadAndRunRequest) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		ok, err := s.validateDeclaredCoverage(ctx, declared, body)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if len(requestIDs) == 0 {
			return fmt.Errorf("market-data coverage validation failed")
		}
		if len(requestIDs) > 0 {
			requests, err := s.downloadRunHistoricalRequests(ctx, uid, requestIDs)
			if err != nil {
				return err
			}
			s.downloadJobs().update(jobID, func(job *downloadRunJob) {
				job.Status = downloadRunRunning
				job.Progress = maxDownloadRunProgress(job.Progress, progressFromHistoricalRequests(requests))
				job.Message = messageFromHistoricalRequests(requests)
				job.Requests = requests
			})
			if err := failOnHistoricalRequestError(requests); err != nil {
				return err
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *server) downloadRunHistoricalRequests(ctx context.Context, uid int64, requestIDs map[int64]struct{}) ([]marketDataRequestJSON, error) {
	resp, err := s.marketData.ListMarketDataRequests(ctx, &mdv1.ListMarketDataRequestsRequest{UserId: uid})
	if err != nil {
		return nil, err
	}
	out := make([]marketDataRequestJSON, 0, len(requestIDs))
	for _, entry := range resp.GetEntries() {
		req := entry.GetRequest()
		if _, ok := requestIDs[req.GetRequestId()]; !ok {
			continue
		}
		out = append(out, requestToJSON(req))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RequestID < out[j].RequestID
	})
	return out, nil
}

func failOnHistoricalRequestError(requests []marketDataRequestJSON) error {
	for _, req := range requests {
		status := strings.ToLower(strings.TrimSpace(req.Status))
		if status == "error" || status == "cancelled" {
			if req.LastError != "" {
				return fmt.Errorf("historical request %d %s: %s", req.RequestID, status, req.LastError)
			}
			return fmt.Errorf("historical request %d %s", req.RequestID, status)
		}
	}
	return nil
}

func progressFromHistoricalRequests(requests []marketDataRequestJSON) float64 {
	if len(requests) == 0 {
		return 0.36
	}
	var weight float64
	for _, req := range requests {
		switch strings.ToLower(strings.TrimSpace(req.Status)) {
		case "ready":
			weight += 1
		case "verifying":
			weight += 0.75
		case "running", "active":
			weight += 0.35
		case "pending":
			weight += 0.1
		case "error", "cancelled":
			weight += 1
		default:
			weight += 0.2
		}
	}
	progress := 0.35 + 0.53*(weight/float64(len(requests)))
	if progress > 0.88 {
		return 0.88
	}
	if progress < 0.36 {
		return 0.36
	}
	return progress
}

func maxDownloadRunProgress(previous, next float64) float64 {
	if next > previous {
		return next
	}
	return previous
}

func messageFromHistoricalRequests(requests []marketDataRequestJSON) string {
	if len(requests) == 0 {
		return "waiting for historical request state"
	}
	if len(requests) == 1 {
		req := requests[0]
		label := strings.TrimSpace(req.Key.Symbol + " " + req.Key.Interval)
		if label == "" {
			label = "historical data"
		}
		if req.LastError != "" {
			return fmt.Sprintf("historical request %d %s: %s", req.RequestID, req.Status, req.LastError)
		}
		return fmt.Sprintf("historical request %d %s: %s", req.RequestID, req.Status, label)
	}
	counts := make(map[string]int, len(requests))
	for _, req := range requests {
		status := strings.ToLower(strings.TrimSpace(req.Status))
		if status == "" {
			status = "unknown"
		}
		counts[status]++
	}
	parts := make([]string, 0, len(counts))
	for status, count := range counts {
		parts = append(parts, fmt.Sprintf("%s=%d", status, count))
	}
	sort.Strings(parts)
	return fmt.Sprintf("historical download requests: %s", strings.Join(parts, ", "))
}

func (s *server) validateDeclaredCoverage(ctx context.Context, declared []*strategyv1.LiveStreamBinding, body downloadAndRunRequest) (bool, error) {
	for _, binding := range declared {
		key := marketDataKeyFromLiveBinding(binding)
		resp, err := s.marketData.ValidateMarketDataCoverage(ctx, &mdv1.ValidateMarketDataCoverageRequest{
			Key:     key,
			StartAt: timestamppb.New(time.UnixMilli(body.StartTimeMS).UTC()),
			EndAt:   timestamppb.New(time.UnixMilli(body.EndTimeMS).UTC()),
		})
		if err != nil {
			return false, err
		}
		if !resp.GetOk() {
			return false, nil
		}
	}
	return true, nil
}
