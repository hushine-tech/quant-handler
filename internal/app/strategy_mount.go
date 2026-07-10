package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hushine-tech/core-service/gen/portfoliov1"
)

// handlePortfolioStrategies routes /api/portfolios/{portfolioID}/strategies[/{strategyID}[/activate]]
func (s *server) handlePortfolioStrategies(w http.ResponseWriter, r *http.Request, portfolioID int64, rest string) {
	// rest = "" → collection  |  "{sid}" → item  |  "{sid}/activate" → action
	rest = strings.Trim(rest, "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.listPortfolioStrategies(w, r, portfolioID)
		return
	}

	parts := strings.SplitN(rest, "/", 2)
	sid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "strategy_id must be an integer")
		return
	}

	if len(parts) == 1 {
		// /api/portfolios/{id}/strategies/{sid}
		switch r.Method {
		case http.MethodPost:
			s.mountStrategy(w, r, portfolioID, sid)
		case http.MethodDelete:
			s.unmountStrategy(w, r, portfolioID, sid)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// /api/portfolios/{id}/strategies/{sid}/activate
	if parts[1] == "activate" && r.Method == http.MethodPost {
		s.activateStrategy(w, r, portfolioID, sid)
		return
	}
	// /api/portfolios/{id}/strategies/{sid}/deactivate
	if parts[1] == "deactivate" && r.Method == http.MethodPost {
		s.deactivateStrategy(w, r, portfolioID, sid)
		return
	}
	http.NotFound(w, r)
}

func (s *server) listPortfolioStrategies(w http.ResponseWriter, r *http.Request, portfolioID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	resp, err := s.portfolios.ListPortfolioStrategies(r.Context(), &portfoliov1.ListPortfolioStrategiesRequest{
		PortfolioId: portfolioID,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	type portfolioStratJSON struct {
		Strategy  strategyJSON `json:"strategy"`
		Active    bool         `json:"active"`
		MountedAt string       `json:"mounted_at"`
	}
	out := make([]portfolioStratJSON, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		out = append(out, portfolioStratJSON{
			Strategy:  protoStrategyToJSON(e.GetStrategy(), false),
			Active:    e.GetActive(),
			MountedAt: e.GetMountedAt().AsTime().UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) mountStrategy(w http.ResponseWriter, r *http.Request, portfolioID, strategyID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.portfolios.MountStrategy(r.Context(), &portfoliov1.MountStrategyRequest{
		PortfolioId:  portfolioID,
		StrategyId: strategyID,
		UserId:     uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mounted": true})
}

func (s *server) unmountStrategy(w http.ResponseWriter, r *http.Request, portfolioID, strategyID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.portfolios.UnmountStrategy(r.Context(), &portfoliov1.UnmountStrategyRequest{
		PortfolioId:  portfolioID,
		StrategyId: strategyID,
		UserId:     uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unmounted": true})
}

func (s *server) deactivateStrategy(w http.ResponseWriter, r *http.Request, portfolioID, strategyID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.portfolios.DeactivateStrategy(r.Context(), &portfoliov1.DeactivateStrategyRequest{
		PortfolioId:  portfolioID,
		StrategyId: strategyID,
		UserId:     uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deactivated": true})
}

func (s *server) activateStrategy(w http.ResponseWriter, r *http.Request, portfolioID, strategyID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.portfolios.ActivateStrategy(r.Context(), &portfoliov1.ActivateStrategyRequest{
		PortfolioId:  portfolioID,
		StrategyId: strategyID,
		UserId:     uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activated": true})
}
