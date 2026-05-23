package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRuntimeManagement_ListRuntimes(t *testing.T) {
	heartbeat := time.Date(2026, 5, 13, 1, 2, 3, 0, time.UTC)
	resolver := &fakeResolver{
		runtimeList: controlpanel.RuntimeList{
			Runtimes: []controlpanel.Runtime{{
				RuntimeID:       "rt_hosted",
				UserID:          42,
				Name:            "default",
				Source:          "hosted",
				Status:          "running",
				ResourceProfile: "small",
				Version:         "v1",
				HeartbeatAt:     heartbeat,
			}},
			Total: 1,
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/runtimes?status=running&source=hosted&limit=10&offset=5", nil), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimesCollection(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.listCalls != 1 || resolver.gotUserID != 42 {
		t.Fatalf("ListRuntimes calls/user = %d/%d, want 1/42", resolver.listCalls, resolver.gotUserID)
	}
	var body runtimeListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Runtimes) != 1 || body.Runtimes[0].RuntimeID != "rt_hosted" {
		t.Fatalf("runtimes = %+v, want rt_hosted", body.Runtimes)
	}
	if body.Runtimes[0].HeartbeatAt != heartbeat.Format(time.RFC3339Nano) {
		t.Fatalf("heartbeat_at = %q, want %q", body.Runtimes[0].HeartbeatAt, heartbeat.Format(time.RFC3339Nano))
	}
}

