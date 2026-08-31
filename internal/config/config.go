package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	elog "github.com/hushine-tech/golang-lib/pkg/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Dependencies  DependenciesConfig  `yaml:"dependencies"`
	Auth          AuthConfig          `yaml:"auth"`
	Docs          DocsConfig          `yaml:"docs"`
	DocsAssistant DocsAssistantConfig `yaml:"docs_assistant"`
	Log           elog.Config         `yaml:"log"`
}

type ServerConfig struct {
	HTTPAddr string `yaml:"http_addr"`
}

type DependenciesConfig struct {
	PortfolioServiceGRPC    string `yaml:"portfolio_service_grpc"`
	OrderServiceGRPC        string `yaml:"order_service_grpc"`
	ControlPanelServiceGRPC string `yaml:"control_panel_service_grpc"`
}

type AuthConfig struct {
	JWTSecret   string   `yaml:"jwt_secret"`
	CORSOrigins []string `yaml:"cors_origins"`
}

type DocsConfig struct {
	Root              string  `yaml:"root"`
	PrivilegedUserIDs []int64 `yaml:"privileged_user_ids"`
}

type DocsAssistantConfig struct {
	Enabled           bool   `yaml:"enabled"`
	APIKey            string `yaml:"-"`
	BaseURL           string `yaml:"base_url"`
	Model             string `yaml:"model"`
	RequestsPerMinute int    `yaml:"requests_per_minute"`
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
			PortfolioServiceGRPC: "127.0.0.1:50051",
			OrderServiceGRPC:     "127.0.0.1:50051",
		},
		Auth: AuthConfig{
			CORSOrigins: []string{
				"http://localhost:5173",
				"http://127.0.0.1:5173",
			},
		},
		DocsAssistant: DocsAssistantConfig{
			BaseURL:           "https://api.openai.com/v1",
			RequestsPerMinute: 6,
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
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) ApplyEnvOverrides() error {
	if v := os.Getenv("SERVER_HTTP_ADDR"); v != "" {
		c.Server.HTTPAddr = v
	}

	if v := os.Getenv("DEPENDENCIES_CORE_SERVICE_GRPC"); v != "" {
		c.Dependencies.PortfolioServiceGRPC = v
	}
	if v := os.Getenv("DEPENDENCIES_ORDER_SERVICE_GRPC"); v != "" {
		c.Dependencies.OrderServiceGRPC = v
	}
	if v := os.Getenv("DEPENDENCIES_CONTROL_PANEL_SERVICE_GRPC"); v != "" {
		c.Dependencies.ControlPanelServiceGRPC = v
	}

	if v := os.Getenv("AUTH_JWT_SECRET"); v != "" {
		c.Auth.JWTSecret = v
	}
	if v := os.Getenv("AUTH_CORS_ORIGINS"); v != "" {
		c.Auth.CORSOrigins = splitCSV(v)
	}
	if v := os.Getenv("DOCS_ROOT"); v != "" {
		c.Docs.Root = v
	}
	if raw, present := os.LookupEnv("DOCS_PRIVILEGED_USER_IDS"); present {
		values, err := parsePositiveUniqueIDs(raw)
		if err != nil {
			return fmt.Errorf("DOCS_PRIVILEGED_USER_IDS: %w", err)
		}
		c.Docs.PrivilegedUserIDs = values
	}
	if raw, present := os.LookupEnv("DOCS_CHAT_ENABLED"); present {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("DOCS_CHAT_ENABLED must be true or false")
		}
		c.DocsAssistant.Enabled = value
	}
	if raw, present := os.LookupEnv("OPENAI_API_KEY"); present {
		c.DocsAssistant.APIKey = strings.TrimSpace(raw)
	}
	if raw, present := os.LookupEnv("OPENAI_BASE_URL"); present {
		c.DocsAssistant.BaseURL = strings.TrimSpace(raw)
	}
	if raw, present := os.LookupEnv("OPENAI_DOCS_MODEL"); present {
		c.DocsAssistant.Model = strings.TrimSpace(raw)
	}
	if raw, present := os.LookupEnv("DOCS_CHAT_REQUESTS_PER_MINUTE"); present {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("DOCS_CHAT_REQUESTS_PER_MINUTE must be an integer")
		}
		c.DocsAssistant.RequestsPerMinute = value
	}
	if _, err := validatePositiveUniqueIDs(c.Docs.PrivilegedUserIDs); err != nil {
		return fmt.Errorf("docs.privileged_user_ids: %w", err)
	}
	if err := validateDocsAssistant(c.DocsAssistant); err != nil {
		return err
	}
	return nil
}

func validateDocsAssistant(config DocsAssistantConfig) error {
	baseURL, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return fmt.Errorf("OPENAI_BASE_URL must be an absolute HTTP(S) URL")
	}
	if !config.Enabled {
		return nil
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return fmt.Errorf("OPENAI_API_KEY is required when docs chat is enabled")
	}
	if strings.TrimSpace(config.Model) == "" {
		return fmt.Errorf("OPENAI_DOCS_MODEL is required when docs chat is enabled")
	}
	if config.RequestsPerMinute <= 0 {
		return fmt.Errorf("DOCS_CHAT_REQUESTS_PER_MINUTE must be positive when docs chat is enabled")
	}
	return nil
}

func parsePositiveUniqueIDs(raw string) ([]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return []int64{}, nil
	}
	parts := strings.Split(raw, ",")
	values := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty user ID")
		}
		value, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid user ID %q", part)
		}
		values = append(values, value)
	}
	return validatePositiveUniqueIDs(values)
}

func validatePositiveUniqueIDs(values []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value <= 0 {
			return nil, fmt.Errorf("user ID must be positive")
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("duplicate user ID %d", value)
		}
		seen[value] = struct{}{}
	}
	return values, nil
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
