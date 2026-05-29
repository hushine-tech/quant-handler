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
	createResp *accountv1.CreateVenueResponse
}

func (f *fakeVenueAccountsClient) CreateVenue(_ context.Context, req *accountv1.CreateVenueRequest, _ ...grpc.CallOption) (*accountv1.CreateVenueResponse, error) {
	f.createReq = req
	if f.createResp != nil {
		return f.createResp, nil
	}
	return &accountv1.CreateVenueResponse{Venue: &accountv1.VenueEntry{VenueId: 88}}, nil
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
