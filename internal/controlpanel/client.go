// Package controlpanel wraps the control-panel-service gRPC client used by
// quant-handler for runtime_id routing. Strategy session traffic goes through
// control-panel strategy proxy RPCs for both hosted and self-hosted runtimes.
package controlpanel

import (
	"context"
	"errors"
	"time"

	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
)

// Resolver is the small surface quant-handler needs from
// control-panel-service. Defining it as an interface lets tests inject a
// fake without dialing gRPC.
type Resolver interface {
	ListRuntimes(ctx context.Context, userID int64, statusFilter, sourceFilter string, limit, offset int) (RuntimeList, error)
	GetRuntime(ctx context.Context, userID int64, runtimeID string) (Runtime, error)
	EndRuntime(ctx context.Context, userID int64, runtimeID string) (Runtime, error)
	ListRuntimeAdmissionFailures(ctx context.Context, userID int64, limit int) ([]RuntimeAdmissionFailure, error)

	ResolveRouteByID(ctx context.Context, userID int64, runtimeID string, role string, mode int) (Route, error)
	PrepareDebugWorkspace(ctx context.Context, userID int64, runtimeID, hostPath, containerPath string) (DebugWorkspaceState, error)
	LoadDebugDataset(ctx context.Context, args LoadDebugDatasetArgs) (DebugDatasetState, error)
	GetRuntimeDebugDataset(ctx context.Context, userID int64, runtimeID string) (DebugDatasetState, error)

	// EnsureHostedRuntime is the explicit hosted-runtime creation entry point.
	// Empty name is allowed; control-panel-service may auto-generate one.
	EnsureHostedRuntime(ctx context.Context, userID int64, name, resourceProfile string) (EnsureResult, error)
}

// Route is the resolved runtime metadata. Endpoint/token fields are retained
// only for compatibility/debug visibility; they are not routing authority for
// normal strategy session traffic.
// Mirrors ResolveRuntimeRouteResponse but stays insulated from the proto type
// so the rest of the handler doesn't import controlpanelv1.
type Route struct {
	RuntimeID            string
	Name                 string
	Source               string
	GRPCEndpoint         string
	DebugEndpoint        string
	CallerToken          string
	CallerTokenExpiresAt time.Time
}

type Runtime struct {
	RuntimeID                  string
	UserID                     int64
	Name                       string
	Source                     string
	Role                       string
	EndpointHost               string
	GRPCPort                   int32
	DebugPort                  int32
	Capabilities               []string
	ResourceProfile            string
	Version                    string
	Status                     string
	CredentialKeyID            string
	PairedAt                   time.Time
	StartedAt                  time.Time
	EndedAt                    time.Time
	EndedReason                string
	CleanupStatus              string
	CleanupReason              string
	CleanupAt                  time.Time
	HeartbeatAt                time.Time
	ConnectionOwnerInstanceID  string
	ConnectionOwnerAcquiredAt  time.Time
	ConnectionOwnerHeartbeatAt time.Time
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	DebugWorkspace             *DebugWorkspaceState
	DebugDataset               *DebugDatasetState
}

type RuntimeList struct {
	Runtimes []Runtime
	HasMore  bool
	Total    int64
}

type RuntimeAdmissionFailure struct {
	AdmissionFailureID int64
	UserID             int64
	CredentialKeyID    string
	RequestedRuntimeID string
	RequestedName      string
	Source             string
	Role               string
	FailureCode        string
	Reason             string
	ConsumedRuntimeID  string
	FirstSeenAt        time.Time
	LastSeenAt         time.Time
	AttemptCount       int32
}

type DebugWorkspaceState struct {
	HostPath              string
	ContainerPath         string
	TemplatePath          string
	ArchivedTemplatePath  string
	VSCodeLaunchCreated   bool
	VSCodeLaunchPreserved bool
	PyCharmDocCreated     bool
	PyCharmDocPreserved   bool
	PreparedAt            time.Time
	LastError             string
}

