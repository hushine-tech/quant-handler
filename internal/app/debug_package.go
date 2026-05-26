package app

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	mdv1 "github.com/hushine-tech/control-panel-service/gen/marketdatav1"
	accountv1 "github.com/hushine-tech/core-service/gen/accountv1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const debugPackageKlineLimit = int32(1000)

var errDebugPackageIncompleteCoverage = errors.New("requested range has incomplete market data")

type debugPackageHTTPError struct {
	status int
	msg    string
}

func (e *debugPackageHTTPError) Error() string {
	return e.msg
}

type debugPackageBody struct {
	Exchange       string  `json:"exchange"`
	Market         string  `json:"market"`
	Symbol         string  `json:"symbol"`
	Interval       string  `json:"interval"`
	StartTimeMS    int64   `json:"start_time_ms"`
	EndTimeMS      int64   `json:"end_time_ms"`
	WalletSource   string  `json:"wallet_source"`
	InitialBalance float64 `json:"initial_balance"`
}

func (s *server) handleAccountDebugPackage(w http.ResponseWriter, r *http.Request, accountID int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.marketData == nil {
		writeErr(w, http.StatusServiceUnavailable, "control-panel-service is not configured (debug package unavailable)")
		return
	}
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	if !s.ensureDebugPackageAccountAccess(w, r, uid, accountID) {
		return
	}
	var body debugPackageBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	normalizeDebugPackageBody(&body)
	if uid <= 0 || accountID <= 0 || body.Exchange != "binance" || body.Market != "futures" || body.Symbol == "" || body.Interval == "" {
		writeErr(w, http.StatusBadRequest, "account_id, binance futures market, symbol, and interval are required")
		return
	}
	if body.StartTimeMS <= 0 || body.EndTimeMS <= body.StartTimeMS {
		writeErr(w, http.StatusBadRequest, "valid start_time_ms and end_time_ms are required")
		return
	}
	if body.InitialBalance <= 0 {
		body.InitialBalance = 1000
	}
	key := &mdv1.StreamKey{
		Exchange: body.Exchange,
		Market:   body.Market,
		Kind:     "kline",
		Symbol:   body.Symbol,
		Interval: body.Interval,
	}
	rows, err := s.fetchDebugPackageKlines(r, key, body.StartTimeMS, body.EndTimeMS)
	if err != nil {
		if errors.Is(err, errDebugPackageIncompleteCoverage) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		var httpErr *debugPackageHTTPError
		if errors.As(err, &httpErr) {
			writeErr(w, httpErr.status, httpErr.msg)
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(rows) == 0 {
		writeErr(w, http.StatusBadRequest, "requested range has no market data")
		return
	}
	parquetBytes, err := encodeDebugPackageKlinesParquet(rows)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to encode debug package data")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="debug-package-`+body.Symbol+`-`+strconv.FormatInt(time.Now().Unix(), 10)+`.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	if err := addZipFile(zw, "manifest.yaml", []byte(debugPackageManifest(body))); err != nil {
		return
	}
	if err := addZipFile(zw, "wallet.yaml", []byte(debugPackageWallet(body.InitialBalance))); err != nil {
		return
	}
	if err := addZipFile(zw, "strategy.py.template", []byte(defaultDebugStrategyTemplate(body.Symbol, body.Interval))); err != nil {
		return
	}
	if err := addZipFile(zw, "README.md", []byte("Import with: hushine-debug import ./debug-package.zip\n")); err != nil {
		return
	}
	_ = addZipFile(zw, "data.parquet", parquetBytes)
}

func (s *server) ensureDebugPackageAccountAccess(w http.ResponseWriter, r *http.Request, uid int64, accountID int64) bool {
	if s.accounts == nil {
		return true
	}
	resp, err := s.accounts.GetAccount(r.Context(), &accountv1.GetAccountRequest{
		AccountId: accountID,
		UserId:    uid,
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return false
	}
	if resp.GetAccount() == nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return false
	}
	return true
}

func normalizeDebugPackageBody(body *debugPackageBody) {
	body.Exchange = strings.ToLower(strings.TrimSpace(body.Exchange))
	if body.Exchange == "" {
		body.Exchange = "binance"
	}
	body.Market = strings.ToLower(strings.TrimSpace(body.Market))
	body.Symbol = strings.ToUpper(strings.TrimSpace(body.Symbol))
	body.Interval = strings.TrimSpace(body.Interval)
}

func (s *server) fetchDebugPackageKlines(r *http.Request, key *mdv1.StreamKey, startMS int64, endMS int64) ([]*mdv1.MarketDataKline, error) {
	validate, err := s.marketData.ValidateMarketDataCoverage(r.Context(), &mdv1.ValidateMarketDataCoverageRequest{
		Key:     key,
		StartAt: timestamppb.New(time.UnixMilli(startMS).UTC()),
		EndAt:   timestamppb.New(time.UnixMilli(endMS).UTC()),
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		return nil, &debugPackageHTTPError{status: code, msg: "coverage validation failed: " + msg}
	}
	if !validate.GetOk() {
		return nil, errDebugPackageIncompleteCoverage
	}

	rows := make([]*mdv1.MarketDataKline, 0, validate.GetExpectedCount())
	cursor := startMS
	stepMS, err := debugPackageIntervalMS(key.GetInterval())
	if err != nil {
		return nil, &debugPackageHTTPError{status: http.StatusBadRequest, msg: err.Error()}
	}
	for cursor < endMS {
		resp, err := s.marketData.QueryMarketDataKlines(r.Context(), &mdv1.QueryMarketDataKlinesRequest{
			Key:     key,
			StartAt: timestamppb.New(time.UnixMilli(cursor).UTC()),
			EndAt:   timestamppb.New(time.UnixMilli(endMS).UTC()),
			Limit:   debugPackageKlineLimit,
		})
		if err != nil {
			code, msg := grpcToHTTP(err)
			return nil, &debugPackageHTTPError{status: code, msg: "failed to load requested market data: " + msg}
		}
		batch := resp.GetRows()
		if len(batch) == 0 {
			break
		}
		rows = append(rows, batch...)
		last := batch[len(batch)-1]
		next := cursorFromKline(last, stepMS)
		if next <= cursor {
			return nil, fmt.Errorf("market data query did not advance cursor")
		}
		cursor = next
		if !resp.GetTruncated() {
			break
		}
	}
	if err := validateDebugPackageKlineRows(rows, startMS, endMS, stepMS); err != nil {
		return nil, err
	}
	return rows, nil
}

func validateDebugPackageKlineRows(rows []*mdv1.MarketDataKline, startMS int64, endMS int64, stepMS int64) error {
	expected := make(map[int64]struct{})
	for ts := startMS; ts < endMS; ts += stepMS {
		expected[ts] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		if row.GetOpenTime() == nil {
			return errDebugPackageIncompleteCoverage
		}
		openMS := row.GetOpenTime().AsTime().UTC().UnixMilli()
		if _, ok := expected[openMS]; !ok {
			continue
		}
		seen[openMS] = struct{}{}
	}
	if len(seen) != len(expected) {
		return errDebugPackageIncompleteCoverage
	}
	return nil
}

func cursorFromKline(row *mdv1.MarketDataKline, fallbackStepMS int64) int64 {
	if row.GetCloseTime() != nil {
		return row.GetCloseTime().AsTime().UTC().UnixMilli()
	}
	if row.GetOpenTime() != nil {
		return row.GetOpenTime().AsTime().UTC().UnixMilli() + fallbackStepMS
	}
	return 0
}

func debugPackageIntervalMS(interval string) (int64, error) {
	interval = strings.TrimSpace(interval)
	if len(interval) < 2 {
		return 0, fmt.Errorf("unsupported interval %q", interval)
	}
	amount, err := strconv.ParseInt(interval[:len(interval)-1], 10, 64)
	if err != nil || amount <= 0 {
		return 0, fmt.Errorf("unsupported interval %q", interval)
	}
	switch interval[len(interval)-1] {
	case 'm':
		return amount * int64(time.Minute/time.Millisecond), nil
	case 'h':
		return amount * int64(time.Hour/time.Millisecond), nil
	case 'd':
		return amount * int64((24*time.Hour)/time.Millisecond), nil
	case 'w':
		return amount * int64((7*24*time.Hour)/time.Millisecond), nil
	default:
		return 0, fmt.Errorf("unsupported interval %q", interval)
	}
}

func debugPackageManifest(body debugPackageBody) string {
	return "exchange: " + body.Exchange + "\nmarket: " + body.Market + "\nsymbol: " + body.Symbol + "\ninterval: " + body.Interval + "\n"
}

func debugPackageWallet(initialBalance float64) string {
	return "market: futures\nasset: USDT\ninitial_balance: " + strconv.FormatFloat(initialBalance, 'f', -1, 64) + "\n"
}

func addZipFile(zw *zip.Writer, name string, data []byte) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

func defaultDebugStrategyTemplate(symbol string, interval string) string {
	return "from hushine_strategy import OrderDecision\n\nclass MyStrategy:\n    INPUTS = [{\"market\":\"futures\",\"symbol\":\"" + symbol + "\",\"interval\":\"" + interval + "\"}]\n\n    def on_market_data(self, data, wallet):\n        return None\n"
}
