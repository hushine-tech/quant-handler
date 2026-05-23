package app

import "testing"

func TestShouldApplyWalletBootstrap(t *testing.T) {
	cases := []struct {
		name string
		body createAccountBodyExt
		want bool
	}{
		{
			"live mode",
			createAccountBodyExt{Mode: 1, Spot: &spotIn{Free: 1}},
			false,
		},
		{
			"backtest empty spot object",
			createAccountBodyExt{Mode: 0, Spot: &spotIn{}},
			false,
		},
		{
			"backtest spot free",
			createAccountBodyExt{Mode: 0, Spot: &spotIn{Free: 10}},
			true,
		},
		{
			"backtest futures positions",
			createAccountBodyExt{Mode: 0, Futures: &futIn{Positions: []futPosIn{{Symbol: "BTCUSDT"}}}},
			true,
		},
		{
			"backtest cross pool only",
			createAccountBodyExt{Mode: 0, Futures: &futIn{InitialBalance: 5000}},
			true,
		},
		{
			"backtest empty futures",
			createAccountBodyExt{Mode: 0, Futures: &futIn{MarginMode: "isolated", PositionMode: "one_way"}},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldApplyWalletBootstrap(tc.body); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestValidateFuturesPayload(t *testing.T) {
	if err := validateFuturesPayload(nil); err != nil {
		t.Fatal(err)
	}
	if err := validateFuturesPayload(&futIn{}); err != nil {
		t.Fatal(err)
	}
	if err := validateFuturesPayload(&futIn{MarginMode: "oops", Positions: []futPosIn{{Symbol: "X"}}}); err == nil {
		t.Fatal("expected error")
	}
	if err := validateFuturesPayload(&futIn{PositionMode: "both", Positions: []futPosIn{{Symbol: "X"}}}); err == nil {
		t.Fatal("expected error")
	}
	if err := validateFuturesPayload(&futIn{MarginMode: "cross", PositionMode: "one_way", Positions: []futPosIn{{Symbol: "X"}}}); err != nil {
		t.Fatal(err)
	}
}