type DebugDatasetState struct {
	DatasetID      string
	UserID         int64
	AccountID      int64
	RuntimeID      string
	Market         string
	Symbol         string
	Interval       string
	StartTimeMS    int64
	EndTimeMS      int64
	BarCount       int64
	CoverageStatus string
	LoadedAt       time.Time
	State          string
	LastError      string
}

type LoadDebugDatasetArgs struct {
	UserID      int64
	AccountID   int64
	RuntimeID   string
	Market      string
	Symbol      string
	Interval    string
	StartTimeMS int64
	EndTimeMS   int64
}

// EnsureResult is Route plus the `provisioned` flag. Handler logs use it
// to distinguish lazy first-touch (Provisioned=true) from steady-state
// reuse (Provisioned=false).
type EnsureResult struct {
	Route
	Provisioned bool
}

// ErrNotConfigured is returned from a no-op resolver when the operator did
// not configure dependencies.control_panel_service_grpc. Handlers should
// treat this as "feature unavailable" rather than a runtime error.
var ErrNotConfigured = errors.New("control-panel-service not configured")

// Client is the production Resolver backed by a gRPC connection.
type Client struct {
	rpc controlpanelv1.ControlPanelServiceClient
}

// NewClient returns a Resolver that dispatches to the supplied gRPC client.
func NewClient(rpc controlpanelv1.ControlPanelServiceClient) *Client {
	return &Client{rpc: rpc}
}

func runtimeFromProto(rt *controlpanelv1.Runtime) Runtime {
	if rt == nil {
		return Runtime{}
	}
	out := Runtime{
		RuntimeID:                 rt.GetRuntimeId(),
		UserID:                    rt.GetUserId(),
		Name:                      rt.GetName(),
		Source:                    rt.GetSource(),
		Role:                      rt.GetRole(),
		EndpointHost:              rt.GetEndpointHost(),
		GRPCPort:                  rt.GetGrpcPort(),
		DebugPort:                 rt.GetDebugPort(),
		Capabilities:              append([]string(nil), rt.GetCapabilities()...),
		ResourceProfile:           rt.GetResourceProfile(),
		Version:                   rt.GetVersion(),
		Status:                    rt.GetStatus(),
		CredentialKeyID:           rt.GetCredentialKeyId(),
		EndedReason:               rt.GetEndedReason(),
		CleanupStatus:             rt.GetCleanupStatus(),
		CleanupReason:             rt.GetCleanupReason(),
		ConnectionOwnerInstanceID: rt.GetConnectionOwnerInstanceId(),
	}
	if ts := rt.GetPairedAt(); ts != nil && ts.IsValid() {
		out.PairedAt = ts.AsTime()
	}
	if ts := rt.GetHeartbeatAt(); ts != nil && ts.IsValid() {
		out.HeartbeatAt = ts.AsTime()
	}
	if ts := rt.GetStartedAt(); ts != nil && ts.IsValid() {
		out.StartedAt = ts.AsTime()
	}
	if ts := rt.GetEndedAt(); ts != nil && ts.IsValid() {
		out.EndedAt = ts.AsTime()
	}
	if ts := rt.GetCleanupAt(); ts != nil && ts.IsValid() {
		out.CleanupAt = ts.AsTime()
	}
	if ts := rt.GetCreatedAt(); ts != nil && ts.IsValid() {
		out.CreatedAt = ts.AsTime()
	}
	if ts := rt.GetUpdatedAt(); ts != nil && ts.IsValid() {
		out.UpdatedAt = ts.AsTime()
	}
	if ts := rt.GetConnectionOwnerAcquiredAt(); ts != nil && ts.IsValid() {
		out.ConnectionOwnerAcquiredAt = ts.AsTime()
	}
	if ts := rt.GetConnectionOwnerHeartbeatAt(); ts != nil && ts.IsValid() {
		out.ConnectionOwnerHeartbeatAt = ts.AsTime()
	}
	if ws := debugWorkspaceFromProto(rt.GetDebugWorkspace()); ws != nil {
		out.DebugWorkspace = ws
	}
	if ds := debugDatasetFromProto(rt.GetDebugDataset()); ds != nil {
		out.DebugDataset = ds
	}
	return out
}

