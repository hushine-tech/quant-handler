package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
	"github.com/hushine-tech/core-service/gen/portfoliov1"
	grpcclientmw "github.com/hushine-tech/golang-lib/middleware/grpcclient"
	httpmw "github.com/hushine-tech/golang-lib/middleware/httpserver"
	cerrors "github.com/hushine-tech/golang-lib/pkg/errors"
	errorcodes "github.com/hushine-tech/golang-lib/pkg/errors/codes"
	"github.com/hushine-tech/quant-handler/internal/config"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	"github.com/hushine-tech/quant-handler/internal/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Run starts the HTTP server and blocks until SIGINT/SIGTERM (handled by process default).
func Run(cfg *config.Config) error {
	httpAddr := cfg.Server.HTTPAddr
	if httpAddr == "" {
		httpAddr = ":8090"
	}
	grpcTarget := cfg.Dependencies.PortfolioServiceGRPC
	if grpcTarget == "" {
		return errors.New("dependencies.portfolio_service_grpc is required")
	}
	jwtSecret := cfg.Auth.JWTSecret
	if jwtSecret == "" {
		return errors.New("auth.jwt_secret is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	logInstance := logger.Instance()

	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(grpcclientmw.UnaryClientInterceptor(logInstance)),
		grpc.WithStreamInterceptor(grpcclientmw.StreamClientInterceptor(logInstance)),
	}
	conn, err := grpc.DialContext(ctx, grpcTarget, dialOpts...)
	if err != nil {
		return fmt.Errorf("grpc dial %q: %w", grpcTarget, err)
	}
	defer conn.Close()

	cli := portfoliov1.NewPortfolioServiceClient(conn)

	// order.v1 API（可选，当前由 core-service gRPC 端口提供）
	var orderCli orderv1.OrderServiceClient
	if orderAddr := cfg.Dependencies.OrderServiceGRPC; orderAddr != "" {
		orderConn, err := grpc.DialContext(ctx, orderAddr, dialOpts...)
		if err != nil {
			logger.Info(ctx, "system", fmt.Sprintf("order API dial failed: %v (order API endpoints disabled)", err))
		} else {
			orderCli = orderv1.NewOrderServiceClient(orderConn)
			logger.Info(ctx, "system", fmt.Sprintf("order.v1 API → %s", orderAddr))
		}
	}

	// control-panel-service owns runtime routing, hosted runtime provisioning,
	// RuntimeChannel strategy proxying, runtime credentials, and market-data.
	if cfg.Dependencies.ControlPanelServiceGRPC == "" {
		return errors.New("dependencies.control_panel_service_grpc is required")
	}
	var controlPanel controlpanel.Resolver
	var marketDataCli mdv1.MarketDataControlPlaneServiceClient
	var cpRuntimeCli controlpanelv1.ControlPanelServiceClient
	cpAddr := cfg.Dependencies.ControlPanelServiceGRPC
	cpConn, err := grpc.DialContext(ctx, cpAddr, dialOpts...)
	if err != nil {
		return fmt.Errorf("control-panel-service grpc dial %q: %w", cpAddr, err)
	}
	controlPanel = controlpanel.NewClient(controlpanelv1.NewControlPanelServiceClient(cpConn))
	marketDataCli = mdv1.NewMarketDataControlPlaneServiceClient(cpConn)
	cpRuntimeCli = controlpanelv1.NewControlPanelServiceClient(cpConn)
	logger.Info(ctx, "system", fmt.Sprintf("control-panel-service → %s (runtime-proxy=on, market-data=on, credentials=on)", cpAddr))

	corsOrigins := cfg.Auth.CORSOrigins
	if len(corsOrigins) == 0 {
		corsOrigins = []string{"http://localhost:5173"}
	}

	s := &server{
		portfolios:      cli,
		orders:          orderCli,
		controlPanel:    controlPanel,
		cpRuntime:       cpRuntimeCli,
		marketData:      marketDataCli,
		downloadRunJobs: newDownloadRunJobStore(),
		jwtSecret:       []byte(jwtSecret),
		corsOrigins:     corsOrigins,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.Handle("/api/auth/signup", s.cors(http.HandlerFunc(s.handleSignup)))
	mux.Handle("/api/auth/login", s.cors(http.HandlerFunc(s.handleLogin)))
	mux.Handle("/api/symbols", s.cors(s.auth(http.HandlerFunc(s.handleSymbols))))
	mux.Handle("/api/portfolios", s.cors(s.auth(s.handlePortfoliosCollection())))
	mux.HandleFunc("/api/portfolios/", s.cors(s.auth(s.handlePortfoliosByID())).ServeHTTP)
	mux.HandleFunc("/api/venues", s.cors(s.auth(http.HandlerFunc(s.handleVenues))).ServeHTTP)
	mux.HandleFunc("/api/venues/", s.cors(s.auth(http.HandlerFunc(s.handleVenueByID))).ServeHTTP)
	mux.HandleFunc("/api/strategy-sessions/", s.cors(s.auth(http.HandlerFunc(s.handleStrategySession))).ServeHTTP)
	mux.HandleFunc("/api/strategy/download-and-run-jobs/", s.cors(s.auth(http.HandlerFunc(s.handleDownloadRunJobStatus))).ServeHTTP)
	mux.HandleFunc("/api/strategies", s.cors(s.auth(http.HandlerFunc(s.handleStrategiesCollection))).ServeHTTP)
	mux.HandleFunc("/api/strategies/", s.cors(s.auth(http.HandlerFunc(s.handleStrategiesByID))).ServeHTTP)
	mux.HandleFunc("/api/orders", s.cors(s.auth(http.HandlerFunc(s.handleOrders))).ServeHTTP)
	mux.HandleFunc("/api/orders/", s.cors(s.auth(http.HandlerFunc(s.handleOrders))).ServeHTTP)
	mux.HandleFunc("/api/sessions", s.cors(s.auth(http.HandlerFunc(s.handleSessions))).ServeHTTP)
	mux.HandleFunc("/api/sessions/", s.cors(s.auth(http.HandlerFunc(s.handleSessions))).ServeHTTP)
	mux.HandleFunc("/api/market-data/", s.cors(s.auth(http.HandlerFunc(s.handleMarketData))).ServeHTTP)
	mux.HandleFunc("/api/runtimes", s.cors(s.auth(http.HandlerFunc(s.handleRuntimesCollection))).ServeHTTP)
	mux.HandleFunc("/api/runtimes/", s.cors(s.auth(http.HandlerFunc(s.handleRuntimeByID))).ServeHTTP)
	mux.HandleFunc("/api/runtime-admission-failures", s.cors(s.auth(http.HandlerFunc(s.handleRuntimeAdmissionFailures))).ServeHTTP)
	mux.HandleFunc("/api/notifications", s.cors(s.auth(http.HandlerFunc(s.handleNotifications))).ServeHTTP)
	mux.HandleFunc("/api/notifications/", s.cors(s.auth(http.HandlerFunc(s.handleNotifications))).ServeHTTP)
	// Phase D3: runtime credentials (settings → keypair issue / list / revoke)
	mux.HandleFunc("/api/runtime-credentials", s.cors(s.auth(http.HandlerFunc(s.handleRuntimeCredentialsCollection))).ServeHTTP)
	mux.HandleFunc("/api/runtime-credentials/", s.cors(s.auth(http.HandlerFunc(s.handleRuntimeCredentialsByID))).ServeHTTP)

	// Wrap mux with golang-lib httpserver middleware (outermost layer)
	// Provides: access log, session_id generation, panic recovery
	handler := httpmw.Middleware(logInstance)(mux)

	logger.Info(ctx, "system", fmt.Sprintf("quant-handler http server listening on %s", httpAddr))
	return http.ListenAndServe(httpAddr, handler)
}

type server struct {
	portfolios      portfoliov1.PortfolioServiceClient
	orders          orderv1.OrderServiceClient // nil if not configured
	controlPanel    controlpanel.Resolver
	cpRuntime       controlpanelv1.ControlPanelServiceClient // Phase D3: direct gRPC client for credential RPCs; nil if CP not configured
	marketData      mdv1.MarketDataControlPlaneServiceClient // Phase D2: market-data control plane on control-panel-service
	downloadRunJobs *downloadRunJobStore
	jwtSecret       []byte
	corsOrigins     []string
}

type authContextKey string

const userIDContextKey authContextKey = "user_id"

type authClaims struct {
	UID int64 `json:"uid"`
	jwt.RegisteredClaims
}

func (s *server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowOrigin(s.corsOrigins, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowOrigin(allowed []string, origin string) bool {
	if origin == "" {
		return false
	}
	for _, o := range allowed {
		if o == "*" {
			return true
		}
		if o == origin {
			return true
		}
	}
	return false
}

func (s *server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(h, prefix))
		if raw == "" {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims := &authClaims{}
		tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return s.jwtSecret, nil
		})
		if err != nil || !tok.Valid {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		if claims.UID <= 0 {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), userIDContextKey, claims.UID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type authBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authUserJSON struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	CreatedAt string `json:"created_at"`
}

func protoUserToJSON(user *portfoliov1.User) authUserJSON {
	if user == nil {
		return authUserJSON{}
	}
	createdAt := ""
	if ts := user.GetCreatedAt(); ts != nil && ts.IsValid() {
		createdAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	return authUserJSON{
		UserID:    user.GetId(),
		Username:  user.GetUsername(),
		CreatedAt: createdAt,
	}
}

func userIDFromContext(ctx context.Context) (int64, bool) {
	uid, ok := ctx.Value(userIDContextKey).(int64)
	return uid, ok && uid > 0
}

func userIDFromRequest(r *http.Request) (int64, bool) {
	return userIDFromContext(r.Context())
}

func (s *server) issueToken(userID int64) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, authClaims{
		UID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("user:%d", userID),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	})
	return tok.SignedString(s.jwtSecret)
}

func (s *server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body authBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := s.portfolios.CreateUser(r.Context(), &portfoliov1.CreateUserRequest{
		Username: body.Username,
		Password: body.Password,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"user": protoUserToJSON(resp.GetUser()),
	})
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body authBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := s.portfolios.VerifyUserPassword(r.Context(), &portfoliov1.VerifyUserPasswordRequest{
		Username: body.Username,
		Password: body.Password,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	if !resp.GetValid() || resp.GetUser() == nil || resp.GetUser().GetId() == 0 {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	signed, err := s.issueToken(resp.GetUser().GetId())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      signed,
		"expires_in": int((24 * time.Hour).Seconds()),
		"user":       protoUserToJSON(resp.GetUser()),
	})
}

func (s *server) handlePortfoliosCollection() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.listPortfolios(w, r)
		case http.MethodPost:
			s.createPortfolio(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *server) handlePortfoliosByID() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		suffix := strings.TrimPrefix(r.URL.Path, "/api/portfolios/")
		suffix = strings.Trim(suffix, "/")
		parts := strings.Split(suffix, "/")
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		rawID := strings.TrimSpace(parts[0])
		id, parseErr := strconv.ParseInt(rawID, 10, 64)
		if parseErr != nil {
			writeErr(w, http.StatusBadRequest, "portfolio_id must be an integer")
			return
		}
		if len(parts) == 1 {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.getPortfolio(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "portfolio-snapshot" {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.getPortfolioPortfolioSnapshot(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "venues" {
			s.handlePortfolioVenues(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "run-strategy" {
			s.handleRunStrategy(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "preview-run-strategy" {
			s.handlePreviewRunStrategy(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "debug-dataset" {
			s.handlePortfolioDebugDataset(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "debug-package" {
			s.handlePortfolioDebugPackage(w, r, id)
			return
		}
		if len(parts) == 3 && parts[1] == "strategy" && parts[2] == "coverage-preview" {
			s.handleCoveragePreview(w, r, id)
			return
		}
		if len(parts) == 3 && parts[1] == "strategy" && parts[2] == "download-and-run" {
			s.handleDownloadAndRun(w, r, id)
			return
		}
		if parts[1] == "strategies" {
			rest := ""
			if len(parts) > 2 {
				rest = strings.Join(parts[2:], "/")
			}
			s.handlePortfolioStrategies(w, r, id, rest)
			return
		}
		http.NotFound(w, r)
	})
}

type portfolioJSON struct {
	PortfolioID int64  `json:"portfolio_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Environment int32  `json:"environment"`
	CreatedAt   string `json:"created_at"`
}

func (s *server) listPortfolios(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	limit, offset := parseCollectionPaging(r)
	page := collectionPageRequested(r)
	ctx := r.Context()
	req := &portfoliov1.ListPortfoliosRequest{UserId: uid}
	if page {
		req.Limit = limit
		req.Offset = offset
	}
	resp, err := s.portfolios.ListPortfolios(ctx, req)
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	out := make([]portfolioJSON, 0, len(resp.GetPortfolios()))
	for _, a := range resp.GetPortfolios() {
		out = append(out, registryEntryToJSON(a))
	}
	if page {
		writeJSON(w, http.StatusOK, pagedResponse{
			Items:      out,
			NextOffset: offset + int32(len(out)),
			HasMore:    resp.GetHasMore(),
			Total:      resp.GetTotal(),
		})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getPortfolio(w http.ResponseWriter, r *http.Request, id int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	ctx := r.Context()
	resp, err := s.portfolios.GetPortfolio(ctx, &portfoliov1.GetPortfolioRequest{PortfolioId: id, UserId: uid})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	if resp.GetPortfolio() == nil {
		writeErr(w, http.StatusNotFound, "portfolio not found")
		return
	}
	writeJSON(w, http.StatusOK, registryEntryToJSON(resp.GetPortfolio()))
}

func registryEntryToJSON(e *portfoliov1.PortfolioRegistryEntry) portfolioJSON {
	return portfolioJSON{
		PortfolioID: e.GetPortfolioId(),
		Name:        e.GetName(),
		Description: e.GetDescription(),
		Environment: e.GetEnvironment(),
		CreatedAt:   e.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	}
}

func grpcToHTTP(err error) (int, string) {
	// Check for CommonError first (grpcclient interceptor converts gRPC errors to CommonError)
	if httpStatus := cerrors.HTTPStatus(err); httpStatus != 0 {
		if msg, ok := friendlyTransientRPCMessage(err); ok {
			return httpStatus, msg
		}
		if msg, ok := friendlyKnownBusinessErrorMessage(err); ok {
			return httpStatus, msg
		}
		return httpStatus, err.Error()
	}

	// Fallback: parse raw gRPC status (for errors that bypass the interceptor)
	st, ok := status.FromError(err)
	if !ok {
		return http.StatusBadGateway, err.Error()
	}
	switch st.Code() {
	case codes.NotFound:
		return http.StatusNotFound, st.Message()
	case codes.InvalidArgument:
		return http.StatusBadRequest, st.Message()
	case codes.Unavailable:
		return http.StatusBadGateway, friendlyBackendUnavailableMessage
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout, friendlyRuntimeTimeoutMessage
	case codes.Canceled:
		return http.StatusBadGateway, friendlyRuntimeCanceledMessage
	case codes.Internal:
		return http.StatusInternalServerError, st.Message()
	case codes.PermissionDenied:
		return http.StatusForbidden, st.Message()
	case codes.AlreadyExists:
		return http.StatusConflict, st.Message()
	case codes.FailedPrecondition:
		return http.StatusPreconditionFailed, st.Message()
	default:
		return http.StatusBadGateway, st.Message()
	}
}

const (
	friendlyRuntimeTimeoutMessage     = "Runtime did not respond in time. It may be starting, busy with another session, or temporarily unreachable; retry in a few seconds or choose another runtime."
	friendlyRuntimeCanceledMessage    = "Runtime request was canceled before it completed. Retry in a few seconds or choose another runtime."
	friendlyRuntimeUnavailableMessage = "Runtime is temporarily unavailable. Retry in a few seconds or choose another runtime."
	friendlyBackendUnavailableMessage = "Backend service is temporarily unavailable. Retry in a few seconds."
)

func grpcToHTTPRuntime(err error) (int, string) {
	code, msg := grpcToHTTP(err)
	if msg == friendlyBackendUnavailableMessage {
		return code, friendlyRuntimeUnavailableMessage
	}
	return code, msg
}

func friendlyTransientRPCMessage(err error) (string, bool) {
	if cerrors.Code(err) == errorcodes.Timeout {
		return friendlyRuntimeTimeoutMessage, true
	}
	if cerrors.HTTPStatus(err) == http.StatusServiceUnavailable {
		return friendlyBackendUnavailableMessage, true
	}
	return "", false
}

func friendlyKnownBusinessErrorMessage(err error) (string, bool) {
	raw := err.Error()
	if strings.Contains(raw, "ACTIVE_SESSION_EXISTS") {
		return "Another session is already running for this portfolio. Finish or stop the running session before starting a new one.", true
	}
	if idx := strings.Index(raw, "venue credential is invalid or lacks Binance futures account permission"); idx >= 0 {
		return raw[idx:], true
	}
	return "", false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
