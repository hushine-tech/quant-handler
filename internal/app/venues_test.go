package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hushine-tech/core-service/gen/portfoliov1"
	"google.golang.org/grpc"
)

type fakeVenuePortfoliosClient struct {
	portfoliov1.PortfolioServiceClient

	createReq  *portfoliov1.CreateVenueRequest
	listReq    *portfoliov1.ListVenuesRequest
	walletReq  *portfoliov1.GetVenueOnlineInfoRequest
	bindReq    *portfoliov1.BindVenueRequest
	createResp *portfoliov1.CreateVenueResponse
	listResp   *portfoliov1.ListVenuesResponse
	walletResp *portfoliov1.GetVenueOnlineInfoResponse
	bindResp   *portfoliov1.BindVenueResponse
}

func (f *fakeVenuePortfoliosClient) CreateVenue(_ context.Context, req *portfoliov1.CreateVenueRequest, _ ...grpc.CallOption) (*portfoliov1.CreateVenueResponse, error) {
	f.createReq = req
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &portfoliov1.CreateVenueResponse{Venue: &portfoliov1.VenueEntry{VenueId: 88}}, nil
}

func (f *fakeVenuePortfoliosClient) ListVenues(_ context.Context, req *portfoliov1.ListVenuesRequest, _ ...grpc.CallOption) (*portfoliov1.ListVenuesResponse, error) {
	f.listReq = req
	if f.listResp != nil {
		return f.listResp, nil
	}
	return &portfoliov1.ListVenuesResponse{}, nil
}

func (f *fakeVenuePortfoliosClient) GetVenueOnlineInfo(_ context.Context, req *portfoliov1.GetVenueOnlineInfoRequest, _ ...grpc.CallOption) (*portfoliov1.GetVenueOnlineInfoResponse, error) {
	f.walletReq = req
	if f.walletResp != nil {
		return f.walletResp, nil
	}
	return &portfoliov1.GetVenueOnlineInfoResponse{
		Venue:  &portfoliov1.VenueEntry{VenueId: req.GetVenueId(), UserId: req.GetUserId()},
		Wallet: &portfoliov1.PortfolioWalletState{},
	}, nil
}

func (f *fakeVenuePortfoliosClient) BindVenue(_ context.Context, req *portfoliov1.BindVenueRequest, _ ...grpc.CallOption) (*portfoliov1.BindVenueResponse, error) {
	f.bindReq = req
	if f.bindResp != nil {
		return f.bindResp, nil
	}
	return &portfoliov1.BindVenueResponse{Venue: &portfoliov1.VenueEntry{VenueId: req.GetVenueId(), PortfolioId: req.GetPortfolioId(), UserId: req.GetUserId()}}, nil
}

