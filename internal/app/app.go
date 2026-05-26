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
	"github.com/hushine-tech/core-service/gen/accountv1"
	orderv1 "github.com/hushine-tech/core-service/gen/orderv1"
	grpcclientmw "github.com/hushine-tech/golang-lib/middleware/grpcclient"
	httpmw "github.com/hushine-tech/golang-lib/middleware/httpserver"
	cerrors "github.com/hushine-tech/golang-lib/pkg/errors"
	"github.com/hushine-tech/quant-handler/internal/config"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	"github.com/hushine-tech/quant-handler/internal/logger"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
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
	grpcTarget := cfg.Dependencies.AccountServiceGRPC
	if grpcTarget == "" {
		return errors.New("dependencies.account_service_grpc is required")
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

	cli := accountv1.NewAccountServiceClient(conn)

	// strategy-service（可选）
	var strategyCli strategyv1.StrategyServiceClient
	if strategyAddr := cfg.Dependencies.StrategyServiceGRPC; strategyAddr != "" {
		stratConn, err := grpc.DialContext(ctx, strategyAddr, dialOpts...)
		if err != nil {
			logger.Info(ctx, "system", fmt.Sprintf("strategy-service dial failed: %v (strategy endpoints disabled)", err))
		} else {
			strategyCli = strategyv1.NewStrategyServiceClient(stratConn)
			logger.Info(ctx, "system", fmt.Sprintf("strategy-service → %s", strategyAddr))
		}
	}

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

	// control-panel-service: D1a route resolution + D2 market-data control plane.
	// Market-data RPCs are mandatory once D2 lands; if the dial fails the
	// market-data endpoints fail-closed at request time.
	var controlPanel controlpanel.Resolver = controlpanel.Disabled()
	var marketDataCli mdv1.MarketDataControlPlaneServiceClient
	var cpRuntimeCli controlpanelv1.ControlPanelServiceClient
	if cpAddr := cfg.Dependencies.ControlPanelServiceGRPC; cpAddr != "" {
		cpConn, err := grpc.DialContext(ctx, cpAddr, dialOpts...)
		if err != nil {
			logger.Info(ctx, "system", fmt.Sprintf("control-panel-service dial failed: %v (route resolution + market-data disabled)", err))
		} else {
			controlPanel = controlpanel.NewClient(controlpanelv1.NewControlPanelServiceClient(cpConn))
			marketDataCli = mdv1.NewMarketDataControlPlaneServiceClient(cpConn)
			cpRuntimeCli = controlpanelv1.NewControlPanelServiceClient(cpConn)
			logger.Info(ctx, "system", fmt.Sprintf("control-panel-service → %s (feature flag=%t, market-data=on, credentials=on)", cpAddr, cfg.Features.ControlPanelRouteResolution))
		}
	}

	corsOrigins := cfg.Auth.CORSOrigins
	if len(corsOrigins) == 0 {
		corsOrigins = []string{"http://localhost:5173"}
	}

	s := &server{
		accounts:                 cli,
		strategy:                 strategyCli,
		orders:                   orderCli,
		controlPanel:             controlPanel,
		cpRuntime:                cpRuntimeCli,
		marketData:               marketDataCli,
		controlPanelRouteFeature: cfg.Features.ControlPanelRouteResolution,
		runtimeDialer:            newRuntimeDialer(defaultRuntimeDialOptions()...),
		downloadRunJobs:          newDownloadRunJobStore(),
		jwtSecret:                []byte(jwtSecret),
		corsOrigins:              corsOrigins,
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
	mux.Handle("/api/accounts", s.cors(s.auth(s.handleAccountsCollection())))
	mux.HandleFunc("/api/accounts/", s.cors(s.auth(s.handleAccountsByID())).ServeHTTP)
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
	accounts                 accountv1.AccountServiceClient
	strategy                 strategyv1.StrategyServiceClient         // nil if not configured
	orders                   orderv1.OrderServiceClient               // nil if not configured
	controlPanel             controlpanel.Resolver                    // never nil; Disabled() when not configured
	cpRuntime                controlpanelv1.ControlPanelServiceClient // Phase D3: direct gRPC client for credential RPCs; nil if CP not configured
	marketData               mdv1.MarketDataControlPlaneServiceClient // Phase D2: market-data control plane on control-panel-service
	controlPanelRouteFeature bool                                     // gates /api/_debug/runtime-route AND section-6 strategy cutover
	runtimeDialer            *runtimeDialer                           // dial cache for strategy-runtime endpoints (D1 section 6)
	downloadRunJobs          *downloadRunJobStore
	jwtSecret                []byte
	corsOrigins              []string
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
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	CreatedAt string `json:"created_at"`
}

func protoUserToJSON(user *accountv1.User) authUserJSON {
	if user == nil {
		return authUserJSON{}
	}
	createdAt := ""
	if ts := user.GetCreatedAt(); ts != nil && ts.IsValid() {
		createdAt = ts.AsTime().UTC().Format(time.RFC3339Nano)
	}
	return authUserJSON{
		ID:        user.GetId(),
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
	resp, err := s.accounts.CreateUser(r.Context(), &accountv1.CreateUserRequest{
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
	resp, err := s.accounts.VerifyUserPassword(r.Context(), &accountv1.VerifyUserPasswordRequest{
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

func (s *server) handleAccountsCollection() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.listAccounts(w, r)
		case http.MethodPost:
			s.createAccountWithBootstrap(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *server) handleAccountsByID() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		suffix := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
		suffix = strings.Trim(suffix, "/")
		parts := strings.Split(suffix, "/")
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		rawID := strings.TrimSpace(parts[0])
		id, parseErr := strconv.ParseInt(rawID, 10, 64)
		if parseErr != nil {
			writeErr(w, http.StatusBadRequest, "account_id must be an integer")
			return
		}
		if len(parts) == 1 {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.getAccount(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "wallet" {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.getWallet(w, r, id)
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
			s.handleAccountDebugDataset(w, r, id)
			return
		}
		if len(parts) == 2 && parts[1] == "debug-package" {
			s.handleAccountDebugPackage(w, r, id)
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
			s.handleAccountStrategies(w, r, id, rest)
			return
		}
		http.NotFound(w, r)
	})
}

type accountJSON struct {
	AccountID   int64  `json:"account_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Mode        int32  `json:"mode"`
	CreatedAt   string `json:"created_at"`
}

func (s *server) listAccounts(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	limit, offset := parseCollectionPaging(r)
	page := collectionPageRequested(r)
	ctx := r.Context()
	req := &accountv1.ListAccountsRequest{UserId: uid}
	if page {
		req.Limit = limit
		req.Offset = offset
	}
	resp, err := s.accounts.ListAccounts(ctx, req)
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	out := make([]accountJSON, 0, len(resp.GetAccounts()))
	for _, a := range resp.GetAccounts() {
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

func (s *server) getAccount(w http.ResponseWriter, r *http.Request, id int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	ctx := r.Context()
	resp, err := s.accounts.GetAccount(ctx, &accountv1.GetAccountRequest{AccountId: id, UserId: uid})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	if resp.GetAccount() == nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	writeJSON(w, http.StatusOK, registryEntryToJSON(resp.GetAccount()))
}

func registryEntryToJSON(e *accountv1.AccountRegistryEntry) accountJSON {
	return accountJSON{
		AccountID:   e.GetAccountId(),
		Name:        e.GetName(),
		Description: e.GetDescription(),
		Mode:        e.GetMode(),
		CreatedAt:   e.GetCreatedAt().AsTime().UTC().Format(time.RFC3339Nano),
	}
}

func grpcToHTTP(err error) (int, string) {
	// Check for CommonError first (grpcclient interceptor converts gRPC errors to CommonError)
	if httpStatus := cerrors.HTTPStatus(err); httpStatus != 0 {
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
		return http.StatusBadGateway, st.Message()
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
