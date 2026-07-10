package app

func portfolioEnvironmentFromBody(body createPortfolioBodyExt) int32 {
	switch body.Environment {
	case 1, 2:
		return body.Environment
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

func orderExchangeLabel(exchange int32) string {
	switch exchange {
	case 1:
		return "binance"
	case 2:
		return "okx"
	default:
		return "unknown"
	}
}

func orderPositionSideLabel(positionSide int32) string {
	switch positionSide {
	case 1:
		return "LONG"
	case 2:
		return "SHORT"
	default:
		return "BOTH"
	}
}
