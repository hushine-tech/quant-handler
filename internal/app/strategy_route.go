package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	grpcclientmw "github.com/hushine-tech/golang-lib/middleware/grpcclient"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	"github.com/hushine-tech/quant-handler/internal/logger"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
)

// callerTokenMetadataKey is retained for the legacy direct-dial path when
// control-panel routing is disabled. RuntimeChannel proxy routing does not
// attach caller_token to strategy RPCs.
const callerTokenMetadataKey = "x-caller-token"

// runtimeDialer keeps a process-lifetime cache of legacy gRPC connections
// keyed by `host:port`.
//
// Connection eviction: D1 keeps connections forever; if a runtime is
// ended or moved, the next call to its endpoint hangs/fails and the
// caller surfaces the error. D3's control-plane proxy makes this cache
// obsolete. For D1 a simple map is enough.
type runtimeDialer struct {
	mu          sync.Mutex
	conns       map[string]*grpc.ClientConn
	dialOptions []grpc.DialOption
}

func newRuntimeDialer(opts ...grpc.DialOption) *runtimeDialer {
	return &runtimeDialer{
		conns:       make(map[string]*grpc.ClientConn),
		dialOptions: opts,
	}
}

// Dial returns a strategy-service client backed by a cached connection
// to `endpoint`. Empty endpoint is rejected.
func (d *runtimeDialer) Dial(ctx context.Context, endpoint string) (strategyv1.StrategyServiceClient, error) {
	if endpoint == "" {
		return nil, errors.New("runtime endpoint is empty")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if conn, ok := d.conns[endpoint]; ok {
		return strategyv1.NewStrategyServiceClient(conn), nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, endpoint, d.dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("dial runtime %q: %w", endpoint, err)
	}
	d.conns[endpoint] = conn
	return strategyv1.NewStrategyServiceClient(conn), nil
}

// Close shuts down all cached connections. Used by graceful shutdown
// in main; not exercised by tests.
func (d *runtimeDialer) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.conns {
		_ = c.Close()
	}
	d.conns = make(map[string]*grpc.ClientConn)
}

type controlPanelStrategyClient struct {
	rpc controlpanelv1.ControlPanelServiceClient
}

func (c controlPanelStrategyClient) RunStrategy(ctx context.Context, in *strategyv1.RunStrategyRequest, opts ...grpc.CallOption) (*strategyv1.RunStrategyResponse, error) {
	return c.rpc.RunStrategy(ctx, in, opts...)
}

func (c controlPanelStrategyClient) PreviewRunStrategy(ctx context.Context, in *strategyv1.PreviewRunStrategyRequest, opts ...grpc.CallOption) (*strategyv1.PreviewRunStrategyResponse, error) {
	return c.rpc.PreviewRunStrategy(ctx, in, opts...)
}

func (c controlPanelStrategyClient) GetStrategyStatus(ctx context.Context, in *strategyv1.GetStrategyStatusRequest, opts ...grpc.CallOption) (*strategyv1.GetStrategyStatusResponse, error) {
	return c.rpc.GetStrategyStatus(ctx, in, opts...)
}

func (c controlPanelStrategyClient) StopStrategy(ctx context.Context, in *strategyv1.StopStrategyRequest, opts ...grpc.CallOption) (*strategyv1.StopStrategyResponse, error) {
	return c.rpc.StopStrategy(ctx, in, opts...)
}

func (c controlPanelStrategyClient) GetLiveConsumptionDiagnostics(ctx context.Context, in *strategyv1.GetLiveConsumptionDiagnosticsRequest, opts ...grpc.CallOption) (*strategyv1.GetLiveConsumptionDiagnosticsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "control-panel proxy does not implement live diagnostics")
}

func (c controlPanelStrategyClient) ValidateStrategyCode(ctx context.Context, in *strategyv1.ValidateStrategyCodeRequest, opts ...grpc.CallOption) (*strategyv1.ValidateStrategyCodeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "control-panel proxy does not implement strategy validation")
}

// defaultRuntimeDialOptions is the dial-option set used by handler when
// connecting to a strategy-runtime. Matches the existing fixed-address
// dial so tracing / logging behavior is unchanged.
func defaultRuntimeDialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(grpcclientmw.UnaryClientInterceptor(logger.Instance())),
		grpc.WithStreamInterceptor(grpcclientmw.StreamClientInterceptor(logger.Instance())),
	}
}