func debugWorkspaceFromProto(ws *controlpanelv1.DebugWorkspaceState) *DebugWorkspaceState {
	if ws == nil {
		return nil
	}
	out := &DebugWorkspaceState{
		HostPath:              ws.GetHostPath(),
		ContainerPath:         ws.GetContainerPath(),
		TemplatePath:          ws.GetTemplatePath(),
		ArchivedTemplatePath:  ws.GetArchivedTemplatePath(),
		VSCodeLaunchCreated:   ws.GetVscodeLaunchCreated(),
		VSCodeLaunchPreserved: ws.GetVscodeLaunchPreserved(),
		PyCharmDocCreated:     ws.GetPycharmDocCreated(),
		PyCharmDocPreserved:   ws.GetPycharmDocPreserved(),
		LastError:             ws.GetLastError(),
	}
	if ws.GetPreparedAtMs() > 0 {
		out.PreparedAt = time.UnixMilli(ws.GetPreparedAtMs()).UTC()
	}
	return out
}

func debugDatasetFromProto(ds *controlpanelv1.DebugDatasetState) *DebugDatasetState {
	if ds == nil {
		return nil
	}
	out := &DebugDatasetState{
		DatasetID:      ds.GetDatasetId(),
		UserID:         ds.GetUserId(),
		AccountID:      ds.GetAccountId(),
		RuntimeID:      ds.GetRuntimeId(),
		Market:         ds.GetMarket(),
		Symbol:         ds.GetSymbol(),
		Interval:       ds.GetInterval(),
		StartTimeMS:    ds.GetStartTimeMs(),
		EndTimeMS:      ds.GetEndTimeMs(),
		BarCount:       ds.GetBarCount(),
		CoverageStatus: ds.GetCoverageStatus(),
		State:          ds.GetState(),
		LastError:      ds.GetLastError(),
	}
	if ds.GetLoadedAtMs() > 0 {
		out.LoadedAt = time.UnixMilli(ds.GetLoadedAtMs()).UTC()
	}
	return out
}

func routeFromResponse(resp *controlpanelv1.ResolveRuntimeRouteResponse) Route {
	r := Route{
		GRPCEndpoint:  resp.GetGrpcEndpoint(),
		DebugEndpoint: resp.GetDebugEndpoint(),
		CallerToken:   resp.GetCallerToken(),
	}
	if rt := resp.GetRuntime(); rt != nil {
		r.RuntimeID = rt.GetRuntimeId()
		r.Name = rt.GetName()
		r.Source = rt.GetSource()
	}
	if ts := resp.GetCallerTokenExpiresAt(); ts != nil && ts.IsValid() {
		r.CallerTokenExpiresAt = ts.AsTime()
	}
	return r
}

func (c *Client) ListRuntimes(ctx context.Context, userID int64, statusFilter, sourceFilter string, limit, offset int) (RuntimeList, error) {
	resp, err := c.rpc.ListRuntimes(ctx, &controlpanelv1.ListRuntimesRequest{
		UserId: userID,
		Status: statusFilter,
		Source: sourceFilter,
		Limit:  int32(limit),
		Offset: int32(offset),
	})
	if err != nil {
		return RuntimeList{}, err
	}
	out := RuntimeList{
		Runtimes: make([]Runtime, 0, len(resp.GetRuntimes())),
		HasMore:  resp.GetHasMore(),
		Total:    resp.GetTotal(),
	}
	for _, rt := range resp.GetRuntimes() {
		out.Runtimes = append(out.Runtimes, runtimeFromProto(rt))
	}
	return out, nil
}

func (c *Client) GetRuntime(ctx context.Context, userID int64, runtimeID string) (Runtime, error) {
	resp, err := c.rpc.GetRuntime(ctx, &controlpanelv1.GetRuntimeRequest{
		UserId:    userID,
		RuntimeId: runtimeID,
	})
	if err != nil {
		return Runtime{}, err
	}
	return runtimeFromProto(resp.GetRuntime()), nil
}

