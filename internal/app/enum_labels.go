package app

func accountEnvironmentFromBody(body createAccountBodyExt) int32 {
	if body.Environment != 0 {
		return body.Environment
	}
	return accountEnvironmentFromLegacyMode(body.Mode)
}

func accountEnvironmentFromLegacyMode(mode int32) int32 {
	switch mode {
	case 1:
		return 2 // live
	case 2:
		return 1 // demo, formerly testnet
	default:
		return 0 // backtest
	}
}

func legacyAccountModeFromEnvironment(environment int32) int32 {
	switch environment {
	case 1:
		return 2 // demo still maps to legacy testnet mode for existing session APIs
	case 2:
		return 1
	default:
		return 0
	}
}

func orderMarketLabel(market int32) string {
	switch market {
	case 1:
		return "spot"
	case 2:
		return "perpetual_futures"
	case 3:
		return "delivery_futures"
	default:
		return "unknown"
	}
}
