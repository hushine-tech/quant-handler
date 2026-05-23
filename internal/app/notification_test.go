package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hushine-tech/core-service/gen/accountv1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeNotificationAccountClient struct {
	accountv1.AccountServiceClient
	settingsReq *accountv1.GetNotificationSettingsRequest
	updateReq   *accountv1.UpdateNotificationPreferencesRequest
	bindReq     *accountv1.CreateNotificationBindCodeRequest
	resp        *accountv1.GetNotificationSettingsResponse
}

func (f *fakeNotificationAccountClient) GetNotificationSettings(_ context.Context, req *accountv1.GetNotificationSettingsRequest, _ ...grpc.CallOption) (*accountv1.GetNotificationSettingsResponse, error) {
	f.settingsReq = req
	return f.resp, nil
}

func (f *fakeNotificationAccountClient) UpdateNotificationPreferences(_ context.Context, req *accountv1.UpdateNotificationPreferencesRequest, _ ...grpc.CallOption) (*accountv1.UpdateNotificationPreferencesResponse, error) {
	f.updateReq = req
	return &accountv1.UpdateNotificationPreferencesResponse{Settings: f.resp}, nil
}

func (f *fakeNotificationAccountClient) CreateNotificationBindCode(_ context.Context, req *accountv1.CreateNotificationBindCodeRequest, _ ...grpc.CallOption) (*accountv1.CreateNotificationBindCodeResponse, error) {
	f.bindReq = req
	return &accountv1.CreateNotificationBindCodeResponse{
		BindCode:    "HUSH-123456",
		ExpiresAt:   timestamppb.New(time.Date(2026, 5, 17, 1, 2, 3, 0, time.UTC)),
		BotUsername: "hushine_bot",
	}, nil
}

func TestNotificationsGetSettings(t *testing.T) {
	fake := &fakeNotificationAccountClient{resp: notificationTestSettings()}
	s := &server{accounts: fake, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodGet, "/api/notifications/settings", nil), 42)
	rec := httptest.NewRecorder()

	s.handleNotifications(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.settingsReq.GetUserId() != 42 {
		t.Fatalf("user_id = %d, want 42", fake.settingsReq.GetUserId())
	}
	var body notificationSettingsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.BotUsername != "hushine_bot" || body.Telegram.Status != "bound" {
		t.Fatalf("body = %+v", body)
	}
}

func TestNotificationsUpdatePreferences(t *testing.T) {
	fake := &fakeNotificationAccountClient{resp: notificationTestSettings()}
	s := &server{accounts: fake, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodPatch, "/api/notifications/preferences", bytes.NewBufferString(`{"system_enabled":true,"strategy_enabled":false,"custom_enabled":true}`)), 42)
	rec := httptest.NewRecorder()

	s.handleNotifications(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.updateReq.GetUserId() != 42 {
		t.Fatalf("user_id = %d, want 42", fake.updateReq.GetUserId())
	}
	if fake.updateReq.GetPreferences().GetStrategyEnabled() {
		t.Fatalf("strategy_enabled = true, want false")
	}
}

func TestNotificationsCreateBindCode(t *testing.T) {
	fake := &fakeNotificationAccountClient{resp: notificationTestSettings()}
	s := &server{accounts: fake, jwtSecret: []byte("s"), corsOrigins: []string{"*"}}
	req := withUID(httptest.NewRequest(http.MethodPost, "/api/notifications/telegram/bind-code", nil), 42)
	rec := httptest.NewRecorder()

	s.handleNotifications(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if fake.bindReq.GetUserId() != 42 || fake.bindReq.GetChannel() != "telegram" {
		t.Fatalf("bind req = %+v", fake.bindReq)
	}
	var body notificationBindCodeJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.BindCode != "HUSH-123456" || body.BotUsername != "hushine_bot" || body.ExpiresAt == "" {
		t.Fatalf("body = %+v", body)
	}
}

func notificationTestSettings() *accountv1.GetNotificationSettingsResponse {
	return &accountv1.GetNotificationSettingsResponse{
		Preferences: &accountv1.NotificationPreferences{
			SystemEnabled:   true,
			StrategyEnabled: true,
			CustomEnabled:   false,
		},
		Plan: &accountv1.NotificationPlan{
			PlanCode:                 "pro",
			NotificationEnabled:      true,
			AllowSystem:              true,
			AllowStrategy:            true,
			AllowCustom:              true,
			CustomRateLimitPerMinute: 30,
			CustomRateLimitBurst:     10,
		},
		Telegram: &accountv1.NotificationChannel{
			Channel:             "telegram",
			Status:              "bound",
			ProviderUsername:    "alice",
			ProviderDisplayName: "Alice",
			LastDeliveryStatus:  "ok",
		},
		BotUsername: "hushine_bot",
	}
}
