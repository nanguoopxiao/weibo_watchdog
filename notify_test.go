package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookNotifierPostsRecord(t *testing.T) {
	var got CheckRecord
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := NewNotifier(NotifyConfig{
		Enabled: true,
		Channels: []NotifyChannelConfig{{
			Type:    "webhook",
			Enabled: true,
			URL:     server.URL,
		}},
	}, server.Client())

	err := notifier.Notify(CheckRecord{
		TargetName: "测试",
		URL:        "https://weibo.com/1/2",
		Status:     StatusPublicInvisible,
		Evidence:   "test",
	})
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if got.Status != StatusPublicInvisible || got.URL != "https://weibo.com/1/2" {
		t.Fatalf("unexpected record: %+v", got)
	}
}

func TestShouldNotifyStatusChange(t *testing.T) {
	notifier := NewNotifier(NotifyConfig{
		Enabled:   true,
		OnRecover: true,
		OnUnknown: false,
	}, nil)

	ok := notifier.ShouldNotify(CheckRecord{
		Status:         StatusPublicInvisible,
		PreviousStatus: StatusPublicVisible,
	}, true)
	if !ok {
		t.Fatal("ShouldNotify() = false, want true")
	}

	ok = notifier.ShouldNotify(CheckRecord{
		Status:         StatusUnknown,
		PreviousStatus: StatusPublicVisible,
	}, true)
	if ok {
		t.Fatal("ShouldNotify() = true for UNKNOWN while OnUnknown=false")
	}
}

func TestTelegramNotifierReadsEnv(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN_TEST", "token-from-env")
	var gotPath string
	var gotPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	notifier := NewNotifier(NotifyConfig{
		Enabled: true,
		Channels: []NotifyChannelConfig{{
			Type:        "telegram",
			Enabled:     true,
			URL:         server.URL,
			BotTokenEnv: "TELEGRAM_BOT_TOKEN_TEST",
			ChatID:      "12345",
		}},
	}, server.Client())

	err := notifier.Notify(CheckRecord{
		TargetName: "测试",
		URL:        "https://weibo.com/1/2",
		Status:     StatusPublicInvisible,
		Evidence:   "test",
	})
	if err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if !strings.Contains(gotPath, "/bottoken-from-env/sendMessage") {
		t.Fatalf("path = %s", gotPath)
	}
	if gotPayload["chat_id"] != "12345" {
		t.Fatalf("chat_id = %v", gotPayload["chat_id"])
	}
}