func TestRuntimeManagement_ListEligibleExecutorRuntimesFiltersRoleModeAndHealth(t *testing.T) {
	heartbeat := time.Date(2026, 5, 13, 1, 2, 3, 0, time.UTC)
	resolver := &fakeResolver{
		runtimeList: controlpanel.RuntimeList{
			Runtimes: []controlpanel.Runtime{
				{RuntimeID: "rt-exec", UserID: 42, Source: "hosted", Role: "executor", Status: "active", ResourceProfile: "small", HeartbeatAt: heartbeat, ConnectionOwnerInstanceID: "cp-1"},
				{RuntimeID: "rt-debug", UserID: 42, Source: "self_hosted", Role: "debugger", Status: "active", ResourceProfile: "small", HeartbeatAt: heartbeat, ConnectionOwnerInstanceID: "cp-1"},
				{RuntimeID: "rt-unhealthy", UserID: 42, Source: "hosted", Role: "executor", Status: "unhealthy", ResourceProfile: "small", HeartbeatAt: heartbeat, ConnectionOwnerInstanceID: "cp-1"},
				{RuntimeID: "rt-no-heartbeat", UserID: 42, Source: "hosted", Role: "executor", Status: "active", ResourceProfile: "small"},
			},
			Total: 4,
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/runtimes?eligible=session_start&role=executor&mode=2&limit=100", nil), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimesCollection(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body runtimeListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Runtimes) != 1 || body.Runtimes[0].RuntimeID != "rt-exec" {
		t.Fatalf("eligible runtimes = %+v, want only rt-exec", body.Runtimes)
	}
	if body.Total != 1 {
		t.Fatalf("total = %d, want filtered total 1", body.Total)
	}
}

func TestRuntimeManagement_ListEligibleModeZeroAllowsExecutorAndDebugger(t *testing.T) {
	heartbeat := time.Date(2026, 5, 13, 1, 2, 3, 0, time.UTC)
	resolver := &fakeResolver{
		runtimeList: controlpanel.RuntimeList{
			Runtimes: []controlpanel.Runtime{
				{RuntimeID: "rt-exec", UserID: 42, Source: "hosted", Role: "executor", Status: "active", ResourceProfile: "small", HeartbeatAt: heartbeat, ConnectionOwnerInstanceID: "cp-1"},
				{RuntimeID: "rt-debug", UserID: 42, Source: "self_hosted", Role: "debugger", Status: "active", ResourceProfile: "small", HeartbeatAt: heartbeat, ConnectionOwnerInstanceID: "cp-1"},
				{RuntimeID: "rt-unhealthy-debug", UserID: 42, Source: "self_hosted", Role: "debugger", Status: "unhealthy", ResourceProfile: "small", HeartbeatAt: heartbeat, ConnectionOwnerInstanceID: "cp-1"},
			},
			Total: 3,
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/runtimes?eligible=session_start&mode=0&limit=100", nil), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimesCollection(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body runtimeListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Runtimes) != 2 {
		t.Fatalf("eligible runtimes = %+v, want executor and debugger", body.Runtimes)
	}
	got := map[string]bool{}
	for _, rt := range body.Runtimes {
		got[rt.RuntimeID] = true
	}
	if !got["rt-exec"] || !got["rt-debug"] {
		t.Fatalf("eligible runtime ids = %+v, want rt-exec and rt-debug", got)
	}
	if body.Total != 2 {
		t.Fatalf("total = %d, want filtered total 2", body.Total)
	}
}

func TestRuntimeManagement_GetRuntime(t *testing.T) {
	resolver := &fakeResolver{
		runtimeResp: controlpanel.Runtime{
			RuntimeID:       "rt_self",
			UserID:          42,
			Name:            "default",
			Source:          "self_hosted",
			Status:          "connected",
			CredentialKeyID: "rk_123",
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/runtimes/rt_self", nil), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimeByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.getRuntimeCalls != 1 || resolver.gotRuntimeID != "rt_self" {
		t.Fatalf("GetRuntime calls/runtime = %d/%q, want 1/rt_self", resolver.getRuntimeCalls, resolver.gotRuntimeID)
	}
	var body runtimeJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.CredentialKeyID != "rk_123" {
		t.Fatalf("credential_key_id = %q, want rk_123", body.CredentialKeyID)
	}
}

func TestRuntimeManagement_PrepareDebugWorkspace(t *testing.T) {
	prepared := time.Date(2026, 5, 22, 1, 2, 3, 0, time.UTC)
	resolver := &fakeResolver{
		debugWorkspace: controlpanel.DebugWorkspaceState{
			HostPath:      "/Users/xdy/debug",
			ContainerPath: "/workspace",
			TemplatePath:  "/workspace/self_hosted_strategy.py",
			PreparedAt:    prepared,
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodPost, "/api/runtimes/rt-debug/prepare-debugging", bytes.NewBufferString(`{"host_path":"/Users/xdy/debug","container_path":"/workspace"}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimeByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.prepareDebugCalls != 1 || resolver.gotRuntimeID != "rt-debug" || resolver.gotHostPath != "/Users/xdy/debug" {
		t.Fatalf("prepare calls/runtime/path = %d/%q/%q", resolver.prepareDebugCalls, resolver.gotRuntimeID, resolver.gotHostPath)
	}
	var body debugWorkspaceJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.TemplatePath != "/workspace/self_hosted_strategy.py" || body.PreparedAt != prepared.Format(time.RFC3339Nano) {
		t.Fatalf("workspace = %+v", body)
	}
}

func TestRuntimeManagement_GetRuntimeDebugDataset(t *testing.T) {
	loaded := time.Date(2026, 5, 22, 1, 2, 3, 0, time.UTC)
	resolver := &fakeResolver{
		debugDataset: controlpanel.DebugDatasetState{
			DatasetID:      "dbg-1",
			UserID:         42,
			AccountID:      7,
			RuntimeID:      "rt-debug",
			Market:         "futures",
			Symbol:         "ETHUSDT",
			Interval:       "1m",
			BarCount:       60,
			CoverageStatus: "complete",
			LoadedAt:       loaded,
			State:          "active",
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/runtimes/rt-debug/debug-dataset", nil), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimeByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.getDatasetCalls != 1 || resolver.gotRuntimeID != "rt-debug" {
		t.Fatalf("dataset calls/runtime = %d/%q", resolver.getDatasetCalls, resolver.gotRuntimeID)
	}
	var body debugDatasetJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.DatasetID != "dbg-1" || body.State != "active" || body.BarCount != 60 {
		t.Fatalf("dataset = %+v", body)
	}
}

func TestRuntimeManagement_EndRuntime(t *testing.T) {
	resolver := &fakeResolver{
		endRuntimeResp: controlpanel.Runtime{
			RuntimeID:       "rt_hosted",
			UserID:          42,
			Name:            "default",
			Source:          "hosted",
			Status:          "ended",
			ResourceProfile: "small",
			EndedReason:     "user_requested",
			CleanupStatus:   "failed",
			CleanupReason:   "docker rm failed",
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodDelete, "/api/runtimes/rt_hosted", nil), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimeByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.endRuntimeCalls != 1 || resolver.gotRuntimeID != "rt_hosted" || resolver.gotUserID != 42 {
		t.Fatalf("EndRuntime calls/runtime/user = %d/%q/%d, want 1/rt_hosted/42", resolver.endRuntimeCalls, resolver.gotRuntimeID, resolver.gotUserID)
	}
	var body runtimeJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != "ended" {
		t.Fatalf("status = %q, want ended", body.Status)
	}
	if body.EndedReason != "user_requested" {
		t.Fatalf("ended_reason = %q, want user_requested", body.EndedReason)
	}
	if body.CleanupStatus != "failed" || body.CleanupReason != "docker rm failed" {
		t.Fatalf("cleanup = %q/%q, want failed/docker rm failed", body.CleanupStatus, body.CleanupReason)
	}
}

func TestRuntimeManagement_EnsureHostedRuntime(t *testing.T) {
	resolver := &fakeResolver{
		ensureResp: controlpanel.EnsureResult{
			Route:       controlpanel.Route{RuntimeID: "rt_hosted"},
			Provisioned: true,
		},
		runtimeResp: controlpanel.Runtime{
			RuntimeID:       "rt_hosted",
			UserID:          42,
			Name:            "default",
			Source:          "hosted",
			Status:          "starting",
			ResourceProfile: "medium",
		},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodPost, "/api/runtimes", bytes.NewBufferString(`{"name":"default","resource_profile":"medium"}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimesCollection(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.ensureCalls != 1 || resolver.getRuntimeCalls != 1 {
		t.Fatalf("Ensure/Get calls = %d/%d, want 1/1", resolver.ensureCalls, resolver.getRuntimeCalls)
	}
	if resolver.gotName != "default" || resolver.gotProfile != "medium" {
		t.Fatalf("Ensure args = %q/%q, want default/medium", resolver.gotName, resolver.gotProfile)
	}
	var body ensureHostedRuntimeJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !body.Provisioned || body.Runtime.RuntimeID != "rt_hosted" {
		t.Fatalf("body = %+v, want provisioned rt_hosted", body)
	}
}

func TestRuntimeManagement_EnsureHostedRuntimeConflict(t *testing.T) {
	resolver := &fakeResolver{
		ensureErr: status.Error(codes.AlreadyExists, "hosted runtime slot occupied; cancel the existing runtime first"),
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodPost, "/api/runtimes", bytes.NewBufferString(`{"name":"default","resource_profile":"small"}`)), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimesCollection(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.ensureCalls != 1 {
		t.Fatalf("Ensure calls = %d, want 1", resolver.ensureCalls)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("cancel the existing runtime first")) {
		t.Fatalf("body = %s, want actionable cancel message", rec.Body.String())
	}
}

func TestRuntimeManagement_ListAdmissionFailures(t *testing.T) {
	lastSeen := time.Date(2026, 5, 16, 1, 2, 3, 0, time.UTC)
	resolver := &fakeResolver{
		admissionFailures: []controlpanel.RuntimeAdmissionFailure{{
			AdmissionFailureID: 1,
			UserID:             42,
			CredentialKeyID:    "key-consumed",
			RequestedRuntimeID: "selfhosted-key-consumed",
			RequestedName:      "custom-old",
			Source:             "self_hosted",
			Role:               "executor",
			FailureCode:        "permission_denied",
			Reason:             "credential consumed by runtime selfhosted-key-consumed",
			ConsumedRuntimeID:  "selfhosted-key-consumed",
			LastSeenAt:         lastSeen,
			AttemptCount:       2,
		}},
	}
	s := &server{controlPanel: resolver, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodGet, "/api/runtime-admission-failures?limit=5", nil), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimeAdmissionFailures(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resolver.admissionCalls != 1 || resolver.gotUserID != 42 {
		t.Fatalf("admission calls/user = %d/%d, want 1/42", resolver.admissionCalls, resolver.gotUserID)
	}
	var body struct {
		Failures []runtimeAdmissionFailureJSON `json:"failures"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Failures) != 1 || body.Failures[0].ConsumedRuntimeID != "selfhosted-key-consumed" {
		t.Fatalf("failures = %+v", body.Failures)
	}
	if body.Failures[0].LastSeenAt != lastSeen.Format(time.RFC3339Nano) {
		t.Fatalf("last_seen_at = %q, want %q", body.Failures[0].LastSeenAt, lastSeen.Format(time.RFC3339Nano))
	}
}
