package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestApplyEnvOverridesUsesCanonicalNames(t *testing.T) {
	t.Setenv("SERVER_HTTP_ADDR", ":18090")
	t.Setenv("DEPENDENCIES_CORE_SERVICE_GRPC", "core.internal:50051")
	t.Setenv("DEPENDENCIES_ORDER_SERVICE_GRPC", "orders.internal:50051")

	cfg := Default()
	if err := cfg.ApplyEnvOverrides(); err != nil {
		t.Fatal(err)
	}

	if got := cfg.Dependencies.PortfolioServiceGRPC; got != "core.internal:50051" {
		t.Fatalf("PortfolioServiceGRPC = %q, want core service addr", got)
	}
	if got := cfg.Dependencies.OrderServiceGRPC; got != "orders.internal:50051" {
		t.Fatalf("OrderServiceGRPC = %q, want order service addr", got)
	}
	if cfg.Server.HTTPAddr != ":18090" {
		t.Fatalf("HTTPAddr = %q, want canonical server addr", cfg.Server.HTTPAddr)
	}
}

func TestApplyEnvOverridesIgnoresRemovedAliases(t *testing.T) {
	t.Setenv("HTTP_ADDR", "legacy-http:1")
	t.Setenv("CORE_SERVICE_GRPC_ADDR", "legacy-core:2")
	t.Setenv("ORDER_SERVICE_GRPC_ADDR", "legacy-order:3")
	cfg := Default()
	if err := cfg.ApplyEnvOverrides(); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.HTTPAddr != ":8090" {
		t.Fatalf("HTTPAddr = %q, want canonical default", cfg.Server.HTTPAddr)
	}
	if cfg.Dependencies.PortfolioServiceGRPC != "127.0.0.1:50051" || cfg.Dependencies.OrderServiceGRPC != "127.0.0.1:50051" {
		t.Fatalf("dependencies = %+v, want canonical defaults", cfg.Dependencies)
	}
}

func TestLoadDocsConfigAndCanonicalEnvironmentOverrides(t *testing.T) {
	directory := t.TempDir()
	configFile := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(configFile, []byte(`
server:
  http_addr: ":8090"
docs:
  root: "/package/from-yaml"
  privileged_user_ids: [7]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Docs.Root != "/package/from-yaml" || !reflect.DeepEqual(cfg.Docs.PrivilegedUserIDs, []int64{7}) {
		t.Fatalf("YAML docs config = %+v", cfg.Docs)
	}

	t.Setenv("DOCS_ROOT", "/package/from-env")
	t.Setenv("DOCS_PRIVILEGED_USER_IDS", "11, 13")
	if err := cfg.ApplyEnvOverrides(); err != nil {
		t.Fatal(err)
	}
	if cfg.Docs.Root != "/package/from-env" || !reflect.DeepEqual(cfg.Docs.PrivilegedUserIDs, []int64{11, 13}) {
		t.Fatalf("environment docs config = %+v", cfg.Docs)
	}
}

func TestApplyEnvOverridesRejectsInvalidPrivilegedUserIDs(t *testing.T) {
	for _, value := range []string{"0", "-1", "abc", "1,,2", "1,1"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("DOCS_PRIVILEGED_USER_IDS", value)
			cfg := Default()
			if err := cfg.ApplyEnvOverrides(); err == nil {
				t.Fatalf("ApplyEnvOverrides accepted %q", value)
			}
		})
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
