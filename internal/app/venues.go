package app

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/hushine-tech/core-service/gen/accountv1"
)

type venueBody struct {
	AccountID      int64          `json:"account_id"`
	Exchange       string         `json:"exchange"`
	Market         string         `json:"market"`
	Environment    string         `json:"environment"`
	Status         string         `json:"status"`
	DisplayName    string         `json:"display_name"`
	Description    string         `json:"description"`
	APIKey         string         `json:"api_key"`
	CredentialInfo map[string]any `json:"credential_info"`
	MarginMode     string         `json:"margin_mode"`
	PositionMode   string         `json:"position_mode"`
}

type venueActionBody struct {
	AccountID int64  `json:"account_id"`
	Reason    string `json:"reason"`
}

type venueJSON struct {
	VenueID               int64  `json:"venue_id"`
	UserID                int64  `json:"user_id"`
	AccountID             int64  `json:"account_id,omitempty"`
	Exchange              int32  `json:"exchange"`
	ExchangeLabel         string `json:"exchange_label"`
	Market                int32  `json:"market"`
	MarketLabel           string `json:"market_label"`
	Environment           int32  `json:"environment"`
	EnvironmentLabel      string `json:"environment_label"`
	Status                int32  `json:"status"`
	StatusLabel           string `json:"status_label"`
	DisplayName           string `json:"display_name"`
	Description           string `json:"description,omitempty"`
	APIKey                string `json:"api_key,omitempty"`
	CredentialFingerprint string `json:"credential_fingerprint,omitempty"`
	MarginMode            int32  `json:"margin_mode"`
	MarginModeLabel       string `json:"margin_mode_label"`
	PositionMode          int32  `json:"position_mode"`
	PositionModeLabel     string `json:"position_mode_label"`
	CreatedAt             string `json:"created_at,omitempty"`
	UpdatedAt             string `json:"updated_at,omitempty"`
	LastUsedAt            string `json:"last_used_at,omitempty"`
	ArchivedAt            string `json:"archived_at,omitempty"`
	ArchivedReason        string `json:"archived_reason,omitempty"`
}

func (s *server) handleVenues(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listVenues(w, r, 0)
	case http.MethodPost:
		s.createVenue(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *server) handleVenueByID(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/venues/")
	suffix = strings.Trim(suffix, "/")
	if suffix == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(suffix, "/")
	venueID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || venueID <= 0 {
		writeErr(w, http.StatusBadRequest, "venue_id must be a positive integer")
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.getVenue(w, r, venueID)
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "bind":
			s.bindVenue(w, r, venueID)
			return
		case "release":
			s.releaseVenue(w, r, venueID)
			return
		case "archive":
			s.archiveVenue(w, r, venueID)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *server) handleAccountVenues(w http.ResponseWriter, r *http.Request, accountID int64) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.listVenues(w, r, accountID)
}

func (s *server) createVenue(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var body venueBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	exchange, err := venueExchangeCode(body.Exchange)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	market, err := venueMarketCode(body.Market)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	environment, err := venueEnvironmentCode(body.Environment)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	statusCode, err := venueStatusCode(body.Status)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	marginMode, err := venueMarginModeCode(body.MarginMode, market)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	positionMode, err := venuePositionModeCode(body.PositionMode, market)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	credentialJSON, err := venueCredentialJSON(body.CredentialInfo)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "credential_info must be JSON serializable")
		return
	}
	resp, err := s.accounts.CreateVenue(r.Context(), &accountv1.CreateVenueRequest{
		UserId:         uid,
		AccountId:      body.AccountID,
		Exchange:       exchange,
		Market:         market,
		Environment:    environment,
		Status:         statusCode,
		DisplayName:    strings.TrimSpace(body.DisplayName),
		Description:    strings.TrimSpace(body.Description),
		ApiKey:         strings.TrimSpace(body.APIKey),
		CredentialJson: credentialJSON,
		MarginMode:     marginMode,
		PositionMode:   positionMode,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusCreated, venueToJSON(resp.GetVenue()))
}

