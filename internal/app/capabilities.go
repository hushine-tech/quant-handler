package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
	strategyv1 "github.com/hushine-tech/strategy-service/gen/strategyv1"
)

const (
	capabilityBacktestSpotUSDT = "backtest_spot_usdt"
	capabilityDemoSpotUSDT     = "demo_spot_usdt"
	capabilityOfflineSpotUSDT  = "offline_spot_usdt"
	capabilityLiveSpotUSDT     = "live_spot_usdt"
	codeSpotCapabilityDisabled = "SPOT_CAPABILITY_DISABLED"
	codeSpotLiveRolloutGuard   = "SPOT_LIVE_ROLLOUT_GUARD"
	codeCapabilityUnavailable  = "SPOT_CAPABILITY_DISCOVERY_UNAVAILABLE"
)

var errProductCapabilityDiscoveryUnavailable = errors.New("product capability discovery is unavailable")

type productCapabilityJSON struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Effective  bool   `json:"effective"`
	Reason     string `json:"reason"`
}

type structuredErrorJSON struct {
	Error       string                 `json:"error"`
	Code        string                 `json:"code"`
	Route       string                 `json:"route,omitempty"`
	Exchange    int32                  `json:"exchange,omitempty"`
	Market      int32                  `json:"market,omitempty"`
	VenueID     int64                  `json:"venue_id,omitempty"`
	Symbol      string                 `json:"symbol,omitempty"`
	FilterType  string                 `json:"filter_type,omitempty"`
	Environment int32                  `json:"environment"`
	Retryable   bool                   `json:"retryable"`
	Source      string                 `json:"source"`
	Failures    []preflightFailureJSON `json:"failures,omitempty"`
}

func (s *server) loadProductCapabilities(ctx context.Context) ([]productCapabilityJSON, error) {
	if s.portfolios == nil {
		return nil, errProductCapabilityDiscoveryUnavailable
	}
	response, err := s.portfolios.GetProductCapabilities(ctx, &portfoliov1.GetProductCapabilitiesRequest{})
	if err != nil || response == nil {
		return nil, errProductCapabilityDiscoveryUnavailable
	}
	states := make([]productCapabilityJSON, 0, len(response.GetCapabilities()))
	for _, state := range response.GetCapabilities() {
		if state == nil {
			return nil, errProductCapabilityDiscoveryUnavailable
		}
		states = append(states, productCapabilityJSON{
			Name:       state.GetName(),
			Configured: state.GetConfigured(),
			Effective:  state.GetEffective(),
			Reason:     state.GetReason(),
		})
	}
	return states, nil
}

func (s *server) handleProductCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	states, err := s.loadProductCapabilities(r.Context())
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, errProductCapabilityDiscoveryUnavailable.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": states})
}

func writeStructuredError(w http.ResponseWriter, status int, detail structuredErrorJSON) {
	writeJSON(w, status, detail)
}

func (s *server) requireSpotStartCapability(ctx context.Context, w http.ResponseWriter, environment int32) bool {
	if environment == 2 {
		writeStructuredError(w, http.StatusPreconditionFailed, structuredErrorJSON{
			Error: "Live Spot is rollout-guarded", Code: codeSpotLiveRolloutGuard,
			Environment: environment, Source: "quant-handler",
		})
		return false
	}
	name := capabilityBacktestSpotUSDT
	if environment == 1 {
		name = capabilityDemoSpotUSDT
	}
	states, err := s.loadProductCapabilities(ctx)
	if err != nil {
		writeStructuredError(w, http.StatusServiceUnavailable, structuredErrorJSON{
			Error: errProductCapabilityDiscoveryUnavailable.Error(), Code: codeCapabilityUnavailable,
			Environment: environment, Retryable: true, Source: "core-service",
		})
		return false
	}
	for _, state := range states {
		if state.Name != name {
			continue
		}
		if state.Effective {
			return true
		}
		message := strings.TrimSpace(state.Reason)
		if message == "" {
			message = "Spot capability is disabled"
		}
		code := codeSpotCapabilityDisabled
		if state.Reason == codeSpotLiveRolloutGuard {
			code = codeSpotLiveRolloutGuard
		}
		writeStructuredError(w, http.StatusPreconditionFailed, structuredErrorJSON{
			Error: message, Code: code, Environment: environment, Source: "core-service",
		})
		return false
	}
	writeStructuredError(w, http.StatusPreconditionFailed, structuredErrorJSON{
		Error: "Spot capability is not reported by core-service", Code: codeSpotCapabilityDisabled,
		Environment: environment, Source: "core-service",
	})
	return false
}

func previewDeclaresSpot(response *strategyv1.PreviewRunStrategyResponse) bool {
	if response == nil {
		return false
	}
	for _, input := range response.GetDeclaredInputs() {
		if input != nil && strings.EqualFold(strings.TrimSpace(input.GetMarket()), "spot") {
			return true
		}
	}
	for _, input := range response.GetRequiredStreams() {
		if input != nil && strings.EqualFold(strings.TrimSpace(input.GetMarket()), "spot") {
			return true
		}
	}
	for _, target := range response.GetDeclaredOrderTargets() {
		if target != nil && strings.EqualFold(strings.TrimSpace(target.GetMarket()), "spot") {
			return true
		}
	}
	for _, route := range response.GetRequiredRoutes() {
		if route != nil && strings.EqualFold(strings.TrimSpace(route.GetMarket()), "spot") {
			return true
		}
	}
	return false
}
