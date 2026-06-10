package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	defaultInterval       = "10m"
	defaultTimeout        = "20s"
	defaultDataDir        = "data"
	defaultUA             = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"
	defaultBotPollTimeout = "25s"
)

var uidRe = regexp.MustCompile(`^\d+$`)

func LoadConfig(path string) (Config, error) {
	var cfg Config

	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}

	applyDefaults(&cfg)
	if err := validateConfig(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if strings.TrimSpace(cfg.Interval) == "" {
		cfg.Interval = defaultInterval
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		cfg.DataDir = defaultDataDir
	}
	if strings.TrimSpace(cfg.UserAgent) == "" {
		cfg.UserAgent = defaultUA
	}
	if strings.TrimSpace(cfg.Network.Timeout) == "" {
		cfg.Network.Timeout = defaultTimeout
	}
	if strings.TrimSpace(cfg.Network.IPFamily) == "" {
		cfg.Network.IPFamily = "auto"
	}
	if strings.TrimSpace(cfg.TelegramBot.URL) == "" {
		cfg.TelegramBot.URL = "https://api.telegram.org"
	}
	if strings.TrimSpace(cfg.TelegramBot.BotTokenEnv) == "" {
		cfg.TelegramBot.BotTokenEnv = "TELEGRAM_BOT_TOKEN"
	}
	if strings.TrimSpace(cfg.TelegramBot.PollTimeout) == "" {
		cfg.TelegramBot.PollTimeout = defaultBotPollTimeout
	}
	if cfg.TelegramBot.DefaultRecentLimit <= 0 {
		cfg.TelegramBot.DefaultRecentLimit = 20
	}
	if cfg.TelegramBot.DefaultMaxPages <= 0 {
		cfg.TelegramBot.DefaultMaxPages = (cfg.TelegramBot.DefaultRecentLimit + 9) / 10
	}
	if cfg.TelegramBot.DefaultMaxPages < 1 {
		cfg.TelegramBot.DefaultMaxPages = 1
	}
	for i := range cfg.Accounts {
		if cfg.Accounts[i].RecentLimit < 0 {
			cfg.Accounts[i].RecentLimit = 0
		}
		if cfg.Accounts[i].LookbackDays < 0 {
			cfg.Accounts[i].LookbackDays = 0
		}
		if cfg.Accounts[i].RecentLimit == 0 && cfg.Accounts[i].LookbackDays == 0 {
			cfg.Accounts[i].RecentLimit = 20
		}
		if cfg.Accounts[i].MaxPages <= 0 {
			if cfg.Accounts[i].RecentLimit > 0 {
				cfg.Accounts[i].MaxPages = (cfg.Accounts[i].RecentLimit + 9) / 10
			} else {
				cfg.Accounts[i].MaxPages = 5
			}
		}
		if cfg.Accounts[i].MaxPages < 1 {
			cfg.Accounts[i].MaxPages = 1
		}
	}
	for i := range cfg.Notify.Channels {
		if strings.TrimSpace(cfg.Notify.Channels[i].Method) == "" {
			cfg.Notify.Channels[i].Method = "POST"
		}
	}
}

func validateConfig(cfg Config) error {
	if _, err := time.ParseDuration(cfg.Interval); err != nil {
		return fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
	}
	if _, err := time.ParseDuration(cfg.Network.Timeout); err != nil {
		return fmt.Errorf("invalid network.timeout %q: %w", cfg.Network.Timeout, err)
	}
	if _, err := time.ParseDuration(cfg.TelegramBot.PollTimeout); err != nil {
		return fmt.Errorf("invalid telegram_bot.poll_timeout %q: %w", cfg.TelegramBot.PollTimeout, err)
	}

	switch strings.ToLower(cfg.Network.IPFamily) {
	case "auto", "ipv4", "ipv6":
	default:
		return fmt.Errorf("network.ip_family must be auto, ipv4, or ipv6, got %q", cfg.Network.IPFamily)
	}

	if len(cfg.Accounts) == 0 && !cfg.TelegramBot.Enabled {
		return errors.New("accounts must not be empty")
	}
	for i, account := range cfg.Accounts {
		uid := strings.TrimSpace(account.UID)
		if uid == "" {
			return fmt.Errorf("accounts[%d].uid must not be empty", i)
		}
		if !uidRe.MatchString(uid) {
			return fmt.Errorf("accounts[%d].uid must be numeric, got %q", i, account.UID)
		}
		if account.RecentLimit == 0 && account.LookbackDays == 0 {
			return fmt.Errorf("accounts[%d] must set recent_limit or lookback_days", i)
		}
	}
	return nil
}

func IntervalDuration(cfg Config) time.Duration {
	d, _ := time.ParseDuration(cfg.Interval)
	return d
}
