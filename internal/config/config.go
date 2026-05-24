package config

import (
	"fmt"
	"os"
	"strings"

	elog "github.com/hushine-tech/golang-lib/pkg/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Dependencies DependenciesConfig `yaml:"dependencies"`
	Auth         AuthConfig         `yaml:"auth"`
	Features     FeaturesConfig     `yaml:"features"`
	Log          elog.Config        `yaml:"log"`
}

type ServerConfig struct {
	HTTPAddr string `yaml:"http_addr"`
}

type DependenciesConfig struct {
	AccountServiceGRPC      string `yaml:"account_service_grpc"`
	StrategyServiceGRPC     string `yaml:"strategy_service_grpc"`
	OrderServiceGRPC        string `yaml:"order_service_grpc"`
	ControlPanelServiceGRPC string `yaml:"control_panel_service_grpc"`
}

// FeaturesConfig holds operator-toggleable runtime routing flags.
type FeaturesConfig struct {
	// ControlPanelRouteResolution gates /api/_debug/runtime-route and strategy
	// run/preview/stop/status routing through control-panel-service. Hosted
	// and self-hosted runtimes both use the RuntimeChannel proxy path.
	ControlPanelRouteResolution bool `yaml:"control_panel_route_resolution"`
}

type AuthConfig struct {
	JWTSecret   string   `yaml:"jwt_secret"`
	CORSOrigins []string `yaml:"cors_origins"`
}

// Default returns a baseline config so env-driven deployments can still start
// when config.yaml is absent.
func Default() *Config {
	logCfg := elog.DefaultConfig()
	logCfg.OutputDir = "./logs"
	logCfg.Tracing.ServiceName = "quant-handler"
	if logCfg.Kafka.Topic == "" {
		logCfg.Kafka.Topic = "app-logs"
	}
	if logCfg.Kafka.TopicPrefix == "" {
		logCfg.Kafka.TopicPrefix = "app-logs"
	}
	return &Config{
		Server: ServerConfig{
			HTTPAddr: ":8090",
		},
		Dependencies: DependenciesConfig{
			AccountServiceGRPC:  "127.0.0.1:50051",
			StrategyServiceGRPC: "127.0.0.1:50053",
			OrderServiceGRPC:    "127.0.0.1:50051",
		},
		Auth: AuthConfig{
			CORSOrigins: []string{"http://localhost:5173"},
		},
		Log: *logCfg,
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("SERVER_HTTP_ADDR"); v != "" {
		c.Server.HTTPAddr = v
	} else if v := os.Getenv("HTTP_ADDR"); v != "" {
		c.Server.HTTPAddr = v
	}

	if v := os.Getenv("DEPENDENCIES_CORE_SERVICE_GRPC"); v != "" {
		c.Dependencies.AccountServiceGRPC = v
	} else if v := os.Getenv("CORE_SERVICE_GRPC_ADDR"); v != "" {
		c.Dependencies.AccountServiceGRPC = v
	} else if v := os.Getenv("DEPENDENCIES_ACCOUNT_SERVICE_GRPC"); v != "" {
		c.Dependencies.AccountServiceGRPC = v
	} else if v := os.Getenv("ACCOUNT_SERVICE_GRPC_ADDR"); v != "" {
		c.Dependencies.AccountServiceGRPC = v
	}
	if v := os.Getenv("DEPENDENCIES_STRATEGY_SERVICE_GRPC"); v != "" {
		c.Dependencies.StrategyServiceGRPC = v
	} else if v := os.Getenv("STRATEGY_SERVICE_GRPC_ADDR"); v != "" {
		c.Dependencies.StrategyServiceGRPC = v
	}
	if v := os.Getenv("DEPENDENCIES_ORDER_SERVICE_GRPC"); v != "" {
		c.Dependencies.OrderServiceGRPC = v
	} else if v := os.Getenv("ORDER_SERVICE_GRPC_ADDR"); v != "" {
		c.Dependencies.OrderServiceGRPC = v
	}
	if v := os.Getenv("DEPENDENCIES_CONTROL_PANEL_SERVICE_GRPC"); v != "" {
		c.Dependencies.ControlPanelServiceGRPC = v
	} else if v := os.Getenv("CONTROL_PANEL_SERVICE_GRPC_ADDR"); v != "" {
		c.Dependencies.ControlPanelServiceGRPC = v
	}

	if v := os.Getenv("FEATURES_CONTROL_PANEL_ROUTE_RESOLUTION"); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			c.Features.ControlPanelRouteResolution = true
		case "0", "false", "no", "off", "":
			c.Features.ControlPanelRouteResolution = false
		}
	}

	if v := os.Getenv("AUTH_JWT_SECRET"); v != "" {
		c.Auth.JWTSecret = v
	} else if v := os.Getenv("QUANT_HANDLER_JWT_SECRET"); v != "" {
		c.Auth.JWTSecret = v
	}
	if v := os.Getenv("AUTH_CORS_ORIGINS"); v != "" {
		c.Auth.CORSOrigins = splitCSV(v)
	} else if v := os.Getenv("HANDLER_CORS_ORIGINS"); v != "" {
		c.Auth.CORSOrigins = splitCSV(v)
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
