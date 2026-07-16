package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	portfoliov1 "github.com/hushine-tech/core-service/gen/portfoliov1"
	"github.com/hushine-tech/quant-handler/internal/controlpanel"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type createStrategyOnlyClient struct {
	portfoliov1.PortfolioServiceClient
	req   *portfoliov1.CreateStrategyRequest
	calls int
}

func (f *createStrategyOnlyClient) CreateStrategy(_ context.Context, req *portfoliov1.CreateStrategyRequest, _ ...grpc.CallOption) (*portfoliov1.CreateStrategyResponse, error) {
	f.calls++
	f.req = req
	return &portfoliov1.CreateStrategyResponse{Strategy: &portfoliov1.StrategyEntry{
		StrategyId: 11,
		UserId:     req.GetUserId(),
		Name:       req.GetName(),
		Version:    req.GetVersion(),
		Code:       req.GetCode(),
		CreatedAt:  timestamppb.Now(),
	}}, nil
}

func TestCreateStrategyRemainsRuntimeIndependent(t *testing.T) {
	portfolios := &createStrategyOnlyClient{}
	resolver := &fakeResolver{resp: controlpanel.Route{RuntimeID: "must-not-route"}}
	proxy := &fakeControlPanelStrategyProxy{}
	s := &server{portfolios: portfolios, controlPanel: resolver, cpRuntime: proxy}
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/strategies", bytes.NewBufferString(
		`{"name":"mean reversion","version":"1.0.0","code":"class Strategy: pass"}`,
	)), 42)
	rec := httptest.NewRecorder()

	s.handleStrategiesCollection(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if portfolios.calls != 1 || portfolios.req == nil || portfolios.req.GetUserId() != 42 {
		t.Fatalf("CreateStrategy calls/request = %d/%+v", portfolios.calls, portfolios.req)
	}
	if resolver.resolveByIDCalls != 0 || resolver.ensureCalls != 0 || proxy.validateReq != nil {
		t.Fatalf("create touched runtime: resolve=%d ensure=%d validate=%+v", resolver.resolveByIDCalls, resolver.ensureCalls, proxy.validateReq)
	}
}
