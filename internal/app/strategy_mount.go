package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hushine-tech/core-service/gen/accountv1"
)

// handleAccountStrategies routes /api/accounts/{accountID}/strategies[/{strategyID}[/activate]]
func (s *server) handleAccountStrategies(w http.ResponseWriter, r *http.Request, accountID int64, rest string) {
	// rest = "" → collection  |  "{sid}" → item  |  "{sid}/activate" → action
	rest = strings.Trim(rest, "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.listAccountStrategies(w, r, accountID)
		return
	}

	parts := strings.SplitN(rest, "/", 2)
	sid, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "strategy_id must be an integer")
		return
	}

	if len(parts) == 1 {
		// /api/accounts/{id}/strategies/{sid}
		switch r.Method {
		case http.MethodPost:
			s.mountStrategy(w, r, accountID, sid)
		case http.MethodDelete:
			s.unmountStrategy(w, r, accountID, sid)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// /api/accounts/{id}/strategies/{sid}/activate
	if parts[1] == "activate" && r.Method == http.MethodPost {
		s.activateStrategy(w, r, accountID, sid)
		return
	}
	// /api/accounts/{id}/strategies/{sid}/deactivate
	if parts[1] == "deactivate" && r.Method == http.MethodPost {
		s.deactivateStrategy(w, r, accountID, sid)
		return
	}
	http.NotFound(w, r)
}

func (s *server) listAccountStrategies(w http.ResponseWriter, r *http.Request, accountID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	resp, err := s.accounts.ListAccountStrategies(r.Context(), &accountv1.ListAccountStrategiesRequest{
		AccountId: accountID,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	type accountStratJSON struct {
		Strategy  strategyJSON `json:"strategy"`
		Active    bool         `json:"active"`
		MountedAt string       `json:"mounted_at"`
	}
	out := make([]accountStratJSON, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		out = append(out, accountStratJSON{
			Strategy:  protoStrategyToJSON(e.GetStrategy(), false),
			Active:    e.GetActive(),
			MountedAt: e.GetMountedAt().AsTime().UTC().Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) mountStrategy(w http.ResponseWriter, r *http.Request, accountID, strategyID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.accounts.MountStrategy(r.Context(), &accountv1.MountStrategyRequest{
		AccountId:  accountID,
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

func (s *server) unmountStrategy(w http.ResponseWriter, r *http.Request, accountID, strategyID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.accounts.UnmountStrategy(r.Context(), &accountv1.UnmountStrategyRequest{
		AccountId:  accountID,
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

func (s *server) deactivateStrategy(w http.ResponseWriter, r *http.Request, accountID, strategyID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.accounts.DeactivateStrategy(r.Context(), &accountv1.DeactivateStrategyRequest{
		AccountId:  accountID,
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

func (s *server) activateStrategy(w http.ResponseWriter, r *http.Request, accountID, strategyID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	_, err := s.accounts.ActivateStrategy(r.Context(), &accountv1.ActivateStrategyRequest{
		AccountId:  accountID,
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
