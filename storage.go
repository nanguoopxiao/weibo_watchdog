package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Store struct {
	dataDir   string
	statePath string
	botPath   string
	logPath   string
	state     map[string]StateEntry
	botState  BotState
	pg        *PostgresStore
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	store := &Store{
		dataDir:   dataDir,
		statePath: filepath.Join(dataDir, "state.json"),
		botPath:   filepath.Join(dataDir, "bot.json"),
		logPath:   filepath.Join(dataDir, "checks.jsonl"),
		state:     map[string]StateEntry{},
		botState: BotState{
			Subscriptions: map[string]Subscription{},
		},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func NewStoreForConfig(cfg Config, dataDir string) (*Store, error) {
	switch strings.ToLower(cfg.Database.Type) {
	case "postgres", "postgresql":
		dsn := resolveConfigValue(cfg.Database.DSN, cfg.Database.DSNEnv)
		pg, err := NewPostgresStore(dsn)
		if err != nil {
			return nil, err
		}
		return &Store{pg: pg}, nil
	default:
		return NewStore(dataDir)
	}
}

func (s *Store) Previous(target Target) (StateEntry, bool) {
	if s.pg != nil {
		return s.pg.Previous(target)
	}
	entry, ok := s.state[targetKey(target)]
	return entry, ok
}

func (s *Store) TrackDiscovered(post DiscoveredPost, now time.Time) (StateEntry, bool) {
	if s.pg != nil {
		return s.pg.TrackDiscovered(post, now)
	}
	key := postKey(post.AccountUID, post.MID, post.ID, post.URL)
	existing, found := s.state[key]
	if !found {
		existing = StateEntry{
			FirstSeenAt: now,
			AccountUID:  post.AccountUID,
			AccountName: post.AccountName,
			URL:         post.URL,
			MobileURL:   post.MobileURL,
			MID:         firstNonEmpty(post.MID, post.ID),
			BID:         post.BID,
			TargetName:  targetNameFromPost(post),
		}
	}

	existing.LastSeenAt = now
	existing.AccountUID = post.AccountUID
	existing.AccountName = post.AccountName
	existing.URL = post.URL
	existing.MobileURL = post.MobileURL
	existing.MID = firstNonEmpty(post.MID, post.ID, existing.MID)
	existing.BID = firstNonEmpty(post.BID, existing.BID)
	existing.PostText = firstNonEmpty(post.Text, existing.PostText)
	existing.RawText = firstNonEmpty(post.RawText, existing.RawText)
	existing.RawCreated = firstNonEmpty(post.RawCreated, existing.RawCreated)
	if !post.CreatedAt.IsZero() {
		existing.CreatedAt = post.CreatedAt
	}
	existing.TargetName = firstNonEmpty(existing.TargetName, targetNameFromPost(post))
	s.state[key] = existing
	return existing, found
}

func (s *Store) TrackedPosts(accounts []Account) []StateEntry {
	if s.pg != nil {
		return s.pg.TrackedPosts(accounts)
	}
	allowed := map[string]bool{}
	for _, account := range accounts {
		allowed[account.UID] = true
	}

	var entries []StateEntry
	for _, entry := range s.state {
		if len(allowed) > 0 && !allowed[entry.AccountUID] {
			continue
		}
		if entry.URL == "" {
			continue
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		left := entries[i].CreatedAt
		right := entries[j].CreatedAt
		if left.IsZero() {
			left = entries[i].FirstSeenAt
		}
		if right.IsZero() {
			right = entries[j].FirstSeenAt
		}
		return left.After(right)
	})
	return entries
}

func (s *Store) BotUpdateOffset() int64 {
	if s.pg != nil {
		return s.pg.BotUpdateOffset()
	}
	return s.botState.UpdateOffset
}

func (s *Store) SetBotUpdateOffset(offset int64) error {
	if s.pg != nil {
		return s.pg.SetBotUpdateOffset(offset)
	}
	if offset > s.botState.UpdateOffset {
		s.botState.UpdateOffset = offset
		return s.saveBotState()
	}
	return nil
}

func (s *Store) UpsertSubscription(sub Subscription) error {
	if s.pg != nil {
		return s.pg.UpsertSubscription(sub)
	}
	if s.botState.Subscriptions == nil {
		s.botState.Subscriptions = map[string]Subscription{}
	}
	now := time.Now().UTC()
	existing, found := s.botState.Subscriptions[sub.ChatID]
	if found && !existing.CreatedAt.IsZero() {
		sub.CreatedAt = existing.CreatedAt
	} else if sub.CreatedAt.IsZero() {
		sub.CreatedAt = now
	}
	if sub.UpdatedAt.IsZero() {
		sub.UpdatedAt = now
	}
	s.botState.Subscriptions[sub.ChatID] = sub
	return s.saveBotState()
}

func (s *Store) Subscription(chatID string) (Subscription, bool) {
	if s.pg != nil {
		return s.pg.Subscription(chatID)
	}
	sub, ok := s.botState.Subscriptions[chatID]
	return sub, ok
}

func (s *Store) DeleteSubscription(chatID string) error {
	if s.pg != nil {
		return s.pg.DeleteSubscription(chatID)
	}
	if s.botState.Subscriptions == nil {
		return nil
	}
	delete(s.botState.Subscriptions, chatID)
	return s.saveBotState()
}

func (s *Store) Subscriptions() []Subscription {
	if s.pg != nil {
		return s.pg.Subscriptions()
	}
	var subs []Subscription
	for _, sub := range s.botState.Subscriptions {
		subs = append(subs, sub)
	}
	sort.Slice(subs, func(i, j int) bool {
		if subs[i].UID == subs[j].UID {
			return subs[i].ChatID < subs[j].ChatID
		}
		return subs[i].UID < subs[j].UID
	})
	return subs
}

func (s *Store) AccountsFromSubscriptions() []Account {
	if s.pg != nil {
		return s.pg.AccountsFromSubscriptions()
	}
	byUID := map[string]Account{}
	for _, sub := range s.botState.Subscriptions {
		if sub.UID == "" {
			continue
		}
		account := byUID[sub.UID]
		if account.UID == "" {
			account = Account{
				Name:           sub.UID,
				UID:            sub.UID,
				RecentLimit:    sub.RecentLimit,
				LookbackDays:   sub.LookbackDays,
				MaxPages:       sub.MaxPages,
				IncludeReposts: sub.IncludeReposts,
			}
		}
		if sub.RecentLimit > account.RecentLimit {
			account.RecentLimit = sub.RecentLimit
		}
		if sub.LookbackDays > account.LookbackDays {
			account.LookbackDays = sub.LookbackDays
		}
		if sub.MaxPages > account.MaxPages {
			account.MaxPages = sub.MaxPages
		}
		account.IncludeReposts = account.IncludeReposts || sub.IncludeReposts
		byUID[sub.UID] = account
	}

	var accounts []Account
	for _, account := range byUID {
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool {
		return accounts[i].UID < accounts[j].UID
	})
	return accounts
}

func (s *Store) ChatIDsForUID(uid string) []string {
	if s.pg != nil {
		return s.pg.ChatIDsForUID(uid)
	}
	var chatIDs []string
	for _, sub := range s.botState.Subscriptions {
		if sub.UID == uid {
			chatIDs = append(chatIDs, sub.ChatID)
		}
	}
	sort.Strings(chatIDs)
	return chatIDs
}

func (s *Store) SaveCheck(record CheckRecord) error {
	if s.pg != nil {
		return s.pg.SaveCheck(record)
	}
	if err := s.appendLog(record); err != nil {
		return err
	}

	key := postKey(record.AccountUID, record.MID, "", record.URL)
	entry := s.state[key]
	if entry.FirstSeenAt.IsZero() {
		entry.FirstSeenAt = record.CheckedAt
	}
	entry.CheckedAt = record.CheckedAt
	entry.TargetName = record.TargetName
	entry.AccountUID = record.AccountUID
	entry.AccountName = record.AccountName
	entry.URL = record.URL
	entry.MobileURL = record.MobileURL
	entry.MID = record.MID
	entry.BID = record.BID
	entry.PostText = firstNonEmpty(record.PostText, entry.PostText)
	entry.CreatedAt = firstNonZeroTime(record.CreatedAt, entry.CreatedAt)
	entry.Status = record.Status
	entry.HTTPStatus = record.HTTPStatus
	entry.FinalURL = record.FinalURL
	entry.Title = record.Title
	entry.Evidence = record.Evidence
	s.state[key] = entry
	return s.saveState()
}

func (s *Store) SaveState() error {
	if s.pg != nil {
		return nil
	}
	return s.saveState()
}

func (s *Store) Save(record CheckRecord) error {
	return s.SaveCheck(record)
}

func targetFromEntry(entry StateEntry) Target {
	return Target{
		Name:         entry.TargetName,
		AccountUID:   entry.AccountUID,
		AccountName:  entry.AccountName,
		URL:          entry.URL,
		MobileURL:    entry.MobileURL,
		MID:          entry.MID,
		BID:          entry.BID,
		ExpectedText: entry.PostText,
	}
}

func (s *Store) load() error {
	if err := s.loadState(); err != nil {
		return err
	}
	return s.loadBotState()
}

func (s *Store) loadState() error {
	raw, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, &s.state)
}

func (s *Store) loadBotState() error {
	raw, err := os.ReadFile(s.botPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &s.botState); err != nil {
		return err
	}
	if s.botState.Subscriptions == nil {
		s.botState.Subscriptions = map[string]Subscription{}
	}
	return nil
}

func (s *Store) appendLog(record CheckRecord) error {
	file, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	enc := json.NewEncoder(writer)
	if err := enc.Encode(record); err != nil {
		return err
	}
	return writer.Flush()
}

func (s *Store) saveState() error {
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}

	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.statePath); err != nil {
		_ = os.Remove(s.statePath)
		return os.Rename(tmp, s.statePath)
	}
	return nil
}

func (s *Store) saveBotState() error {
	raw, err := json.MarshalIndent(s.botState, "", "  ")
	if err != nil {
		return err
	}

	tmp := s.botPath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.botPath); err != nil {
		_ = os.Remove(s.botPath)
		return os.Rename(tmp, s.botPath)
	}
	return nil
}

func targetKey(target Target) string {
	return postKey(target.AccountUID, target.MID, "", target.URL)
}

func postKey(uid, mid, id, url string) string {
	if uid != "" && mid != "" {
		return uid + ":" + mid
	}
	if uid != "" && id != "" {
		return uid + ":" + id
	}
	return url
}

func targetNameFromPost(post DiscoveredPost) string {
	label := firstNonEmpty(post.AccountName, post.AccountUID)
	if label == post.AccountUID {
		label = ""
	}
	text := snippet(post.Text, 32)
	if text == "" {
		text = firstNonEmpty(post.MID, post.ID, post.BID)
	}
	if label == "" {
		return text
	}
	if text == "" {
		return label
	}
	return label + " / " + text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