func (c *Client) EndRuntime(ctx context.Context, userID int64, runtimeID string) (Runtime, error) {
	resp, err := c.rpc.EndRuntime(ctx, &controlpanelv1.EndRuntimeRequest{
		UserId:    userID,
		RuntimeId: runtimeID,
	})
	if err != nil {
		return Runtime{}, err
	}
	return runtimeFromProto(resp.GetRuntime()), nil
}

func admissionFailureFromProto(f *controlpanelv1.RuntimeAdmissionFailure) RuntimeAdmissionFailure {
	if f == nil {
		return RuntimeAdmissionFailure{}
	}
	out := RuntimeAdmissionFailure{
		AdmissionFailureID: f.GetAdmissionFailureId(),
		UserID:             f.GetUserId(),
		CredentialKeyID:    f.GetCredentialKeyId(),
		RequestedRuntimeID: f.GetRequestedRuntimeId(),
		RequestedName:      f.GetRequestedName(),
		Source:             f.GetSource(),
		Role:               f.GetRole(),
		FailureCode:        f.GetFailureCode(),
		Reason:             f.GetReason(),
		ConsumedRuntimeID:  f.GetConsumedRuntimeId(),
		AttemptCount:       f.GetAttemptCount(),
	}
	if ts := f.GetFirstSeenAt(); ts != nil && ts.IsValid() {
		out.FirstSeenAt = ts.AsTime()
	}
	if ts := f.GetLastSeenAt(); ts != nil && ts.IsValid() {
		out.LastSeenAt = ts.AsTime()
	}
	return out
}

func (c *Client) ListRuntimeAdmissionFailures(ctx context.Context, userID int64, limit int) ([]RuntimeAdmissionFailure, error) {
	resp, err := c.rpc.ListRuntimeAdmissionFailures(ctx, &controlpanelv1.ListRuntimeAdmissionFailuresRequest{
		UserId: userID,
		Limit:  int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]RuntimeAdmissionFailure, 0, len(resp.GetFailures()))
	for _, f := range resp.GetFailures() {
		out = append(out, admissionFailureFromProto(f))
	}
	return out, nil
}

func (c *Client) ResolveRouteByID(ctx context.Context, userID int64, runtimeID string, role string, mode int) (Route, error) {
	resp, err := c.rpc.ResolveRuntimeRouteByID(ctx, &controlpanelv1.ResolveRuntimeRouteByIDRequest{
		UserId:    userID,
		RuntimeId: runtimeID,
		Role:      role,
		Mode:      int32(mode),
	})
	if err != nil {
		return Route{}, err
	}
	return routeFromResponse(resp), nil
}

func (c *Client) PrepareDebugWorkspace(ctx context.Context, userID int64, runtimeID, hostPath, containerPath string) (DebugWorkspaceState, error) {
	resp, err := c.rpc.PrepareDebugWorkspace(ctx, &controlpanelv1.PrepareDebugWorkspaceRequest{
		UserId:        userID,
		RuntimeId:     runtimeID,
		HostPath:      hostPath,
		ContainerPath: containerPath,
	})
	if err != nil {
		return DebugWorkspaceState{}, err
	}
	ws := debugWorkspaceFromProto(resp.GetWorkspace())
	if ws == nil {
		return DebugWorkspaceState{}, nil
	}
	return *ws, nil
}

func (c *Client) LoadDebugDataset(ctx context.Context, args LoadDebugDatasetArgs) (DebugDatasetState, error) {
	resp, err := c.rpc.LoadDebugDataset(ctx, &controlpanelv1.LoadDebugDatasetRequest{
		UserId:      args.UserID,
		RuntimeId:   args.RuntimeID,
		AccountId:   args.AccountID,
		Market:      args.Market,
		Symbol:      args.Symbol,
		Interval:    args.Interval,
		StartTimeMs: args.StartTimeMS,
		EndTimeMs:   args.EndTimeMS,
	})
	if err != nil {
		return DebugDatasetState{}, err
	}
	ds := debugDatasetFromProto(resp.GetDataset())
	if ds == nil {
		return DebugDatasetState{}, nil
	}
	return *ds, nil
}

