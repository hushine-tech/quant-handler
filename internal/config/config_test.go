package config

import "testing"

func TestApplyEnvOverridesUsesCoreServiceGRPCAddr(t *testing.T) {
	t.Setenv("CORE_SERVICE_GRPC_ADDR", "core.internal:50051")

	cfg := Default()
	cfg.ApplyEnvOverrides()

	if got := cfg.Dependencies.PortfolioServiceGRPC; got != "core.internal:50051" {
		t.Fatalf("PortfolioServiceGRPC = %q, want core service addr", got)
	}
}

func TestDefaultAllowsBothDocumentedLocalFrontendOrigins(t *testing.T) {
	want := map[string]bool{
		"http://localhost:5173": true,
		"http://127.0.0.1:5173": true,
	}
	for _, origin := range Default().Auth.CORSOrigins {
		delete(want, origin)
	}
	if len(want) != 0 {
		t.Fatalf("default CORS origins are missing documented local origins: %v", want)
	}
}
