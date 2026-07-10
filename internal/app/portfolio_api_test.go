package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hushine-tech/core-service/gen/portfoliov1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakePortfolioListClient struct {
	portfoliov1.PortfolioServiceClient

	listReq *portfoliov1.ListPortfoliosRequest
}

func (f *fakePortfolioListClient) ListPortfolios(_ context.Context, req *portfoliov1.ListPortfoliosRequest, _ ...grpc.CallOption) (*portfoliov1.ListPortfoliosResponse, error) {
	f.listReq = req
	return &portfoliov1.ListPortfoliosResponse{
		Portfolios: []*portfoliov1.PortfolioRegistryEntry{{
			PortfolioId:   42,
			UserId:      req.GetUserId(),
			Name:        "alpha",
			Environment: 0,
			Description: "venue group",
			CreatedAt:   timestamppb.Now(),
		}},
		Total: 1,
	}, nil
}

func TestPortfolioCollectionUsesPortfolioContract(t *testing.T) {
	fake := &fakePortfolioListClient{}
	s := &server{portfolios: fake, jwtSecret: []byte("secret"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/portfolios", nil), 7)
	rec := httptest.NewRecorder()

	s.handlePortfoliosCollection().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.listReq.GetUserId() != 7 {
		t.Fatalf("user_id = %d, want 7", fake.listReq.GetUserId())
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := body[0]["portfolio_id"]; got != float64(42) {
		t.Fatalf("portfolio_id = %#v, want 42", got)
	}
	if _, ok := body[0]["account_id"]; ok {
		t.Fatalf("legacy account_id leaked in response: %s", rec.Body.String())
	}
}
