package app

import (
	"strings"
	"testing"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
)

// This fails if a wallet bootstrap accepts a legacy direction, non-canonical
// side, or uses only the symbol as the row identity.

func TestBuildFuturesWalletUsesCanonicalPositionSidesAndModeIdentity(t *testing.T) {
	tests := []struct {
		name        string
		input       *futIn
		wantSides   []portfoliov1.FuturesPositionSide
		wantSymbols []string
	}{
		{
			name: "one way keeps one BOTH leg per symbol",
			input: &futIn{PositionMode: "one_way", Positions: []futPosIn{
				{Symbol: "ethusdt", PositionSide: "BOTH"},
				{Symbol: "ETHUSDT", PositionSide: "BOTH"},
			}},
			wantSides:   []portfoliov1.FuturesPositionSide{portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_BOTH},
			wantSymbols: []string{"ETHUSDT"},
		},
		{
			name: "hedge keeps LONG and SHORT legs for same symbol",
			input: &futIn{PositionMode: "hedge", Positions: []futPosIn{
				{Symbol: "ETHUSDT", PositionSide: "LONG"},
				{Symbol: "ethusdt", PositionSide: "SHORT"},
				{Symbol: "ETHUSDT", PositionSide: "LONG"},
			}},
			wantSides:   []portfoliov1.FuturesPositionSide{portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_LONG, portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_SHORT},
			wantSymbols: []string{"ETHUSDT", "ETHUSDT"},
		},
		{
			name:        "empty positions are valid",
			input:       &futIn{PositionMode: "hedge"},
			wantSides:   []portfoliov1.FuturesPositionSide{},
			wantSymbols: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wallet, err := buildFuturesWallet(tt.input)
			if err != nil {
				t.Fatalf("buildFuturesWallet() error = %v", err)
			}
			if len(wallet.GetPositions()) != len(tt.wantSides) {
				t.Fatalf("positions = %#v, want %d rows", wallet.GetPositions(), len(tt.wantSides))
			}
			for i, position := range wallet.GetPositions() {
				if position.GetSymbol() != tt.wantSymbols[i] || position.GetPositionSide() != tt.wantSides[i] {
					t.Fatalf("position[%d] = (%q, %v), want (%q, %v)", i, position.GetSymbol(), position.GetPositionSide(), tt.wantSymbols[i], tt.wantSides[i])
				}
			}
		})
	}
}

func TestBuildFuturesWalletRejectsNonCanonicalOrIllegalPositionSides(t *testing.T) {
	tests := []struct {
		name  string
		input *futIn
		want  string
	}{
		{
			name:  "unknown side",
			input: &futIn{PositionMode: "hedge", Positions: []futPosIn{{Symbol: "ETHUSDT", PositionSide: "FLAT"}}},
			want:  "futures.positions[0].position_side must be BOTH, LONG, or SHORT",
		},
		{
			name:  "one way long",
			input: &futIn{PositionMode: "one_way", Positions: []futPosIn{{Symbol: "ETHUSDT", PositionSide: "LONG"}}},
			want:  "futures.positions[0].position_side must be BOTH in one_way mode",
		},
		{
			name:  "hedge both",
			input: &futIn{PositionMode: "hedge", Positions: []futPosIn{{Symbol: "ETHUSDT", PositionSide: "BOTH"}}},
			want:  "futures.positions[0].position_side must be LONG or SHORT in hedge mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildFuturesWallet(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("buildFuturesWallet() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFuturesPositionSideHTTPLabelUsesOnlyShortCanonicalStrings(t *testing.T) {
	tests := []struct {
		side portfoliov1.FuturesPositionSide
		want string
	}{
		{portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_BOTH, "BOTH"},
		{portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_LONG, "LONG"},
		{portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_SHORT, "SHORT"},
	}
	for _, tt := range tests {
		if got := futuresPositionSideHTTPLabel(tt.side); got != tt.want {
			t.Fatalf("futuresPositionSideHTTPLabel(%v) = %q, want %q", tt.side, got, tt.want)
		}
	}
}
