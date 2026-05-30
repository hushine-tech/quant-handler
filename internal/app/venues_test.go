package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hushine-tech/core-service/gen/accountv1"
	"google.golang.org/grpc"
)

type fakeVenueAccountsClient struct {
	accountv1.AccountServiceClient

	createReq  *accountv1.CreateVenueRequest
	listReq    *accountv1.ListVenuesRequest
	createResp *accountv1.CreateVenueResponse
	listResp   *accountv1.ListVenuesResponse
}

func (f *fakeVenueAccountsClient) CreateVenue(_ context.Context, req *accountv1.CreateVenueRequest, _ ...grpc.CallOption) (*accountv1.CreateVenueResponse, error) {
	f.createReq = req
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &accountv1.CreateVenueResponse{Venue: &accountv1.VenueEntry{VenueId: 88}}, nil
}

func (f *fakeVenueAccountsClient) ListVenues(_ context.Context, req *accountv1.ListVenuesRequest, _ ...grpc.CallOption) (*accountv1.ListVenuesResponse, error) {
	f.listReq = req
	if f.listResp != nil {
		return f.listResp, nil
	}
	return &accountv1.ListVenuesResponse{}, nil
}

func TestCreateVenueForwardsCredentialJSON(t *testing.T) {
	fake := &fakeVenueAccountsClient{}
	s := &server{accounts: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	body := strings.NewReader(`{
		"account_id": 42,
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
	if fake.createReq.GetUserId() != 7 || fake.createReq.GetAccountId() != 42 {
		t.Fatalf("create req owner/account mismatch: %+v", fake.createReq)
	}
	if fake.createReq.GetExchange() != 1 || fake.createReq.GetMarket() != 2 || fake.createReq.GetEnvironment() != 1 {
		t.Fatalf("route enum mismatch: %+v", fake.createReq)
	}
	if !strings.Contains(fake.createReq.GetCredentialJson(), `"api_secret":"s1"`) {
		t.Fatalf("credential_json not forwarded: %s", fake.createReq.GetCredentialJson())
	}
}

func TestListAccountVenuesUsesAccountScope(t *testing.T) {
	fake := &fakeVenueAccountsClient{
		listResp: &accountv1.ListVenuesResponse{Total: 0},
	}
	s := &server{accounts: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/accounts/42/venues?include_unbound=true&limit=25&offset=50", nil), 7)
	rec := httptest.NewRecorder()

	s.handleAccountVenues(rec, req, 42)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.listReq.GetUserId() != 7 || fake.listReq.GetAccountId() != 42 {
		t.Fatalf("list scope mismatch: %+v", fake.listReq)
	}
	if !fake.listReq.GetIncludeUnbound() || fake.listReq.GetLimit() != 25 || fake.listReq.GetOffset() != 50 {
		t.Fatalf("list options mismatch: %+v", fake.listReq)
	}
}

func TestListVenuesQueryAccountIDUsesAccountScope(t *testing.T) {
	fake := &fakeVenueAccountsClient{
		listResp: &accountv1.ListVenuesResponse{Total: 0},
	}
	s := &server{accounts: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/venues?account_id=42&include_unbound=true", nil), 7)
	rec := httptest.NewRecorder()

	s.handleVenues(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.listReq.GetUserId() != 7 || fake.listReq.GetAccountId() != 42 {
		t.Fatalf("list scope mismatch: %+v", fake.listReq)
	}
	if !fake.listReq.GetIncludeUnbound() {
		t.Fatalf("include_unbound not forwarded: %+v", fake.listReq)
	}
}
