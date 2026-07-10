package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/controlpanel"
)

// fakeResolver is a stub controlpanel.Resolver. It records the last call
// and returns a configurable response or error. It is only used by these
// tests and intentionally does not embed the proto-generated client —
// the controlpanel.Resolver interface is the seam.
type fakeResolver struct {
	gotUserID         int64
	gotName           string
	gotProfile        string
	resp              controlpanel.Route
	err               error
	ensureResp        controlpanel.EnsureResult
	ensureErr         error
	runtimeList       controlpanel.RuntimeList
	listErr           error
	runtimeResp       controlpanel.Runtime
	getRuntimeErr     error
	endRuntimeResp    controlpanel.Runtime
	endRuntimeErr     error
	admissionFailures []controlpanel.RuntimeAdmissionFailure
	admissionErr      error
	debugWorkspace    controlpanel.DebugWorkspaceState
	debugWorkspaceErr error
	debugDataset      controlpanel.DebugDatasetState
	debugDatasetErr   error
	gotRuntimeID      string
	gotHostPath       string
	gotContainerPath  string
	gotPortfolioID      int64
	gotMarket         string
	gotSymbol         string
	gotInterval       string
	gotStartTimeMS    int64
	gotEndTimeMS      int64
	gotRole           string
	gotEnvironment    int
	resolveByIDResp   controlpanel.Route
	resolveByIDErr    error
	resolveCalls      int
	ensureCalls       int
	listCalls         int
	getRuntimeCalls   int
	endRuntimeCalls   int
	admissionCalls    int
	prepareDebugCalls int
	loadDebugCalls    int
	getDatasetCalls   int
	resolveByIDCalls  int
}

func (f *fakeResolver) ListRuntimes(_ context.Context, userID int64, statusFilter, sourceFilter string, limit, offset int) (controlpanel.RuntimeList, error) {
	f.listCalls++
	f.gotUserID = userID
	return f.runtimeList, f.listErr
}

func (f *fakeResolver) GetRuntime(_ context.Context, userID int64, runtimeID string) (controlpanel.Runtime, error) {
	f.getRuntimeCalls++
	f.gotUserID = userID
	f.gotRuntimeID = runtimeID
	return f.runtimeResp, f.getRuntimeErr
}

func (f *fakeResolver) EndRuntime(_ context.Context, userID int64, runtimeID string) (controlpanel.Runtime, error) {
	f.endRuntimeCalls++
	f.gotUserID = userID
	f.gotRuntimeID = runtimeID
	return f.endRuntimeResp, f.endRuntimeErr
}

func (f *fakeResolver) ListRuntimeAdmissionFailures(_ context.Context, userID int64, limit int) ([]controlpanel.RuntimeAdmissionFailure, error) {
	f.admissionCalls++
	f.gotUserID = userID
	return f.admissionFailures, f.admissionErr
}

func (f *fakeResolver) PrepareDebugWorkspace(_ context.Context, userID int64, runtimeID, hostPath, containerPath string) (controlpanel.DebugWorkspaceState, error) {
	f.prepareDebugCalls++
	f.gotUserID = userID
	f.gotRuntimeID = runtimeID
	f.gotHostPath = hostPath
	f.gotContainerPath = containerPath
	return f.debugWorkspace, f.debugWorkspaceErr
}

func (f *fakeResolver) LoadDebugDataset(_ context.Context, args controlpanel.LoadDebugDatasetArgs) (controlpanel.DebugDatasetState, error) {
	f.loadDebugCalls++
	f.gotUserID = args.UserID
	f.gotRuntimeID = args.RuntimeID
	f.gotPortfolioID = args.PortfolioID
	f.gotMarket = args.Market
	f.gotSymbol = args.Symbol
	f.gotInterval = args.Interval
	f.gotStartTimeMS = args.StartTimeMS
	f.gotEndTimeMS = args.EndTimeMS
	return f.debugDataset, f.debugDatasetErr
}

func (f *fakeResolver) GetRuntimeDebugDataset(_ context.Context, userID int64, runtimeID string) (controlpanel.DebugDatasetState, error) {
	f.getDatasetCalls++
	f.gotUserID = userID
	f.gotRuntimeID = runtimeID
	return f.debugDataset, f.debugDatasetErr
}

func (f *fakeResolver) ResolveRouteByID(_ context.Context, userID int64, runtimeID string, role string, environment int) (controlpanel.Route, error) {
	f.resolveByIDCalls++
	f.gotUserID = userID
	f.gotRuntimeID = runtimeID
	f.gotRole = role
	f.gotEnvironment = environment
	if f.resolveByIDResp.RuntimeID == "" && f.resolveByIDErr == nil {
		return f.resp, f.err
	}
	return f.resolveByIDResp, f.resolveByIDErr
}

func (f *fakeResolver) EnsureHostedRuntime(_ context.Context, userID int64, name, profile string) (controlpanel.EnsureResult, error) {
	f.ensureCalls++
	f.gotUserID = userID
	f.gotName = name
	f.gotProfile = profile
	return f.ensureResp, f.ensureErr
}

func TestRuntimeRouteByNameReturnsGone(t *testing.T) {
	resolver := &fakeResolver{
		resp: controlpanel.Route{RuntimeID: "rt_x"},
	}
	s := &server{
		controlPanel: resolver,
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/_debug/runtime-route", nil), 42)
	rec := httptest.NewRecorder()

	s.handleResolveRuntimeRoute(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410; body=%s", rec.Code, rec.Body.String())
	}
	if !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("body is not JSON: %s", rec.Body.String())
	}
	if resolver.resolveCalls != 0 || resolver.resolveByIDCalls != 0 {
		t.Fatalf("route resolver was called after route-by-name removal")
	}
}

// TestRuntimeRoute_MethodNotAllowed proves only GET is accepted, since the
// removal endpoint is read-only.
func TestRuntimeRoute_MethodNotAllowed(t *testing.T) {
	s := &server{
		controlPanel: controlpanel.Disabled(),
		jwtSecret:    []byte("s"),
		corsOrigins:  []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/_debug/runtime-route", nil), 42)
	rec := httptest.NewRecorder()

	s.handleResolveRuntimeRoute(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