func TestCreateVenueForwardsCredentialJSON(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	body := strings.NewReader(`{
		"portfolio_id": 42,
		"exchange": "binance",
		"market": "perpetual_futures",
		"environment": "demo",
		"display_name": "binance demo perp",
		"api_key": "k1",
		"credential_info": {"api_key":"k1","api_secret":"s1"},
		"margin_mode": "cross",
		"position_mode": "one_way"
	}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", body), 7)
	rec := httptest.NewRecorder()

	s.handleVenues(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.createReq.GetUserId() != 7 || fake.createReq.GetPortfolioId() != 42 {
		t.Fatalf("create req owner/portfolio mismatch: %+v", fake.createReq)
	}
	if fake.createReq.GetExchange() != 1 || fake.createReq.GetMarket() != 2 || fake.createReq.GetEnvironment() != 1 {
		t.Fatalf("route enum mismatch: %+v", fake.createReq)
	}
	if fake.createReq.GetApiKey() != "k1" {
		t.Fatalf("api_key not forwarded: %q", fake.createReq.GetApiKey())
	}
	if !strings.Contains(fake.createReq.GetCredentialJson(), `"api_secret":"s1"`) {
		t.Fatalf("credential_json not forwarded: %s", fake.createReq.GetCredentialJson())
	}
}

func TestCreateDemoVenueMissingCredentialInfoForwardsEmptyCredentialJSON(t *testing.T) {
	for _, body := range []string{
		`{"exchange":"binance","market":"perpetual_futures","environment":"demo","api_key":"k1","margin_mode":"cross","position_mode":"one_way"}`,
		`{"exchange":"binance","market":"perpetual_futures","environment":"demo","api_key":"k1","credential_info":null,"margin_mode":"cross","position_mode":"one_way"}`,
	} {
		fake := &fakeVenuePortfoliosClient{}
		s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
		req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(body)), 42)
		rr := httptest.NewRecorder()

		s.handleVenues(rr, req)

		if rr.Code != http.StatusCreated {
			t.Fatalf("body %s status = %d body=%s", body, rr.Code, rr.Body.String())
		}
		if fake.createReq.GetEnvironment() != 1 {
			t.Fatalf("body %s environment = %d, want 1", body, fake.createReq.GetEnvironment())
		}
		if fake.createReq.GetApiKey() != "k1" {
			t.Fatalf("body %s api_key not forwarded: %q", body, fake.createReq.GetApiKey())
		}
		if fake.createReq.GetCredentialJson() != "" {
			t.Fatalf("body %s credential_json = %q, want empty", body, fake.createReq.GetCredentialJson())
		}
	}
}

func TestCreateBacktestVenueOmitsCredentialPayload(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{createResp: &portfoliov1.CreateVenueResponse{Venue: &portfoliov1.VenueEntry{VenueId: 88}}}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}

	body := strings.NewReader(`{
		"portfolio_id":42,
		"exchange":"binance",
		"market":"perpetual_futures",
		"environment":"backtest",
		"margin_mode":"cross",
		"position_mode":"one_way"
	}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", body), 42)
	rr := httptest.NewRecorder()

	s.handleVenues(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if fake.createReq.GetApiKey() != "" || fake.createReq.GetCredentialJson() != "" {
		t.Fatalf("forwarded credentials api_key=%q credential_json=%q",
			fake.createReq.GetApiKey(), fake.createReq.GetCredentialJson())
	}
}

func TestCreateBacktestVenueAllowsUnbound(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{createResp: &portfoliov1.CreateVenueResponse{Venue: &portfoliov1.VenueEntry{VenueId: 88}}}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	body := strings.NewReader(`{
		"exchange":"binance",
		"market":"perpetual_futures",
		"environment":"backtest",
		"margin_mode":"cross",
		"position_mode":"one_way"
	}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", body), 42)
	rr := httptest.NewRecorder()

	s.handleVenues(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if fake.createReq == nil {
		t.Fatal("CreateVenue was not forwarded")
	}
	if fake.createReq.GetPortfolioId() != 0 {
		t.Fatalf("portfolio_id = %d, want unbound", fake.createReq.GetPortfolioId())
	}
}

func TestCreateBacktestFuturesVenueForwardsOnlyFuturesWallet(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{createResp: &portfoliov1.CreateVenueResponse{Venue: &portfoliov1.VenueEntry{VenueId: 88}}}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	body := strings.NewReader(`{
		"exchange":"binance",
		"market":"perpetual_futures",
		"environment":"backtest",
		"margin_mode":"cross",
		"position_mode":"one_way",
		"futures":{
			"margin_mode":"cross",
			"position_mode":"one_way",
			"initial_balance":1500,
			"positions":[{"symbol":"ETHUSDT","position_side":"BOTH","initial_balance":500,"fee_rate":0.0004}]
		}
	}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", body), 42)
	rr := httptest.NewRecorder()

	s.handleVenues(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if fake.createReq.GetFutures().GetInitialBalance() != 1500 || len(fake.createReq.GetFutures().GetPositions()) != 1 {
		t.Fatalf("futures bootstrap not forwarded: %+v", fake.createReq.GetFutures())
	}
	if fake.createReq.GetSpot() != nil {
		t.Fatalf("Spot wallet leaked into Futures venue: %+v", fake.createReq.GetSpot())
	}
	if leverage := fake.createReq.GetFutures().GetPositions()[0].GetLeverage(); leverage != 0 {
		t.Fatalf("Venue bootstrap assigned strategy-owned leverage: %v", leverage)
	}
	if side := fake.createReq.GetFutures().GetPositions()[0].GetPositionSide(); side != portfoliov1.FuturesPositionSide_FUTURES_POSITION_SIDE_BOTH {
		t.Fatalf("position side = %v, want BOTH", side)
	}
	if fake.createReq.GetTotalValue() != 1500 || fake.createReq.GetWalletBalance() != 1500 || fake.createReq.GetAvailableBalance() != 1500 {
		t.Fatalf("bootstrap totals = total:%v wallet:%v available:%v",
			fake.createReq.GetTotalValue(), fake.createReq.GetWalletBalance(), fake.createReq.GetAvailableBalance())
	}
}

func TestCreateBacktestFuturesVenueRejectsLegacyOrNonCanonicalPositionSide(t *testing.T) {
	for _, body := range []string{
		`{"exchange":"binance","market":"perpetual_futures","environment":"backtest","futures":{"margin_mode":"cross","position_mode":"one_way","positions":[{"symbol":"ETHUSDT","direction":1}]}}`,
		`{"exchange":"binance","market":"perpetual_futures","environment":"backtest","futures":{"margin_mode":"cross","position_mode":"one_way","positions":[{"symbol":"ETHUSDT","position_side":"1"}]}}`,
		`{"exchange":"binance","market":"perpetual_futures","environment":"backtest","futures":{"margin_mode":"cross","position_mode":"one_way","positions":[{"symbol":"ETHUSDT","position_side":"LONG"}]}}`,
	} {
		fake := &fakeVenuePortfoliosClient{}
		s := &server{portfolios: fake}
		req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(body)), 42)
		rec := httptest.NewRecorder()

		s.handleVenues(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("legacy or illegal position side status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if fake.createReq != nil {
			t.Fatalf("legacy or illegal position side reached core-service: %+v", fake.createReq)
		}
	}
}

func TestCreateBacktestFuturesVenueRejectsEditableLeverage(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "venue scalar",
			body: `{"exchange":"binance","market":"perpetual_futures","environment":"backtest","leverage":10,"futures":{"margin_mode":"cross","initial_balance":1000}}`,
		},
		{
			name: "wallet scalar",
			body: `{"exchange":"binance","market":"perpetual_futures","environment":"backtest","futures":{"margin_mode":"cross","initial_balance":1000,"leverage":10}}`,
		},
		{
			name: "position",
			body: `{"exchange":"binance","market":"perpetual_futures","environment":"backtest","futures":{"margin_mode":"isolated","positions":[{"symbol":"ETHUSDT","initial_balance":1000,"leverage":10}]}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeVenuePortfoliosClient{}
			s := &server{portfolios: fake}
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(tc.body)), 42)
			rec := httptest.NewRecorder()

			s.handleVenues(rec, req)

			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid JSON") {
				t.Fatalf("editable leverage was not rejected: status=%d body=%s", rec.Code, rec.Body.String())
			}
			if fake.createReq != nil {
				t.Fatalf("editable leverage reached core-service: %+v", fake.createReq)
			}
		})
	}
}

func TestCreateVenueRejectsRemovedTopLevelInitialBalance(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "Futures",
			body: `{
				"exchange":"binance","market":"perpetual_futures","environment":"backtest",
				"margin_mode":"cross","position_mode":"one_way","initial_balance":1500,
				"futures":{"margin_mode":"cross","position_mode":"one_way","initial_balance":1500}
			}`,
		},
		{
			name: "Spot",
			body: `{
				"exchange":"binance","market":"spot","environment":"backtest",
				"initial_balance":1500,
				"spot":{"assets":[{"asset":"USDT","free":"1500","locked":"0"}]}
			}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeVenuePortfoliosClient{}
			s := &server{portfolios: fake}
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(tc.body)), 42)
			rec := httptest.NewRecorder()

			s.handleVenues(rec, req)

			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid JSON") {
				t.Fatalf("removed top-level initial_balance was not rejected by strict JSON boundary: status=%d body=%s", rec.Code, rec.Body.String())
			}
			if fake.createReq != nil {
				t.Fatalf("removed top-level initial_balance reached core-service: %+v", fake.createReq)
			}
		})
	}
}

