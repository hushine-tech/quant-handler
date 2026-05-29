package app

import (
	"fmt"
	"strings"
)

// effectiveSpotPayload is true when the client sent a non-empty spot configuration.
func effectiveSpotPayload(s *spotIn) bool {
	if s == nil {
		return false
	}
	return s.Free != 0 || s.Locked != 0 || len(s.Assets) > 0
}

// effectiveFuturesPayload is true when the client sent contracts or a cross margin pool balance.
func effectiveFuturesPayload(f *futIn) bool {
	if f == nil {
		return false
	}
	return len(f.Positions) > 0 || f.InitialBalance != 0
}

func shouldApplyWalletBootstrap(body createAccountBodyExt) bool {
	if accountEnvironmentFromBody(body) != 0 {
		return false
	}
	return effectiveSpotPayload(body.Spot) || effectiveFuturesPayload(body.Futures)
}

func validateFuturesPayload(f *futIn) error {
	if f == nil || !effectiveFuturesPayload(f) {
		return nil
	}
	mm := strings.ToLower(strings.TrimSpace(f.MarginMode))
	if mm == "" {
		mm = "isolated"
	}
	if mm != "isolated" && mm != "cross" {
		return fmt.Errorf("invalid margin_mode %q: use isolated or cross", f.MarginMode)
	}
	pm := normPositionMode(f.PositionMode)
	if pm == "" {
		pm = "one_way"
	}
	if pm != "one_way" && pm != "hedge" {
		return fmt.Errorf("invalid position_mode %q: use one_way or hedge", f.PositionMode)
	}
	return nil
}
