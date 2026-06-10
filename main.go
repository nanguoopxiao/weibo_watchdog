package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "check":
		if err := runCheck(os.Args[2:]); err != nil {
			fatal(err)
		}
	case "run":
		if err := runDaemon(os.Args[2:]); err != nil {
			fatal(err)
		}
	case "bot":
		if err := runBot(os.Args[2:]); err != nil {
			fatal(err)
		}
	case "send-test":
		if err := runSendTest(os.Args[2:]); err != nil {
			fatal(err)
		}
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func runCheck(args []string) error {
	cfg, client, store, notifier, err := setup(args)
	if err != nil {
		return err
	}
	return CheckOnce(cfg, client, store, notifier)
}

func runDaemon(args []string) error {
	cfg, client, store, notifier, err := setup(args)
	if err != nil {
		return err
	}
	return RunLoop(cfg, client, store, notifier)
}

func runBot(args []string) error {
	cfg, client, store, _, err := setup(args)
	if err != nil {
		return err
	}
	return RunTelegramBot(cfg, client, store)
}

func runSendTest(args []string) error {
	cfg, _, _, notifier, err := setup(args)
	if err != nil {
		return err
	}
	if !cfg.Notify.Enabled {
		return fmt.Errorf("notify.enabled is false")
	}
	record := CheckRecord{
		CheckedAt:      time.Now().UTC(),
		TargetName:     "推送测试",
		URL:            "https://weibo.com/",
		Status:         StatusUnknown,
		PreviousStatus: StatusPublicVisible,
		Evidence:       "this is a notification test",
		Snippet:        "如果你在手机端看到这条消息，说明推送通道已经连通。",
	}
	return notifier.Notify(record)
}

func setup(args []string) (Config, *http.Client, *Store, Notifier, error) {
	fs := flag.NewFlagSet("weibo-monitor", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "path to config JSON")
	if err := fs.Parse(args); err != nil {
		return Config{}, nil, nil, Notifier{}, err
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return Config{}, nil, nil, Notifier{}, err
	}

	client, err := NewHTTPClient(cfg.Network)
	if err != nil {
		return Config{}, nil, nil, Notifier{}, err
	}

	dataDir := cfg.DataDir
	if !filepath.IsAbs(dataDir) {
		base := filepath.Dir(*configPath)
		if base == "." || base == "" {
			base = "."
		}
		dataDir = filepath.Join(base, dataDir)
	}

	store, err := NewStore(dataDir)
	if err != nil {
		return Config{}, nil, nil, Notifier{}, err
	}
	return cfg, client, store, NewNotifier(cfg.Notify, client), nil
}

func usage() {
	fmt.Println(`weibo-monitor

Usage:
  weibo-monitor check [-config config.json]      run one check and exit
  weibo-monitor run [-config config.json]        keep checking by interval
  weibo-monitor bot [-config config.json]        run Telegram bot subscription service
  weibo-monitor send-test [-config config.json]  send a test notification

Config defaults to ./config.json.`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