func TestCreateBacktestSpotVenueForwardsOnlyCanonicalSpotWallet(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{createResp: &portfoliov1.CreateVenueResponse{Venue: &portfoliov1.VenueEntry{VenueId: 89}}}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	body := strings.NewReader(`{
		"exchange":"binance",
		"market":"spot",
		"environment":"backtest",
		"spot":{"assets":[
			{"asset":"USDT","free":"250.00000000","locked":"0.00000000"},
			{"asset":"BTC","free":"0.01000000","locked":"0.00000000","avg_entry_price":"25000.00000000"}
		]}
	}`)
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", body), 42)
	rec := httptest.NewRecorder()

	s.handleVenues(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fake.createReq.GetFutures() != nil {
		t.Fatalf("Futures wallet leaked into Spot venue: %+v", fake.createReq.GetFutures())
	}
	spotAssets := fake.createReq.GetSpot().GetAssets()
	if len(spotAssets) != 2 || spotAssets[0].GetAsset() != "USDT" || spotAssets[0].GetFreeDecimal() != "250.00000000" || spotAssets[0].GetLockedDecimal() != "0.00000000" {
		t.Fatalf("canonical USDT bootstrap = %+v", spotAssets)
	}
	if spotAssets[1].GetAsset() != "BTC" || spotAssets[1].GetFreeDecimal() != "0.01000000" || spotAssets[1].GetLockedDecimal() != "0.00000000" || spotAssets[1].GetAvgEntryPriceDecimal() != "25000.00000000" {
		t.Fatalf("canonical BTC bootstrap = %+v", spotAssets[1])
	}
	if fake.createReq.GetTotalValue() != 500 || fake.createReq.GetWalletBalance() != 500 || fake.createReq.GetAvailableBalance() != 250 {
		t.Fatalf("bootstrap totals = total:%v wallet:%v available:%v",
			fake.createReq.GetTotalValue(), fake.createReq.GetWalletBalance(), fake.createReq.GetAvailableBalance())
	}
}

func TestCreateBacktestSpotVenueStrictlyRejectsLegacyWalletFields(t *testing.T) {
	tests := []struct {
		name string
		spot string
	}{
		{
			name: "wallet free",
			spot: `{"free":"100","assets":[{"asset":"USDT","free":"100","locked":"0"}]}`,
		},
		{
			name: "wallet locked",
			spot: `{"locked":"1","assets":[{"asset":"USDT","free":"100","locked":"0"}]}`,
		},
		{
			name: "asset symbol",
			spot: `{"assets":[{"asset":"USDT","symbol":"USDT","free":"100","locked":"0"}]}`,
		},
		{
			name: "asset qty",
			spot: `{"assets":[{"asset":"USDT","qty":"100","free":"100","locked":"0"}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeVenuePortfoliosClient{}
			s := &server{portfolios: fake}
			body := `{"exchange":"binance","market":"spot","environment":"backtest","spot":` + tc.spot + `}`
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(body)), 42)
			rec := httptest.NewRecorder()

			s.handleVenues(rec, req)

			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid JSON") {
				t.Fatalf("legacy field was not rejected by strict JSON boundary: status=%d body=%s", rec.Code, rec.Body.String())
			}
			if fake.createReq != nil {
				t.Fatalf("legacy field reached core-service: %+v", fake.createReq)
			}
		})
	}
}

func TestCreateBacktestVenueRejectsWalletFromAnotherMarket(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "Spot rejects Futures wallet",
			body: `{
				"exchange":"binance","market":"spot","environment":"backtest",
				"spot":{"assets":[{"asset":"USDT","free":"100","locked":"0"}]},
				"futures":{"margin_mode":"cross","initial_balance":1000}
			}`,
		},
		{
			name: "Futures rejects Spot wallet",
			body: `{
				"exchange":"binance","market":"perpetual_futures","environment":"backtest",
				"margin_mode":"cross","position_mode":"one_way",
				"spot":{"assets":[{"asset":"USDT","free":"100","locked":"0"}]},
				"futures":{"margin_mode":"cross","initial_balance":1000}
			}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeVenuePortfoliosClient{}
			s := &server{portfolios: fake}
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(tc.body)), 42)
			rec := httptest.NewRecorder()

			s.handleVenues(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want=400 body=%s", rec.Code, rec.Body.String())
			}
			if fake.createReq != nil {
				t.Fatalf("cross-market wallet reached core: %#v", fake.createReq)
			}
		})
	}
}

