package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(dsn string) (*PostgresStore, error) {
	if dsn == "" {
		return nil, errors.New("postgres dsn is required; set database.dsn or database.dsn_env")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	store := &PostgresStore{pool: pool}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (p *PostgresStore) migrate(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS app_state (
  key text PRIMARY KEY,
  value text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS subscriptions (
  chat_id text PRIMARY KEY,
  username text,
  first_name text,
  last_name text,
  weibo_uid text,
  recent_limit integer NOT NULL DEFAULT 20,
  lookback_days integer NOT NULL DEFAULT 0,
  max_pages integer NOT NULL DEFAULT 2,
  include_reposts boolean NOT NULL DEFAULT true,
  waiting_for_uid boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_weibo_uid ON subscriptions (weibo_uid) WHERE weibo_uid IS NOT NULL AND weibo_uid <> '';

CREATE TABLE IF NOT EXISTS weibo_posts (
  post_key text PRIMARY KEY,
  target_name text,
  account_uid text NOT NULL,
  account_name text,
  url text NOT NULL,
  mobile_url text,
  mid text,
  bid text,
  post_text text,
  raw_text text,
  created_at_weibo timestamptz,
  raw_created text,
  first_seen_at timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  checked_at timestamptz,
  status text,
  http_status integer,
  final_url text,
  title text,
  evidence text
);

CREATE INDEX IF NOT EXISTS idx_weibo_posts_account_uid ON weibo_posts (account_uid);
CREATE INDEX IF NOT EXISTS idx_weibo_posts_created_at ON weibo_posts (created_at_weibo DESC NULLS LAST, first_seen_at DESC);

CREATE TABLE IF NOT EXISTS post_checks (
  id bigserial PRIMARY KEY,
  post_key text,
  checked_at timestamptz NOT NULL,
  target_name text,
  account_uid text,
  account_name text,
  url text NOT NULL,
  mobile_url text,
  mid text,
  bid text,
  post_text text,
  created_at_weibo timestamptz,
  status text NOT NULL,
  previous_status text,
  http_status integer,
  final_url text,
  title text,
  evidence text,
  snippet text,
  error text
);

CREATE INDEX IF NOT EXISTS idx_post_checks_post_key ON post_checks (post_key, checked_at DESC);
CREATE INDEX IF NOT EXISTS idx_post_checks_checked_at ON post_checks (checked_at DESC);
`)
	return err
}

func (p *PostgresStore) Previous(target Target) (StateEntry, bool) {
	key := targetKey(target)
	entry, ok := p.entryByKey(key)
	return entry, ok
}

func (p *PostgresStore) TrackDiscovered(post DiscoveredPost, now time.Time) (StateEntry, bool) {
	key := postKey(post.AccountUID, post.MID, post.ID, post.URL)
	existing, found := p.entryByKey(key)
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := p.pool.Exec(ctx, `
INSERT INTO weibo_posts (
  post_key, target_name, account_uid, account_name, url, mobile_url, mid, bid,
  post_text, raw_text, created_at_weibo, raw_created, first_seen_at, last_seen_at,
  checked_at, status, http_status, final_url, title, evidence
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
ON CONFLICT (post_key) DO UPDATE SET
  target_name = COALESCE(NULLIF(EXCLUDED.target_name, ''), weibo_posts.target_name),
  account_uid = EXCLUDED.account_uid,
  account_name = COALESCE(NULLIF(EXCLUDED.account_name, ''), weibo_posts.account_name),
  url = EXCLUDED.url,
  mobile_url = COALESCE(NULLIF(EXCLUDED.mobile_url, ''), weibo_posts.mobile_url),
  mid = COALESCE(NULLIF(EXCLUDED.mid, ''), weibo_posts.mid),
  bid = COALESCE(NULLIF(EXCLUDED.bid, ''), weibo_posts.bid),
  post_text = COALESCE(NULLIF(EXCLUDED.post_text, ''), weibo_posts.post_text),
  raw_text = COALESCE(NULLIF(EXCLUDED.raw_text, ''), weibo_posts.raw_text),
  created_at_weibo = COALESCE(EXCLUDED.created_at_weibo, weibo_posts.created_at_weibo),
  raw_created = COALESCE(NULLIF(EXCLUDED.raw_created, ''), weibo_posts.raw_created),
  last_seen_at = EXCLUDED.last_seen_at
`, key, existing.TargetName, existing.AccountUID, existing.AccountName, existing.URL, existing.MobileURL,
		existing.MID, existing.BID, existing.PostText, existing.RawText, nullableTime(existing.CreatedAt),
		existing.RawCreated, existing.FirstSeenAt, existing.LastSeenAt, nullableTime(existing.CheckedAt),
		nullableString(existing.Status), nullableInt(existing.HTTPStatus), nullableString(existing.FinalURL),
		nullableString(existing.Title), nullableString(existing.Evidence))
	if err != nil {
		fmt.Printf("postgres TrackDiscovered failed: %v\n", err)
	}
	return existing, found
}

func (p *PostgresStore) TrackedPosts(accounts []Account) []StateEntry {
	allowed := map[string]bool{}
	for _, account := range accounts {
		allowed[account.UID] = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := p.pool.Query(ctx, `
SELECT target_name, account_uid, account_name, url, mobile_url, mid, bid, post_text,
       raw_text, created_at_weibo, raw_created, first_seen_at, last_seen_at,
       checked_at, status, http_status, final_url, title, evidence
FROM weibo_posts
ORDER BY COALESCE(created_at_weibo, first_seen_at) DESC
`)
	if err != nil {
		fmt.Printf("postgres TrackedPosts failed: %v\n", err)
		return nil
	}
	defer rows.Close()

	var entries []StateEntry
	for rows.Next() {
		entry, err := scanStateEntry(rows)
		if err != nil {
			fmt.Printf("postgres TrackedPosts scan failed: %v\n", err)
			continue
		}
		if len(allowed) > 0 && !allowed[entry.AccountUID] {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func (p *PostgresStore) BotUpdateOffset() int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var value string
	err := p.pool.QueryRow(ctx, `SELECT value FROM app_state WHERE key = 'telegram_update_offset'`).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		fmt.Printf("postgres BotUpdateOffset failed: %v\n", err)
		return 0
	}
	offset, _ := strconv.ParseInt(value, 10, 64)
	return offset
}

func (p *PostgresStore) SetBotUpdateOffset(offset int64) error {
	if offset <= p.BotUpdateOffset() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := p.pool.Exec(ctx, `
INSERT INTO app_state (key, value, updated_at)
VALUES ('telegram_update_offset', $1, now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
`, strconv.FormatInt(offset, 10))
	return err
}

func (p *PostgresStore) UpsertSubscription(sub Subscription) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	if sub.CreatedAt.IsZero() {
		sub.CreatedAt = now
	}
	if sub.UpdatedAt.IsZero() {
		sub.UpdatedAt = now
	}
	_, err := p.pool.Exec(ctx, `
INSERT INTO subscriptions (
  chat_id, username, first_name, last_name, weibo_uid, recent_limit, lookback_days,
  max_pages, include_reposts, waiting_for_uid, created_at, updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (chat_id) DO UPDATE SET
  username = EXCLUDED.username,
  first_name = EXCLUDED.first_name,
  last_name = EXCLUDED.last_name,
  weibo_uid = EXCLUDED.weibo_uid,
  recent_limit = EXCLUDED.recent_limit,
  lookback_days = EXCLUDED.lookback_days,
  max_pages = EXCLUDED.max_pages,
  include_reposts = EXCLUDED.include_reposts,
  waiting_for_uid = EXCLUDED.waiting_for_uid,
  updated_at = EXCLUDED.updated_at
`, sub.ChatID, nullableString(sub.Username), nullableString(sub.FirstName), nullableString(sub.LastName),
		nullableString(sub.UID), sub.RecentLimit, sub.LookbackDays, sub.MaxPages, sub.IncludeReposts,
		sub.WaitingForUID, sub.CreatedAt, sub.UpdatedAt)
	return err
}

func (p *PostgresStore) Subscription(chatID string) (Subscription, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row := p.pool.QueryRow(ctx, `
SELECT chat_id, username, first_name, last_name, weibo_uid, recent_limit, lookback_days,
       max_pages, include_reposts, waiting_for_uid, created_at, updated_at
FROM subscriptions WHERE chat_id = $1
`, chatID)
	sub, err := scanSubscription(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, false
	}
	if err != nil {
		fmt.Printf("postgres Subscription failed: %v\n", err)
		return Subscription{}, false
	}
	return sub, true
}

func (p *PostgresStore) DeleteSubscription(chatID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := p.pool.Exec(ctx, `DELETE FROM subscriptions WHERE chat_id = $1`, chatID)
	return err
}

func (p *PostgresStore) Subscriptions() []Subscription {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := p.pool.Query(ctx, `
SELECT chat_id, username, first_name, last_name, weibo_uid, recent_limit, lookback_days,
       max_pages, include_reposts, waiting_for_uid, created_at, updated_at
FROM subscriptions
ORDER BY weibo_uid, chat_id
`)
	if err != nil {
		fmt.Printf("postgres Subscriptions failed: %v\n", err)
		return nil
	}
	defer rows.Close()

	var subs []Subscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			fmt.Printf("postgres Subscriptions scan failed: %v\n", err)
			continue
		}
		subs = append(subs, sub)
	}
	return subs
}

func (p *PostgresStore) AccountsFromSubscriptions() []Account {
	byUID := map[string]Account{}
	for _, sub := range p.Subscriptions() {
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
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].UID < accounts[j].UID })
	return accounts
}

func (p *PostgresStore) ChatIDsForUID(uid string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := p.pool.Query(ctx, `SELECT chat_id FROM subscriptions WHERE weibo_uid = $1 ORDER BY chat_id`, uid)
	if err != nil {
		fmt.Printf("postgres ChatIDsForUID failed: %v\n", err)
		return nil
	}
	defer rows.Close()
	var chatIDs []string
	for rows.Next() {
		var chatID string
		if err := rows.Scan(&chatID); err == nil {
			chatIDs = append(chatIDs, chatID)
		}
	}
	return chatIDs
}

func (p *PostgresStore) SaveCheck(record CheckRecord) error {
	key := postKey(record.AccountUID, record.MID, "", record.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
INSERT INTO post_checks (
  post_key, checked_at, target_name, account_uid, account_name, url, mobile_url, mid, bid,
  post_text, created_at_weibo, status, previous_status, http_status, final_url, title,
  evidence, snippet, error
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
`, key, record.CheckedAt, record.TargetName, record.AccountUID, nullableString(record.AccountName),
		record.URL, nullableString(record.MobileURL), nullableString(record.MID), nullableString(record.BID),
		nullableString(record.PostText), nullableTime(record.CreatedAt), record.Status,
		nullableString(record.PreviousStatus), nullableInt(record.HTTPStatus), nullableString(record.FinalURL),
		nullableString(record.Title), record.Evidence, nullableString(record.Snippet), nullableString(record.Error))
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
INSERT INTO weibo_posts (
  post_key, target_name, account_uid, account_name, url, mobile_url, mid, bid,
  post_text, created_at_weibo, first_seen_at, last_seen_at, checked_at, status,
  http_status, final_url, title, evidence
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
ON CONFLICT (post_key) DO UPDATE SET
  target_name = EXCLUDED.target_name,
  account_uid = EXCLUDED.account_uid,
  account_name = COALESCE(NULLIF(EXCLUDED.account_name, ''), weibo_posts.account_name),
  url = EXCLUDED.url,
  mobile_url = COALESCE(NULLIF(EXCLUDED.mobile_url, ''), weibo_posts.mobile_url),
  mid = COALESCE(NULLIF(EXCLUDED.mid, ''), weibo_posts.mid),
  bid = COALESCE(NULLIF(EXCLUDED.bid, ''), weibo_posts.bid),
  post_text = COALESCE(NULLIF(EXCLUDED.post_text, ''), weibo_posts.post_text),
  created_at_weibo = COALESCE(EXCLUDED.created_at_weibo, weibo_posts.created_at_weibo),
  checked_at = EXCLUDED.checked_at,
  status = EXCLUDED.status,
  http_status = EXCLUDED.http_status,
  final_url = EXCLUDED.final_url,
  title = EXCLUDED.title,
  evidence = EXCLUDED.evidence
`, key, record.TargetName, record.AccountUID, nullableString(record.AccountName), record.URL,
		nullableString(record.MobileURL), nullableString(record.MID), nullableString(record.BID),
		nullableString(record.PostText), nullableTime(record.CreatedAt), record.CheckedAt, record.CheckedAt,
		record.CheckedAt, record.Status, nullableInt(record.HTTPStatus), nullableString(record.FinalURL),
		nullableString(record.Title), record.Evidence)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *PostgresStore) entryByKey(key string) (StateEntry, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	row := p.pool.QueryRow(ctx, `
SELECT target_name, account_uid, account_name, url, mobile_url, mid, bid, post_text,
       raw_text, created_at_weibo, raw_created, first_seen_at, last_seen_at,
       checked_at, status, http_status, final_url, title, evidence
FROM weibo_posts WHERE post_key = $1
`, key)
	entry, err := scanStateEntry(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return StateEntry{}, false
	}
	if err != nil {
		fmt.Printf("postgres entryByKey failed: %v\n", err)
		return StateEntry{}, false
	}
	return entry, true
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanStateEntry(row rowScanner) (StateEntry, error) {
	var targetName, accountUID, accountName, url, mobileURL, mid, bid sql.NullString
	var postText, rawText, rawCreated, status, finalURL, title, evidence sql.NullString
	var createdAt, firstSeenAt, lastSeenAt, checkedAt sql.NullTime
	var httpStatus sql.NullInt64
	err := row.Scan(&targetName, &accountUID, &accountName, &url, &mobileURL, &mid, &bid,
		&postText, &rawText, &createdAt, &rawCreated, &firstSeenAt, &lastSeenAt,
		&checkedAt, &status, &httpStatus, &finalURL, &title, &evidence)
	if err != nil {
		return StateEntry{}, err
	}
	return StateEntry{
		TargetName:  nullString(targetName),
		AccountUID:  nullString(accountUID),
		AccountName: nullString(accountName),
		URL:         nullString(url),
		MobileURL:   nullString(mobileURL),
		MID:         nullString(mid),
		BID:         nullString(bid),
		PostText:    nullString(postText),
		RawText:     nullString(rawText),
		CreatedAt:   nullTime(createdAt),
		RawCreated:  nullString(rawCreated),
		FirstSeenAt: nullTime(firstSeenAt),
		LastSeenAt:  nullTime(lastSeenAt),
		CheckedAt:   nullTime(checkedAt),
		Status:      nullString(status),
		HTTPStatus:  int(nullInt(httpStatus)),
		FinalURL:    nullString(finalURL),
		Title:       nullString(title),
		Evidence:    nullString(evidence),
	}, nil
}

func scanSubscription(row rowScanner) (Subscription, error) {
	var chatID string
	var username, firstName, lastName, uid sql.NullString
	var recentLimit, lookbackDays, maxPages int
	var includeReposts, waitingForUID bool
	var createdAt, updatedAt time.Time
	err := row.Scan(&chatID, &username, &firstName, &lastName, &uid, &recentLimit, &lookbackDays,
		&maxPages, &includeReposts, &waitingForUID, &createdAt, &updatedAt)
	if err != nil {
		return Subscription{}, err
	}
	return Subscription{
		ChatID:         chatID,
		Username:       nullString(username),
		FirstName:      nullString(firstName),
		LastName:       nullString(lastName),
		UID:            nullString(uid),
		RecentLimit:    recentLimit,
		LookbackDays:   lookbackDays,
		MaxPages:       maxPages,
		IncludeReposts: includeReposts,
		WaitingForUID:  waitingForUID,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func nullString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullInt(value sql.NullInt64) int64 {
	if !value.Valid {
		return 0
	}
	return value.Int64
}

func nullTime(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time
}
