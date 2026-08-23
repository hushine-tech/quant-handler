package app

import (
	"context"
	"errors"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
)

type controlPanelStrategyClient struct {
	rpc controlpanelv1.ControlPanelServiceClient
}

func (c controlPanelStrategyClient) RunStrategy(ctx context.Context, in *strategyv1.RunStrategyRequest, opts ...grpc.CallOption) (*strategyv1.RunStrategyResponse, error) {
	return c.rpc.RunStrategy(ctx, in, opts...)
}

// PrepareRunStrategyStart is an internal RuntimeChannel method. The public
// control-panel gateway does not expose it, and quant-handler never calls it;
// this explicit fail-closed method only keeps the narrow client adapter aligned
// with the additive strategy-service interface.
func (c controlPanelStrategyClient) PrepareRunStrategyStart(context.Context, *strategyv1.PrepareRunStrategyStartRequest, ...grpc.CallOption) (*strategyv1.PreparedRunStrategyStart, error) {
	return nil, status.Error(codes.Unimplemented, "PrepareRunStrategyStart is internal to RuntimeChannel")
}

func (c controlPanelStrategyClient) PreviewRunStrategy(ctx context.Context, in *strategyv1.PreviewRunStrategyRequest, opts ...grpc.CallOption) (*strategyv1.PreviewRunStrategyResponse, error) {
	return c.rpc.PreviewRunStrategy(ctx, in, opts...)
}

func (c controlPanelStrategyClient) ValidateStrategySource(ctx context.Context, in *strategyv1.ValidateStrategySourceRequest, opts ...grpc.CallOption) (*strategyv1.ValidateStrategySourceResponse, error) {
	return c.rpc.ValidateStrategySource(ctx, in, opts...)
}

func (c controlPanelStrategyClient) GetStrategyStatus(ctx context.Context, in *strategyv1.GetStrategyStatusRequest, opts ...grpc.CallOption) (*strategyv1.GetStrategyStatusResponse, error) {
	return c.rpc.GetStrategyStatus(ctx, in, opts...)
}

func (c controlPanelStrategyClient) StopStrategy(ctx context.Context, in *strategyv1.StopStrategyRequest, opts ...grpc.CallOption) (*strategyv1.StopStrategyResponse, error) {
	return c.rpc.StopStrategy(ctx, in, opts...)
}

// resolveStrategyRuntime resolves a runtime route via control-panel and returns
// the control-panel strategy proxy client.
//
// Behavior depends on route resolution:
//   - routeEnsure  → require runtime_id and call ResolveRuntimeRouteByID.
//     Used by run / preview paths.
//   - routeResolve → call ResolveRuntimeRoute; read-only.
//     Used by stop / status paths (the session must
//     already exist somewhere).
//
// On any failure (control-panel rejection or missing proxy client)
// writes an HTTP error response and returns nil. Caller MUST bail out; the
// control-panel proxy is the only allowed strategy RPC route.
type strategyRoutePolicy struct {
	role        string
	environment int
}

func strategyRoutePolicyForEnvironment(environment int32) strategyRoutePolicy {
	if environment == 0 {
		return strategyRoutePolicy{environment: 0}
	}
	return strategyRoutePolicy{role: "executor", environment: int(environment)}
}

func (s *server) resolveStrategyRuntime(ctx context.Context, w http.ResponseWriter, userID int64, resolution strategyRouteResolution, runtimeID string, policy strategyRoutePolicy) (strategyv1.StrategyServiceClient, string) {
	var err error
	switch resolution {
	case routeEnsure:
		if runtimeID == "" {
			writeErr(w, http.StatusBadRequest, "runtime selection required")
			return nil, ""
		}
		var route controlpanel.Route
		route, err = s.controlPanel.ResolveRouteByID(ctx, userID, runtimeID, policy.role, policy.environment)
		if err == nil {
			return s.controlPanelStrategyProxy(w), route.RuntimeID
		}
	case routeResolve:
		if runtimeID == "" {
			writeErr(w, http.StatusConflict, "session is not bound to a runtime")
			return nil, ""
		}
		var rt controlpanel.Route
		rt, err = s.controlPanel.ResolveRouteByID(ctx, userID, runtimeID, policy.role, policy.environment)
		if err == nil {
			return s.controlPanelStrategyProxy(w), rt.RuntimeID
		}
	default:
		writeErr(w, http.StatusInternalServerError, "internal: unknown strategy route resolution")
		return nil, ""
	}
	if err != nil {
		if errors.Is(err, controlpanel.ErrNotConfigured) {
			writeErr(w, http.StatusServiceUnavailable, "control-panel-service not configured")
			return nil, ""
		}
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return nil, ""
	}
	writeErr(w, http.StatusServiceUnavailable, "control-panel-service strategy proxy not configured")
	return nil, ""
}

func (s *server) controlPanelStrategyProxy(w http.ResponseWriter) strategyv1.StrategyServiceClient {
	if s.cpRuntime == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service strategy proxy not configured")
		return nil
	}
	return controlPanelStrategyClient{rpc: s.cpRuntime}
}

// strategyRouteResolution picks Ensure (lazy provision) vs Resolve (read-only).
type strategyRouteResolution int

const (
	routeEnsure strategyRouteResolution = iota
	routeResolve
)

// strategyClient is the single route every strategy-session handler uses to
// obtain a runtime RPC client. It always resolves runtime_id through
// control-panel-service and always sends strategy RPCs via RuntimeChannel proxy.
func (s *server) strategyClient(ctx context.Context, w http.ResponseWriter, userID int64, resolution strategyRouteResolution, runtimeID string, policy strategyRoutePolicy) (strategyv1.StrategyServiceClient, string, bool) {
	cli, runtimeID := s.resolveStrategyRuntime(ctx, w, userID, resolution, runtimeID, policy)
	if cli == nil {
		return nil, "", false
	}
	return cli, runtimeID, true
}