func (s *server) listVenues(w http.ResponseWriter, r *http.Request, accountID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	limit, offset := parseCollectionPaging(r)
	resp, err := s.accounts.ListVenues(r.Context(), &accountv1.ListVenuesRequest{
		UserId:          uid,
		AccountId:       accountID,
		IncludeUnbound:  boolQuery(r, "include_unbound"),
		IncludeInactive: boolQuery(r, "include_inactive"),
		Limit:           limit,
		Offset:          offset,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	items := venuesToJSON(resp.GetVenues())
	writeJSON(w, http.StatusOK, pagedResponse{
		Items:      items,
		NextOffset: offset + int32(len(items)),
		HasMore:    resp.GetHasMore(),
		Total:      resp.GetTotal(),
	})
}

func (s *server) getVenue(w http.ResponseWriter, r *http.Request, venueID int64) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	resp, err := s.accounts.GetVenue(r.Context(), &accountv1.GetVenueRequest{UserId: uid, VenueId: venueID})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	if resp.GetVenue() == nil {
		writeErr(w, http.StatusNotFound, "venue not found")
		return
	}
	writeJSON(w, http.StatusOK, venueToJSON(resp.GetVenue()))
}

func (s *server) bindVenue(w http.ResponseWriter, r *http.Request, venueID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	var body venueActionBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := s.accounts.BindVenue(r.Context(), &accountv1.BindVenueRequest{
		UserId:    uid,
		AccountId: body.AccountID,
		VenueId:   venueID,
		Reason:    strings.TrimSpace(body.Reason),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, venueToJSON(resp.GetVenue()))
}

func (s *server) releaseVenue(w http.ResponseWriter, r *http.Request, venueID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	body := decodeVenueActionBody(r)
	resp, err := s.accounts.ReleaseVenue(r.Context(), &accountv1.ReleaseVenueRequest{
		UserId:  uid,
		VenueId: venueID,
		Reason:  strings.TrimSpace(body.Reason),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, venueToJSON(resp.GetVenue()))
}

func (s *server) archiveVenue(w http.ResponseWriter, r *http.Request, venueID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	body := decodeVenueActionBody(r)
	_, err := s.accounts.ArchiveVenue(r.Context(), &accountv1.ArchiveVenueRequest{
		UserId:  uid,
		VenueId: venueID,
		Reason:  strings.TrimSpace(body.Reason),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"archived": true})
}

func venueCredentialJSON(info map[string]any) (string, error) {
	if len(info) == 0 {
		return "{}", nil
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeVenueActionBody(r *http.Request) venueActionBody {
	var body venueActionBody
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	return body
}

func venuesToJSON(venues []*accountv1.VenueEntry) []venueJSON {
	out := make([]venueJSON, 0, len(venues))
	for _, venue := range venues {
		out = append(out, venueToJSON(venue))
	}
	return out
}

func venueToJSON(venue *accountv1.VenueEntry) venueJSON {
	if venue == nil {
		return venueJSON{}
	}
	return venueJSON{
		VenueID:               venue.GetVenueId(),
		UserID:                venue.GetUserId(),
		AccountID:             venue.GetAccountId(),
		Exchange:              venue.GetExchange(),
		ExchangeLabel:         venueExchangeLabel(venue.GetExchange()),
		Market:                venue.GetMarket(),
		MarketLabel:           orderMarketLabel(venue.GetMarket()),
		Environment:           venue.GetEnvironment(),
		EnvironmentLabel:      venueEnvironmentLabel(venue.GetEnvironment()),
		Status:                venue.GetStatus(),
		StatusLabel:           venueStatusLabel(venue.GetStatus()),
		DisplayName:           venue.GetDisplayName(),
		Description:           venue.GetDescription(),
		APIKey:                venue.GetApiKey(),
		CredentialFingerprint: venue.GetCredentialFingerprint(),
		MarginMode:            venue.GetMarginMode(),
		MarginModeLabel:       venueMarginModeLabel(venue.GetMarginMode()),
		PositionMode:          venue.GetPositionMode(),
		PositionModeLabel:     venuePositionModeLabel(venue.GetPositionMode()),
		CreatedAt:             protoTime(venue.GetCreatedAt()),
		UpdatedAt:             protoTime(venue.GetUpdatedAt()),
		LastUsedAt:            protoTime(venue.GetLastUsedAt()),
		ArchivedAt:            protoTime(venue.GetArchivedAt()),
		ArchivedReason:        venue.GetArchivedReason(),
	}
}

func venueExchangeCode(raw string) (int32, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "binance":
		return 1, nil
	case "okx":
		return 2, nil
	default:
		return 0, errBadVenueEnum("exchange", raw)
	}
}

func venueMarketCode(raw string) (int32, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "spot":
		return 1, nil
	case "futures", "perpetual_futures", "usdm_futures":
		return 2, nil
	case "delivery_futures":
		return 3, nil
	default:
		return 0, errBadVenueEnum("market", raw)
	}
}

func venueEnvironmentCode(raw string) (int32, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "backtest":
		return 0, nil
	case "demo", "":
		return 1, nil
	case "live":
		return 2, nil
	default:
		return 0, errBadVenueEnum("environment", raw)
	}
}

func venueStatusCode(raw string) (int32, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "active":
		return 1, nil
	case "disabled":
		return 2, nil
	case "revoked":
		return 3, nil
	case "archived":
		return 4, nil
	default:
		return 0, errBadVenueEnum("status", raw)
	}
}

func venueMarginModeCode(raw string, market int32) (int32, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if market == 1 {
		if v == "" || v == "none" {
			return 0, nil
		}
		return 0, errBadVenueEnum("margin_mode", raw)
	}
	switch v {
	case "", "cross":
		return 1, nil
	case "isolated":
		return 2, nil
	default:
		return 0, errBadVenueEnum("margin_mode", raw)
	}
}

func venuePositionModeCode(raw string, market int32) (int32, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if market == 1 {
		if v == "" || v == "none" {
			return 0, nil
		}
		return 0, errBadVenueEnum("position_mode", raw)
	}
	switch v {
	case "", "one_way", "one-way", "oneway":
		return 1, nil
	case "hedge":
		return 2, nil
	default:
		return 0, errBadVenueEnum("position_mode", raw)
	}
}

func venueExchangeLabel(code int32) string {
	switch code {
	case 1:
		return "binance"
	case 2:
		return "okx"
	default:
		return "unknown"
	}
}

func venueEnvironmentLabel(code int32) string {
	switch code {
	case 0:
		return "backtest"
	case 1:
		return "demo"
	case 2:
		return "live"
	default:
		return "unknown"
	}
}

func venueStatusLabel(code int32) string {
	switch code {
	case 1:
		return "active"
	case 2:
		return "disabled"
	case 3:
		return "revoked"
	case 4:
		return "archived"
	default:
		return "unknown"
	}
}

func venueMarginModeLabel(code int32) string {
	switch code {
	case 1:
		return "cross"
	case 2:
		return "isolated"
	default:
		return "none"
	}
}

func venuePositionModeLabel(code int32) string {
	switch code {
	case 1:
		return "one_way"
	case 2:
		return "hedge"
	default:
		return "none"
	}
}

func errBadVenueEnum(field, value string) error {
	return &venueEnumError{field: field, value: value}
}

type venueEnumError struct {
	field string
	value string
}

func (e *venueEnumError) Error() string {
	if strings.TrimSpace(e.value) == "" {
		return e.field + " is required"
	}
	return e.field + " is invalid"
}

func boolQuery(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
