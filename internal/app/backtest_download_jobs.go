package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	StrategyPath string `json:"strategy_path"`
	Interval     string `json:"interval"`
	StartTimeMS  int64  `json:"start_time_ms"`
	EndTimeMS    int64  `json:"end_time_ms"`
	RuntimeID    string `json:"runtime_id"`
}

type downloadRunJob struct {
	JobID     string               `json:"job_id"`
	Status    downloadRunJobStatus `json:"status"`
	Progress  float64              `json:"progress"`
	SessionID string               `json:"session_id,omitempty"`
	Error     string               `json:"error,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
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

func (s *server) handleDownloadAndRun(w http.ResponseWriter, r *http.Request, accountID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.marketData == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service is not configured (market-data control plane unavailable)")
		return
	}
	var body downloadAndRunRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
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
	if s.controlPanelRouteFeature && runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime selection required")
		return
	}
	policy, ok := s.strategyRoutePolicyForAccount(r.Context(), w, uid, accountID, runtimeID)
	if !ok {
		return
	}
	cli, callerToken, _, ok := s.strategyClient(r.Context(), w, uid, modeEnsure, runtimeID, policy)
	if !ok {
		return
	}

	job := s.downloadJobs().create()
	go s.runDownloadAndRunJob(context.Background(), job.JobID, cli, callerToken, uid, accountID, runtimeID, body)
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

func (s *server) runDownloadAndRunJob(ctx context.Context, jobID string, cli strategyv1.StrategyServiceClient, callerToken string, uid int64, accountID int64, runtimeID string, body downloadAndRunRequest) {
	store := s.downloadJobs()
	fail := func(err error) {
		store.update(jobID, func(job *downloadRunJob) {
			job.Status = downloadRunError
			job.Error = err.Error()
		})
	}
	store.update(jobID, func(job *downloadRunJob) {
		job.Status = downloadRunRunning
		job.Progress = 0.05
	})

	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	ctx = withCallerToken(ctx, callerToken)

	preview, err := cli.PreviewRunStrategy(ctx, &strategyv1.PreviewRunStrategyRequest{
		AccountId:    accountID,
		StrategyPath: body.StrategyPath,
		StartTimeMs:  body.StartTimeMS,
		EndTimeMs:    body.EndTimeMS,
		UserId:       uid,
		RuntimeId:    runtimeID,
	})
	if err != nil {
		fail(err)
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

	requestIDs, err := s.createMissingCoverageRequests(ctx, uid, accountID, declared, body)
	if err != nil {
		fail(err)
		return
	}
	store.update(jobID, func(job *downloadRunJob) { job.Progress = 0.35 })

	if err := s.waitForCoverageValidation(ctx, uid, requestIDs, declared, body); err != nil {
		fail(err)
		return
	}
	store.update(jobID, func(job *downloadRunJob) { job.Progress = 0.9 })

	run, err := cli.RunStrategy(ctx, &strategyv1.RunStrategyRequest{
		AccountId:    accountID,
		StrategyPath: body.StrategyPath,
		Interval:     body.Interval,
		StartTimeMs:  body.StartTimeMS,
		EndTimeMs:    body.EndTimeMS,
		UserId:       uid,
		RuntimeId:    runtimeID,
	})
	if err != nil {
		fail(err)
		return
	}
	store.update(jobID, func(job *downloadRunJob) {
		job.Status = downloadRunReady
		job.Progress = 1
		job.SessionID = run.GetSessionId()
		job.Error = ""
	})
}

func (s *server) createMissingCoverageRequests(ctx context.Context, uid int64, accountID int64, declared []*strategyv1.LiveStreamBinding, body downloadAndRunRequest) (map[int64]struct{}, error) {
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
				AccountId:         accountID,
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

func (s *server) waitForCoverageValidation(ctx context.Context, uid int64, requestIDs map[int64]struct{}, declared []*strategyv1.LiveStreamBinding, body downloadAndRunRequest) error {
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
			if err := s.failOnHistoricalRequestError(ctx, uid, requestIDs); err != nil {
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

func (s *server) failOnHistoricalRequestError(ctx context.Context, uid int64, requestIDs map[int64]struct{}) error {
	resp, err := s.marketData.ListMarketDataRequests(ctx, &mdv1.ListMarketDataRequestsRequest{UserId: uid})
	if err != nil {
		return err
	}
	for _, entry := range resp.GetEntries() {
		req := entry.GetRequest()
		if _, ok := requestIDs[req.GetRequestId()]; !ok {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(req.GetStatus()))
		if status == "error" || status == "cancelled" {
			if req.GetLastError() != "" {
				return fmt.Errorf("historical request %d %s: %s", req.GetRequestId(), status, req.GetLastError())
			}
			return fmt.Errorf("historical request %d %s", req.GetRequestId(), status)
		}
	}
	return nil
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
