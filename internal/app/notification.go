package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/hushine-tech/core-service/gen/accountv1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type notificationPreferencesJSON struct {
	SystemEnabled   bool `json:"system_enabled"`
	StrategyEnabled bool `json:"strategy_enabled"`
	CustomEnabled   bool `json:"custom_enabled"`
}

type notificationPlanJSON struct {
	PlanCode                 string `json:"plan_code"`
	NotificationEnabled      bool   `json:"notification_enabled"`
	AllowSystem              bool   `json:"allow_system"`
	AllowStrategy            bool   `json:"allow_strategy"`
	AllowCustom              bool   `json:"allow_custom"`
	CustomRateLimitPerMinute int32  `json:"custom_rate_limit_per_minute"`
	CustomRateLimitBurst     int32  `json:"custom_rate_limit_burst"`
}

type notificationChannelJSON struct {
	Channel             string `json:"channel"`
	Status              string `json:"status"`
	ProviderUsername    string `json:"provider_username,omitempty"`
	ProviderDisplayName string `json:"provider_display_name,omitempty"`
	BoundAt             string `json:"bound_at,omitempty"`
	LastDeliveryAt      string `json:"last_delivery_at,omitempty"`
	LastDeliveryStatus  string `json:"last_delivery_status,omitempty"`
	LastDeliveryError   string `json:"last_delivery_error,omitempty"`
}

type notificationSettingsJSON struct {
	Preferences notificationPreferencesJSON `json:"preferences"`
	Plan        notificationPlanJSON        `json:"plan"`
	Telegram    notificationChannelJSON     `json:"telegram"`
	BotUsername string                      `json:"bot_username"`
}

type notificationBindCodeJSON struct {
	BindCode    string `json:"bind_code"`
	ExpiresAt   string `json:"expires_at"`
	BotUsername string `json:"bot_username"`
}

func (s *server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFromRequest(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "missing user context")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/api/notifications")
	suffix = strings.Trim(suffix, "/")
	switch {
	case suffix == "" || suffix == "settings":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.getNotificationSettings(w, r, uid)
	case suffix == "preferences":
		if r.Method != http.MethodPatch && r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.updateNotificationPreferences(w, r, uid)
	case suffix == "telegram/bind-code":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.createNotificationBindCode(w, r, uid)
	case suffix == "telegram/confirm":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.confirmNotificationBinding(w, r, uid)
	case suffix == "telegram":
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.unbindNotificationTelegram(w, r, uid)
	case suffix == "test":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.sendTestNotification(w, r, uid)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) getNotificationSettings(w http.ResponseWriter, r *http.Request, userID int64) {
	resp, err := s.accounts.GetNotificationSettings(r.Context(), &accountv1.GetNotificationSettingsRequest{UserId: userID})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, notificationSettingsToJSON(resp))
}

func (s *server) updateNotificationPreferences(w http.ResponseWriter, r *http.Request, userID int64) {
	var body notificationPreferencesJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := s.accounts.UpdateNotificationPreferences(r.Context(), &accountv1.UpdateNotificationPreferencesRequest{
		UserId: userID,
		Preferences: &accountv1.NotificationPreferences{
			SystemEnabled:   body.SystemEnabled,
			StrategyEnabled: body.StrategyEnabled,
			CustomEnabled:   body.CustomEnabled,
		},
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, notificationSettingsToJSON(resp.GetSettings()))
}

func (s *server) createNotificationBindCode(w http.ResponseWriter, r *http.Request, userID int64) {
	resp, err := s.accounts.CreateNotificationBindCode(r.Context(), &accountv1.CreateNotificationBindCodeRequest{
		UserId:  userID,
		Channel: "telegram",
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusCreated, notificationBindCodeJSON{
		BindCode:    resp.GetBindCode(),
		ExpiresAt:   protoTimeToJSON(resp.GetExpiresAt()),
		BotUsername: resp.GetBotUsername(),
	})
}

func (s *server) confirmNotificationBinding(w http.ResponseWriter, r *http.Request, userID int64) {
	resp, err := s.accounts.ConfirmNotificationBinding(r.Context(), &accountv1.ConfirmNotificationBindingRequest{
		UserId:  userID,
		Channel: "telegram",
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, notificationSettingsToJSON(resp.GetSettings()))
}

func (s *server) unbindNotificationTelegram(w http.ResponseWriter, r *http.Request, userID int64) {
	resp, err := s.accounts.UnbindNotificationChannel(r.Context(), &accountv1.UnbindNotificationChannelRequest{
		UserId:  userID,
		Channel: "telegram",
	})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, notificationSettingsToJSON(resp.GetSettings()))
}

func (s *server) sendTestNotification(w http.ResponseWriter, r *http.Request, userID int64) {
	resp, err := s.accounts.SendTestNotification(r.Context(), &accountv1.SendTestNotificationRequest{UserId: userID})
	if err != nil {
		code, msg := grpcToHTTP(err)
		writeErr(w, code, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accepted": resp.GetAccepted(),
		"settings": notificationSettingsToJSON(resp.GetSettings()),
	})
}

func notificationSettingsToJSON(resp *accountv1.GetNotificationSettingsResponse) notificationSettingsJSON {
	if resp == nil {
		return notificationSettingsJSON{}
	}
	pref := resp.GetPreferences()
	plan := resp.GetPlan()
	telegram := resp.GetTelegram()
	return notificationSettingsJSON{
		Preferences: notificationPreferencesJSON{
			SystemEnabled:   pref.GetSystemEnabled(),
			StrategyEnabled: pref.GetStrategyEnabled(),
			CustomEnabled:   pref.GetCustomEnabled(),
		},
		Plan: notificationPlanJSON{
			PlanCode:                 plan.GetPlanCode(),
			NotificationEnabled:      plan.GetNotificationEnabled(),
			AllowSystem:              plan.GetAllowSystem(),
			AllowStrategy:            plan.GetAllowStrategy(),
			AllowCustom:              plan.GetAllowCustom(),
			CustomRateLimitPerMinute: plan.GetCustomRateLimitPerMinute(),
			CustomRateLimitBurst:     plan.GetCustomRateLimitBurst(),
		},
		Telegram: notificationChannelJSON{
			Channel:             telegram.GetChannel(),
			Status:              telegram.GetStatus(),
			ProviderUsername:    telegram.GetProviderUsername(),
			ProviderDisplayName: telegram.GetProviderDisplayName(),
			BoundAt:             protoTimeToJSON(telegram.GetBoundAt()),
			LastDeliveryAt:      protoTimeToJSON(telegram.GetLastDeliveryAt()),
			LastDeliveryStatus:  telegram.GetLastDeliveryStatus(),
			LastDeliveryError:   telegram.GetLastDeliveryError(),
		},
		BotUsername: resp.GetBotUsername(),
	}
}

func protoTimeToJSON(ts *timestamppb.Timestamp) string {
	if ts == nil || !ts.IsValid() {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339Nano)
}