func (c *Client) GetRuntimeDebugDataset(ctx context.Context, userID int64, runtimeID string) (DebugDatasetState, error) {
	resp, err := c.rpc.GetRuntimeDebugDataset(ctx, &controlpanelv1.GetRuntimeDebugDatasetRequest{
		UserId:    userID,
		RuntimeId: runtimeID,
	})
	if err != nil {
		return DebugDatasetState{}, err
	}
	ds := debugDatasetFromProto(resp.GetDataset())
	if ds == nil {
		return DebugDatasetState{}, nil
	}
	return *ds, nil
}

// EnsureHostedRuntime calls control-panel's EnsureHostedRuntime and
// reshapes the response. See `ResolveRouteByID` for error semantics: gRPC
// errors are returned verbatim so callers map them via grpcToHTTP.
func (c *Client) EnsureHostedRuntime(ctx context.Context, userID int64, name, resourceProfile string) (EnsureResult, error) {
	resp, err := c.rpc.EnsureHostedRuntime(ctx, &controlpanelv1.EnsureHostedRuntimeRequest{
		UserId:          userID,
		Name:            name,
		ResourceProfile: resourceProfile,
	})
	if err != nil {
		return EnsureResult{}, err
	}
	r := EnsureResult{
		Route: Route{
			GRPCEndpoint:  resp.GetGrpcEndpoint(),
			DebugEndpoint: resp.GetDebugEndpoint(),
			CallerToken:   resp.GetCallerToken(),
		},
		Provisioned: resp.GetProvisioned(),
	}
	if rt := resp.GetRuntime(); rt != nil {
		r.RuntimeID = rt.GetRuntimeId()
		r.Name = rt.GetName()
		r.Source = rt.GetSource()
	}
	if ts := resp.GetCallerTokenExpiresAt(); ts != nil && ts.IsValid() {
		r.CallerTokenExpiresAt = ts.AsTime()
	}
	return r, nil
}

// notConfigured is the fallback Resolver returned when the handler has no
// configured control-panel-service address. Every call returns
// ErrNotConfigured so feature-flagged endpoints can produce a stable,
// machine-readable signal without panicking on a nil client.
type notConfigured struct{}

func (notConfigured) ListRuntimes(_ context.Context, _ int64, _, _ string, _, _ int) (RuntimeList, error) {
	return RuntimeList{}, ErrNotConfigured
}

func (notConfigured) GetRuntime(_ context.Context, _ int64, _ string) (Runtime, error) {
	return Runtime{}, ErrNotConfigured
}

func (notConfigured) EndRuntime(_ context.Context, _ int64, _ string) (Runtime, error) {
	return Runtime{}, ErrNotConfigured
}

func (notConfigured) ListRuntimeAdmissionFailures(_ context.Context, _ int64, _ int) ([]RuntimeAdmissionFailure, error) {
	return nil, ErrNotConfigured
}

func (notConfigured) ResolveRouteByID(_ context.Context, _ int64, _ string, _ string, _ int) (Route, error) {
	return Route{}, ErrNotConfigured
}

func (notConfigured) PrepareDebugWorkspace(_ context.Context, _ int64, _, _, _ string) (DebugWorkspaceState, error) {
	return DebugWorkspaceState{}, ErrNotConfigured
}

func (notConfigured) LoadDebugDataset(_ context.Context, _ LoadDebugDatasetArgs) (DebugDatasetState, error) {
	return DebugDatasetState{}, ErrNotConfigured
}

func (notConfigured) GetRuntimeDebugDataset(_ context.Context, _ int64, _ string) (DebugDatasetState, error) {
	return DebugDatasetState{}, ErrNotConfigured
}

func (notConfigured) EnsureHostedRuntime(_ context.Context, _ int64, _, _ string) (EnsureResult, error) {
	return EnsureResult{}, ErrNotConfigured
}

// Disabled returns a Resolver that fails every call with ErrNotConfigured.
func Disabled() Resolver { return notConfigured{} }