func TestCreateBacktestSpotVenueRejectsNonCanonicalWalletAssets(t *testing.T) {
	tests := []struct {
		name string
		spot string
	}{
		{name: "missing spot wallet", spot: ""},
		{name: "missing USDT", spot: `,"spot":{"assets":[{"asset":"BTC","free":"0.1","locked":"0"}]}`},
		{name: "duplicate asset", spot: `,"spot":{"assets":[{"asset":"USDT","free":"1","locked":"0"},{"asset":"usdt","free":"2","locked":"0"}]}`},
		{name: "trading symbol is not an asset", spot: `,"spot":{"assets":[{"asset":"USDT","free":"1","locked":"0"},{"asset":"BTCUSDT","free":"1","locked":"0"}]}`},
		{name: "negative free", spot: `,"spot":{"assets":[{"asset":"USDT","free":"-1","locked":"0"}]}`},
		{name: "unsupported precision", spot: `,"spot":{"assets":[{"asset":"USDT","free":"0.000000001","locked":"0"}]}`},
		{name: "number instead of exact string", spot: `,"spot":{"assets":[{"asset":"USDT","free":1,"locked":"0"}]}`},
		{name: "invalid asset code", spot: `,"spot":{"assets":[{"asset":"USD_T","free":"1","locked":"0"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeVenuePortfoliosClient{}
			s := &server{portfolios: fake}
			body := `{"exchange":"binance","market":"spot","environment":"backtest"` + tc.spot + `}`
			req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(body)), 42)
			rec := httptest.NewRecorder()

			s.handleVenues(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want=400 body=%s", rec.Code, rec.Body.String())
			}
			if fake.createReq != nil {
				t.Fatalf("invalid Spot wallet reached core: %+v", fake.createReq)
			}
		})
	}
}

func TestCreateBacktestSpotVenueNormalizesValidatedAssetCodes(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{}
	s := &server{portfolios: fake}
	body := `{"exchange":"binance","market":"spot","environment":"backtest","spot":{"assets":[{"asset":"usdt","free":"1000.00000000","locked":"0.00000000"},{"asset":"btc","free":"0.01000000","locked":"0.00100000"}]}}`
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(body)), 42)
	rec := httptest.NewRecorder()

	s.handleVenues(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assets := fake.createReq.GetSpot().GetAssets()
	if fake.createReq.GetMarket() != 1 || len(assets) != 2 || assets[0].GetAsset() != "USDT" || assets[1].GetAsset() != "BTC" {
		t.Fatalf("market=%d normalized assets=%+v", fake.createReq.GetMarket(), assets)
	}
}

func TestCreateBacktestVenueRejectsCredentialPayload(t *testing.T) {
	for _, body := range []string{
		`{"exchange":"binance","market":"perpetual_futures","environment":"backtest","api_key":"k","margin_mode":"cross","position_mode":"one_way"}`,
		`{"exchange":"binance","market":"perpetual_futures","environment":"backtest","credential_info":{},"margin_mode":"cross","position_mode":"one_way"}`,
		`{"exchange":"binance","market":"perpetual_futures","environment":"backtest","credential_info":null,"margin_mode":"cross","position_mode":"one_way"}`,
		`{"exchange":"binance","market":"perpetual_futures","environment":"backtest","credential_info":{"api_secret":"s"},"margin_mode":"cross","position_mode":"one_way"}`,
	} {
		fake := &fakeVenuePortfoliosClient{}
		s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
		req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues", strings.NewReader(body)), 42)
		rr := httptest.NewRecorder()

		s.handleVenues(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, want 400", body, rr.Code)
		}
		if fake.createReq != nil {
			t.Fatalf("body %s forwarded CreateVenue unexpectedly: %+v", body, fake.createReq)
		}
	}
}

func TestListPortfolioVenuesUsesPortfolioScope(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{
		listResp: &portfoliov1.ListVenuesResponse{Total: 0},
	}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/portfolios/42/venues?include_unbound=true&limit=25&offset=50", nil), 7)
	rec := httptest.NewRecorder()

	s.handlePortfolioVenues(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.listReq.GetUserId() != 7 || fake.listReq.GetPortfolioId() != 42 {
		t.Fatalf("list scope mismatch: %+v", fake.listReq)
	}
	if !fake.listReq.GetIncludeUnbound() || fake.listReq.GetLimit() != 25 || fake.listReq.GetOffset() != 50 {
		t.Fatalf("list options mismatch: %+v", fake.listReq)
	}
}

func TestListVenuesQueryPortfolioIDUsesPortfolioScope(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{
		listResp: &portfoliov1.ListVenuesResponse{Total: 0},
	}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/venues?portfolio_id=42&include_unbound=true", nil), 7)
	rec := httptest.NewRecorder()

	s.handleVenues(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.listReq.GetUserId() != 7 || fake.listReq.GetPortfolioId() != 42 {
		t.Fatalf("list scope mismatch: %+v", fake.listReq)
	}
	if !fake.listReq.GetIncludeUnbound() {
		t.Fatalf("include_unbound not forwarded: %+v", fake.listReq)
	}
}

func TestGetVenueWalletForwardsVenueIDAndUserID(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{
		walletResp: &portfoliov1.GetVenueOnlineInfoResponse{
			Venue: &portfoliov1.VenueEntry{
				VenueId:     53,
				UserId:      7,
				PortfolioId: 42,
				Exchange:    1,
				Market:      2,
				Environment: 0,
			},
			Wallet: &portfoliov1.PortfolioWalletState{
				TotalValue: 1000,
				Futures:    &portfoliov1.FuturesWallet{MarginMode: "cross", PositionMode: "one_way"},
			},
		},
	}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/venues/53/wallet", nil), 7)
	rec := httptest.NewRecorder()

	s.handleVenueByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.walletReq.GetVenueId() != 53 || fake.walletReq.GetUserId() != 7 {
		t.Fatalf("wallet request = %+v, want venue 53 user 7", fake.walletReq)
	}
	if !strings.Contains(rec.Body.String(), `"total_value":1000`) {
		t.Fatalf("wallet response missing total value: %s", rec.Body.String())
	}
}

func TestBindVenueForwardsPortfolioIDAndReason(t *testing.T) {
	fake := &fakeVenuePortfoliosClient{}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/venues/53/bind", strings.NewReader(`{"portfolio_id":42,"reason":"from test"}`)), 7)
	rec := httptest.NewRecorder()

	s.handleVenueByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.bindReq.GetVenueId() != 53 || fake.bindReq.GetPortfolioId() != 42 || fake.bindReq.GetUserId() != 7 || fake.bindReq.GetReason() != "from test" {
		t.Fatalf("bind request = %+v, want venue 53 portfolio 42 user 7 reason", fake.bindReq)
	}
}
