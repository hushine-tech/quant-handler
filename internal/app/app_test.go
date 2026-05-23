package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	cerrors "github.com/hushine-tech/golang-lib/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCORSPreflightBypassesAuth(t *testing.T) {
	s := &server{
		jwtSecret:   []byte("secret"),
		corsOrigins: []string{"http://localhost:5173"},
	}

	nextCalled := false
	handler := s.cors(s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodOptions, "/api/accounts", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if nextCalled {
		t.Fatal("preflight should not call downstream auth-wrapped handler")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("allow-origin = %q, want http://localhost:5173", got)
	}
}

func TestCORSWildcardAllowsRemoteOrigin(t *testing.T) {
	s := &server{
		jwtSecret:   []byte("secret"),
		corsOrigins: []string{"*"},
	}

	handler := s.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	req.Header.Set("Origin", "http://192.168.66.12:5173")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://192.168.66.12:5173" {
		t.Fatalf("allow-origin = %q, want remote origin echoed back", got)
	}
}

func TestAuthRejectsMissingBearerToken(t *testing.T) {
	s := &server{jwtSecret: []byte("secret")}
	handler := s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestGrpcToHTTPMapsCommonErrorFirst(t *testing.T) {
	err := cerrors.New(999001, http.StatusConflict, "duplicate strategy")

	code, msg := grpcToHTTP(err)

	if code != http.StatusConflict {
		t.Fatalf("code = %d, want %d", code, http.StatusConflict)
	}
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
}

func TestGrpcToHTTPMapsNotFound(t *testing.T) {
	err := status.Error(codes.NotFound, "missing account")

	code, msg := grpcToHTTP(err)

	if code != http.StatusNotFound {
		t.Fatalf("code = %d, want %d", code, http.StatusNotFound)
	}
	if msg != "missing account" {
		t.Fatalf("msg = %q, want %q", msg, "missing account")
	}
}
