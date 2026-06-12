package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTelegramStartThenUIDSubscription(t *testing.T) {
	sendCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/bottest-token/sendMessage"):
			sendCount++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.URL.Path == "/api/container/getIndex":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":1,"data":{"cards":[{"card_type":9,"mblog":{"id":"1","mid":"1","created_at":"今天 08:05","text_raw":"初始化微博摘要"}}]}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	old := discoverEndpointBase
	discoverEndpointBase = server.URL + "/api/container/getIndex"
	defer func() { discoverEndpointBase = old }()

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	cfg := Config{
		Interval: "10m",
		TelegramBot: TelegramBotConfig{
			URL:                server.URL,
			BotToken:           "test-token",
			DefaultRecentLimit: 20,
			DefaultMaxPages:    3,
			IncludeReposts:     true,
		},
	}
	applyDefaults(&cfg)

	bot, err := NewTelegramBot(cfg.TelegramBot, server.Client())
	if err != nil {
		t.Fatalf("NewTelegramBot() error = %v", err)
	}

	err = handleTelegramUpdate(cfg, server.Client(), store, bot, telegramUpdate{
		UpdateID: 1,
		Message: &telegramMessage{
			Text: "/start",
			Chat: telegramChat{ID: 2062220133, Type: "private"},
			From: telegramUser{ID: 2062220133, Username: "tester"},
		},
	})
	if err != nil {
		t.Fatalf("handle /start error = %v", err)
	}
	sub, ok := store.Subscription("2062220133")
	if !ok || !sub.WaitingForUID {
		t.Fatalf("subscription after start = %+v, ok=%v", sub, ok)
	}

	err = handleTelegramUpdate(cfg, server.Client(), store, bot, telegramUpdate{
		UpdateID: 2,
		Message: &telegramMessage{
			Text: "1234567890",
			Chat: telegramChat{ID: 2062220133, Type: "private"},
			From: telegramUser{ID: 2062220133, Username: "tester"},
		},
	})
	if err != nil {
		t.Fatalf("handle uid error = %v", err)
	}
	sub, ok = store.Subscription("2062220133")
	if !ok || sub.UID != "1234567890" || sub.WaitingForUID {
		t.Fatalf("subscription after uid = %+v, ok=%v", sub, ok)
	}
	if sub.RecentLimit != 20 || sub.MaxPages != 3 || !sub.IncludeReposts {
		t.Fatalf("defaults not applied: %+v", sub)
	}
	if sendCount != 2 {
		t.Fatalf("sendCount = %d, want 2", sendCount)
	}
	tracked := store.TrackedPosts([]Account{{UID: "1234567890"}})
	if len(tracked) != 1 || tracked[0].PostText != "初始化微博摘要" {
		t.Fatalf("tracked = %+v", tracked)
	}
}

func TestShouldNotifyBotRecordIgnoresUnknownToVisible(t *testing.T) {
	cfg := Config{
		Notify: NotifyConfig{OnRecover: true, OnUnknown: false},
	}
	ok := shouldNotifyBotRecord(cfg, CheckRecord{
		Status:         StatusPublicVisible,
		PreviousStatus: StatusUnknown,
	}, true)
	if ok {
		t.Fatal("shouldNotifyBotRecord() = true for UNKNOWN -> PUBLIC_VISIBLE, want false")
	}
}

func TestShouldNotifyBotRecordInvisibleToVisibleRecovery(t *testing.T) {
	cfg := Config{
		Notify: NotifyConfig{OnRecover: true, OnUnknown: false},
	}
	ok := shouldNotifyBotRecord(cfg, CheckRecord{
		Status:         StatusPublicVisible,
		PreviousStatus: StatusPublicInvisible,
	}, true)
	if !ok {
		t.Fatal("shouldNotifyBotRecord() = false for PUBLIC_INVISIBLE -> PUBLIC_VISIBLE, want true")
	}
}
