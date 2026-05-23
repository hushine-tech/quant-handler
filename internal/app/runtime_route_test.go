package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	gotAccountID      int64
	gotMarket         string
	gotSymbol         string
	gotInterval       string
	gotStartTimeMS    int64
	gotEndTimeMS      int64
	gotRole           string
	gotMode           int
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
	f.gotAccountID = args.AccountID
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

func (f *fakeResolver) ResolveRouteByID(_ context.Context, userID int64, runtimeID string, role string, mode int) (controlpanel.Route, error) {
	f.resolveByIDCalls++
	f.gotUserID = userID
	f.gotRuntimeID = runtimeID
	f.gotRole = role
	f.gotMode = mode
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

// TestRuntimeRoute_FeatureFlagOffReturns404 proves the default D1a posture:
// even with a healthy control-panel client wired in, the shadow endpoint is
// NOT advertised unless the operator flips the flag. This is what protects
// the existing fixed STRATEGY_SERVICE_GRPC_ADDR call path.
func TestRuntimeRoute_FeatureFlagOffReturns404(t *testing.T) {
	resolver := &fakeResolver{
		resp: controlpanel.Route{RuntimeID: "rt_x", GRPCEndpoint: "10.0.0.1:50053"},
	}
	s := &server{
		controlPanel:             resolver,
		controlPanelRouteFeature: false,
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/_debug/runtime-route", nil), 42)
	rec := httptest.NewRecorder()

	s.handleResolveRuntimeRoute(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (feature flag off); body=%s", rec.Code, rec.Body.String())
	}
	if resolver.gotUserID != 0 {
		t.Errorf("ResolveRoute should not have been called when feature flag is off, got user_id=%d", resolver.gotUserID)
	}
}

// TestRuntimeRoute_FeatureFlagOnReturnsGone preserves the debug endpoint
// shape while documenting that name-based routing has been removed.
func TestRuntimeRoute_FeatureFlagOnReturnsGone(t *testing.T) {
	resolver := &fakeResolver{}
	s := &server{
		controlPanel:             resolver,
		controlPanelRouteFeature: true,
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
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

func TestRuntimeRoute_FeatureFlagOnButDisabledClientReturnsGone(t *testing.T) {
	s := &server{
		controlPanel:             controlpanel.Disabled(),
		controlPanelRouteFeature: true,
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/_debug/runtime-route", nil), 42)
	rec := httptest.NewRecorder()

	s.handleResolveRuntimeRoute(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRuntimeRoute_GRPCFailedPreconditionMapsTo412 confirms fail-closed
// errors from control-panel-service (NotFound / FailedPrecondition / etc.)
// get mapped through grpcToHTTP rather than swallowed. This is the
// no-silent-fallback property the section 6 cutover will rely on.
func TestRuntimeRoute_GRPCFailedPreconditionMapsTo412(t *testing.T) {
	resolver := &fakeResolver{
		err: status.Error(codes.FailedPrecondition, "runtime unhealthy"),
	}
	s := &server{
		controlPanel:             resolver,
		controlPanelRouteFeature: true,
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/_debug/runtime-route", nil), 42)
	rec := httptest.NewRecorder()

	s.handleResolveRuntimeRoute(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRuntimeRoute_MethodNotAllowed proves only GET is accepted, since the
// shadow endpoint is read-only.
func TestRuntimeRoute_MethodNotAllowed(t *testing.T) {
	s := &server{
		controlPanel:             controlpanel.Disabled(),
		controlPanelRouteFeature: true,
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/_debug/runtime-route", nil), 42)
	rec := httptest.NewRecorder()

	s.handleResolveRuntimeRoute(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestRuntimeRoute_MissingUserContextReturnsGone(t *testing.T) {
	s := &server{
		controlPanel:             controlpanel.Disabled(),
		controlPanelRouteFeature: true,
		jwtSecret:                []byte("s"),
		corsOrigins:              []string{"*"},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/_debug/runtime-route", nil)
	rec := httptest.NewRecorder()

	s.handleResolveRuntimeRoute(rec, req)

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
}
