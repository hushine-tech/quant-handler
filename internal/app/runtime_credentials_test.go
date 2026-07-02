package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	controlpanelv1 "github.com/hushine-tech/control-panel-service/gen/controlpanelv1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeRuntimeCredentialClient struct {
	controlpanelv1.ControlPanelServiceClient
	req *controlpanelv1.IssueRuntimeCredentialRequest
}

func (f *fakeRuntimeCredentialClient) IssueRuntimeCredential(_ context.Context, req *controlpanelv1.IssueRuntimeCredentialRequest, _ ...grpc.CallOption) (*controlpanelv1.IssueRuntimeCredentialResponse, error) {
	f.req = req
	return &controlpanelv1.IssueRuntimeCredentialResponse{
		KeyId:               "key-1",
		PrivateKeyPem:       "private-key",
		PublicKeyPem:        "public-key",
		CreatedAt:           timestamppb.New(time.Date(2026, 6, 21, 8, 0, 0, 0, time.UTC)),
		Role:                "executor",
		ClientCertPem:       "client-cert",
		ClientKeyPem:        "client-key",
		ServerCaPem:         "server-ca",
		ClientCertExpiresAt: timestamppb.New(time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)),
	}, nil
}

func TestIssueRuntimeCredentialReturnsTLSBundle(t *testing.T) {
	fake := &fakeRuntimeCredentialClient{}
	s := &server{cpRuntime: fake, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}

	req := withUID(httptest.NewRequest(http.MethodPost, "/api/runtime-credentials", nil), 42)
	rec := httptest.NewRecorder()
	s.handleRuntimeCredentialsCollection(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fake.req == nil || fake.req.GetUserId() != 42 || fake.req.GetRole() != "executor" {
		t.Fatalf("IssueRuntimeCredential request = %+v, want user=42 role=executor", fake.req)
	}
	var body issueRuntimeCredentialResponseJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Version != 1 || body.KeyID != "key-1" || body.PrivateKeyPEM != "private-key" {
		t.Fatalf("basic credential body = %+v", body)
	}
	if body.ClientCertPEM != "client-cert" || body.ClientKeyPEM != "client-key" || body.ServerCAPEM != "server-ca" {
		t.Fatalf("tls bundle = cert:%q key:%q ca:%q", body.ClientCertPEM, body.ClientKeyPEM, body.ServerCAPEM)
	}
	if body.ClientCertExpiresAt != "2026-06-22T08:00:00Z" {
		t.Fatalf("client_cert_expires_at = %q", body.ClientCertExpiresAt)
	}
}
