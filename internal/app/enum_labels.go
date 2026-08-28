package app

import (
	"fmt"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
)

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

func futuresPositionSideHTTPLabel(positionSide portfoliov1.FuturesPositionSide) (string, error) {
	switch positionSide {
	case portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_BOTH:
		return "BOTH", nil
	case portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_LONG:
		return "LONG", nil
	case portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_SHORT:
		return "SHORT", nil
	default:
		return "", fmt.Errorf("invalid futures position side %d", positionSide)
	}
}
