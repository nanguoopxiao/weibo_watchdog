package main

import (
	"fmt"
	"net/http"
	"time"
)

func CheckOnce(cfg Config, client *http.Client, store *Store, notifier Notifier) error {
	now := time.Now().UTC()

	for _, account := range cfg.Accounts {
		posts, err := DiscoverAccountPosts(client, cfg.UserAgent, account, now)
		if err != nil {
			fmt.Printf("discover failed for %s: %v\n", accountLabel(account), err)
			continue
		}

		newCount := 0
		for _, post := range posts {
			entry, found := store.TrackDiscovered(post, now)
			if !found {
				newCount++
				fmt.Printf("new post tracked: %s %s\n", accountLabel(account), entry.URL)
			}
		}
		if err := store.SaveState(); err != nil {
			return fmt.Errorf("save discovered posts for %s: %w", accountLabel(account), err)
		}
		fmt.Printf("discover %s: %d posts, %d new\n", accountLabel(account), len(posts), newCount)
	}

	for _, entry := range store.TrackedPosts(cfg.Accounts) {
		record, previousFound := checkEntry(cfg, client, entry)
		if err := store.SaveCheck(record); err != nil {
			return fmt.Errorf("save result for %s: %w", record.TargetName, err)
		}

		printRecord(record)
		if notifier.ShouldNotify(record, previousFound) {
			if err := notifier.Notify(record); err != nil {
				fmt.Printf("notify failed for %s: %v\n", record.TargetName, err)
			} else {
				fmt.Printf("notify sent for %s\n", record.TargetName)
			}
		}
	}
	return nil
}

func RunLoop(cfg Config, client *http.Client, store *Store, notifier Notifier) error {
	interval := IntervalDuration(cfg)
	for {
		started := time.Now()
		fmt.Printf("check started at %s\n", started.Format(time.RFC3339))
		if err := CheckOnce(cfg, client, store, notifier); err != nil {
			return err
		}

		elapsed := time.Since(started)
		sleepFor := interval - elapsed
		if sleepFor < time.Second {
			sleepFor = time.Second
		}
		fmt.Printf("next check in %s\n", sleepFor.Round(time.Second))
		time.Sleep(sleepFor)
	}
}

func checkEntry(cfg Config, client *http.Client, entry StateEntry) (CheckRecord, bool) {
	target := targetFromEntry(entry)
	probe, status, evidence := ProbeVisibility(client, cfg.UserAgent, target)
	previousFound := entry.Status != ""
	record := CheckRecord{
		CheckedAt:      time.Now().UTC(),
		TargetName:     target.Name,
		AccountUID:     target.AccountUID,
		AccountName:    target.AccountName,
		URL:            target.URL,
		MobileURL:      target.MobileURL,
		MID:            target.MID,
		BID:            target.BID,
		PostText:       target.ExpectedText,
		CreatedAt:      entry.CreatedAt,
		Status:         status,
		PreviousStatus: entry.Status,
		HTTPStatus:     probe.StatusCode,
		FinalURL:       probe.FinalURL,
		Title:          probe.Title,
		Evidence:       evidence,
		Snippet:        probe.Snippet,
		Error:          probe.Error,
	}
	if !previousFound {
		record.PreviousStatus = ""
	}
	return record, previousFound
}

func printRecord(record CheckRecord) {
	prev := record.PreviousStatus
	if prev == "" {
		prev = "-"
	}
	fmt.Printf("[%s] %s\n", record.Status, nonEmpty(record.TargetName, record.URL))
	fmt.Printf("  previous: %s\n", prev)
	fmt.Printf("  http: %d\n", record.HTTPStatus)
	fmt.Printf("  final: %s\n", record.FinalURL)
	fmt.Printf("  evidence: %s\n", record.Evidence)
	if record.Snippet != "" {
		fmt.Printf("  snippet: %s\n", record.Snippet)
	}
}

func targetLabel(target Target) string {
	if target.Name != "" {
		return target.Name
	}
	if target.MID != "" {
		return target.MID
	}
	return target.URL
}

func accountLabel(account Account) string {
	if account.Name != "" {
		return account.Name + "(" + account.UID + ")"
	}
	return account.UID
}
