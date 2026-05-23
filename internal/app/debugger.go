package app

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hushine-tech/quant-handler/internal/controlpanel"
)

type loadDebugDatasetBody struct {
	RuntimeID   string `json:"runtime_id"`
	Market      string `json:"market"`
	Symbol      string `json:"symbol"`
	Interval    string `json:"interval"`
	StartTimeMS int64  `json:"start_time_ms"`
	EndTimeMS   int64  `json:"end_time_ms"`
}

func (s *server) handleAccountDebugDataset(w http.ResponseWriter, r *http.Request, accountID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var body loadDebugDatasetBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	runtimeID := strings.TrimSpace(body.RuntimeID)
	if runtimeID == "" {
		writeErr(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	market := strings.ToLower(strings.TrimSpace(body.Market))
	symbol := strings.ToUpper(strings.TrimSpace(body.Symbol))
	interval := strings.TrimSpace(body.Interval)
	if market == "" || symbol == "" || interval == "" {
		writeErr(w, http.StatusBadRequest, "market, symbol, and interval are required")
		return
	}
	if body.StartTimeMS <= 0 || body.EndTimeMS <= body.StartTimeMS {
		writeErr(w, http.StatusBadRequest, "valid start_time_ms and end_time_ms are required")
		return
	}
	state, err := s.controlPanel.LoadDebugDataset(r.Context(), controlpanel.LoadDebugDatasetArgs{
		UserID:      uid,
		AccountID:   accountID,
		RuntimeID:   runtimeID,
		Market:      market,
		Symbol:      symbol,
		Interval:    interval,
		StartTimeMS: body.StartTimeMS,
		EndTimeMS:   body.EndTimeMS,
	})
	if err != nil {
		writeControlPanelErr(w, err, "control-panel-service not configured")
		return
	}
	writeJSON(w, http.StatusOK, debugDatasetToJSON(&state))
}