// resolveStrategyRuntime resolves a runtime route via control-panel and returns
// the control-panel strategy proxy client. Caller MUST already be on the
// cutover path (feature flag on).
//
// Behavior depends on `mode`:
//   - modeEnsure  → require runtime_id and call ResolveRuntimeRouteByID.
//     Used by run / preview paths.
//   - modeResolve → call ResolveRuntimeRoute; read-only.
//     Used by stop / status paths (the session must
//     already exist somewhere).
//
// On any failure (control-panel rejection or missing proxy client)
// writes an HTTP error response and returns nil. Caller MUST bail out
// without falling back to the legacy fixed dial — that is the section
// 6.4 fail-closed contract.
type strategyRoutePolicy struct {
	role string
	mode int
}

func defaultStrategyRoutePolicy() strategyRoutePolicy {
	return strategyRoutePolicy{role: "executor", mode: -1}
}

func strategyRoutePolicyForSessionMode(mode int32) strategyRoutePolicy {
	if mode == 0 {
		return strategyRoutePolicy{mode: 0}
	}
	return strategyRoutePolicy{role: "executor", mode: int(mode)}
}

func (s *server) resolveStrategyRuntime(ctx context.Context, w http.ResponseWriter, userID int64, mode strategyRouteMode, runtimeID string, policy strategyRoutePolicy) (strategyv1.StrategyServiceClient, string, string) {
	var err error
	switch mode {
	case modeEnsure:
		if runtimeID == "" {
			writeErr(w, http.StatusBadRequest, "runtime selection required")
			return nil, "", ""
		}
		var route controlpanel.Route
		route, err = s.controlPanel.ResolveRouteByID(ctx, userID, runtimeID, policy.role, policy.mode)
		if err == nil {
			return s.controlPanelStrategyProxy(w), "", route.RuntimeID
		}
	case modeResolve:
		if runtimeID == "" {
			writeErr(w, http.StatusConflict, "session is not bound to a runtime")
			return nil, "", ""
		}
		var rt controlpanel.Route
		rt, err = s.controlPanel.ResolveRouteByID(ctx, userID, runtimeID, policy.role, policy.mode)
		if err == nil {
			return s.controlPanelStrategyProxy(w), "", rt.RuntimeID
		}
	default:
		writeErr(w, http.StatusInternalServerError, "internal: unknown strategyRouteMode")
		return nil, "", ""
	}
	if err != nil {
		if errors.Is(err, controlpanel.ErrNotConfigured) {
			writeErr(w, http.StatusServiceUnavailable, "control-panel-service not configured")
			return nil, "", ""
		}
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return nil, "", ""
	}
	writeErr(w, http.StatusServiceUnavailable, "control-panel-service strategy proxy not configured")
	return nil, "", ""
}

func (s *server) controlPanelStrategyProxy(w http.ResponseWriter) strategyv1.StrategyServiceClient {
	if s.cpRuntime == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service strategy proxy not configured")
		return nil
	}
	return controlPanelStrategyClient{rpc: s.cpRuntime}
}

// strategyRouteMode picks Ensure (lazy provision) vs Resolve (read-only).
type strategyRouteMode int

const (
	modeEnsure strategyRouteMode = iota
	modeResolve
)

// strategyClient is the single seam every strategy-session handler uses
// to obtain a runtime gRPC client. It encapsulates the feature-flag
// branching:
//
//   - features.control_panel_route_resolution = false → return the
//     legacy fixed-address strategy-service client (s.strategy).
//   - = true → resolve a runtime via control-panel-service and return the
//     control-panel strategy proxy client; on any control-panel/proxy failure,
//     surface the error and return ok=false. **No silent fallback to
//     the fixed dial when the feature flag is on** — this is the
//     section 6.4 fail-closed contract.
//
// Returns (client, callerToken, ok). When ok=false, an HTTP error has
// already been written to `w` and the caller MUST return immediately.
func (s *server) strategyClient(ctx context.Context, w http.ResponseWriter, userID int64, mode strategyRouteMode, runtimeID string, policy strategyRoutePolicy) (strategyv1.StrategyServiceClient, string, string, bool) {
	if s.controlPanelRouteFeature {
		// Cutover path. Errors surface via writeErr inside
		// resolveStrategyRuntime; no fallback.
		cli, token, runtimeID := s.resolveStrategyRuntime(ctx, w, userID, mode, runtimeID, policy)
		if cli == nil {
			return nil, "", "", false
		}
		return cli, token, runtimeID, true
	}
	// Legacy path: fixed STRATEGY_SERVICE_GRPC_ADDR dial.
	if s.strategy == nil {
		writeErr(w, http.StatusServiceUnavailable, "strategy-service not configured")
		return nil, "", "", false
	}
	return s.strategy, "", "", true
}

// withCallerToken attaches caller_token only for legacy direct-dial strategy
// calls. RuntimeChannel proxy calls leave token empty.
func withCallerToken(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, callerTokenMetadataKey, token)
}
