package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type TelegramBot struct {
	baseURL string
	token   string
	client  *http.Client
}

type telegramGetUpdatesResponse struct {
	OK          bool             `json:"ok"`
	Description string           `json:"description"`
	Result      []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID int64        `json:"message_id"`
	Text      string       `json:"text"`
	Chat      telegramChat `json:"chat"`
	From      telegramUser `json:"from"`
}

type telegramChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type telegramUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func NewTelegramBot(cfg TelegramBotConfig, client *http.Client) (*TelegramBot, error) {
	token := resolveConfigValue(cfg.BotToken, cfg.BotTokenEnv)
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("telegram_bot.bot_token or telegram_bot.bot_token_env is required")
	}
	return &TelegramBot{
		baseURL: strings.TrimRight(nonEmpty(cfg.URL, "https://api.telegram.org"), "/"),
		token:   token,
		client:  client,
	}, nil
}

func (b *TelegramBot) GetUpdates(offset int64, timeout time.Duration) ([]telegramUpdate, error) {
	values := url.Values{}
	if offset > 0 {
		values.Set("offset", strconv.FormatInt(offset, 10))
	}
	values.Set("timeout", strconv.Itoa(int(timeout.Seconds())))
	values.Set("allowed_updates", `["message"]`)

	endpoint := fmt.Sprintf("%s/bot%s/getUpdates?%s", b.baseURL, b.token, values.Encode())
	resp, err := b.client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram getUpdates HTTP %d: %s", resp.StatusCode, snippet(string(body), 200))
	}

	var payload telegramGetUpdatesResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !payload.OK {
		return nil, fmt.Errorf("telegram getUpdates failed: %s", payload.Description)
	}
	return payload.Result, nil
}

func (b *TelegramBot) SendMessage(chatID, text string) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	raw, _ := json.Marshal(payload)

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", b.baseURL, b.token)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return expectSuccess(b.client.Do(req))
}

func RunTelegramBot(cfg Config, client *http.Client, store *Store) error {
	if !cfg.TelegramBot.Enabled {
		return errors.New("telegram_bot.enabled is false")
	}
	bot, err := NewTelegramBot(cfg.TelegramBot, client)
	if err != nil {
		return err
	}

	pollTimeout, _ := time.ParseDuration(cfg.TelegramBot.PollTimeout)
	interval := IntervalDuration(cfg)
	nextCheck := time.Now()
	fmt.Println("telegram bot started")

	for {
		if !time.Now().Before(nextCheck) {
			if err := CheckBotSubscriptions(cfg, client, store, bot); err != nil {
				fmt.Printf("bot monitor failed: %v\n", err)
			}
			nextCheck = time.Now().Add(interval)
		}

		updates, err := bot.GetUpdates(store.BotUpdateOffset(), pollTimeout)
		if err != nil {
			fmt.Printf("telegram polling failed: %v\n", err)
			time.Sleep(5 * time.Second)
			continue
		}
		for _, update := range updates {
			if err := handleTelegramUpdate(cfg, client, store, bot, update); err != nil {
				fmt.Printf("telegram update %d failed: %v\n", update.UpdateID, err)
			}
			if err := store.SetBotUpdateOffset(update.UpdateID + 1); err != nil {
				return err
			}
		}
	}
}

func handleTelegramUpdate(cfg Config, client *http.Client, store *Store, bot *TelegramBot, update telegramUpdate) error {
	if update.Message == nil {
		return nil
	}

	msg := update.Message
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	text := strings.TrimSpace(msg.Text)

	switch {
	case strings.HasPrefix(text, "/start"):
		sub := subscriptionFromMessage(cfg, *msg)
		sub.WaitingForUID = true
		if existing, ok := store.Subscription(chatID); ok && existing.UID != "" {
			sub.UID = existing.UID
		}
		if err := store.UpsertSubscription(sub); err != nil {
			return err
		}
		return bot.SendMessage(chatID, "请发送要监控的微博用户 UID。\n\nUID 是微博主页链接 https://weibo.com/u/数字 里的数字部分。我会默认监控最近 20 条微博，每 10 分钟检查一次。")

	case strings.HasPrefix(text, "/help"):
		return bot.SendMessage(chatID, "发送微博 UID 即可开始监控。\n/status 查看当前订阅\n/stop 停止监控")

	case strings.HasPrefix(text, "/status"):
		sub, ok := store.Subscription(chatID)
		if !ok || sub.UID == "" {
			return bot.SendMessage(chatID, "当前还没有订阅。请发送微博用户 UID。")
		}
		return bot.SendMessage(chatID, fmt.Sprintf("当前监控 UID：%s\n最近条数：%d\n检查间隔：%s", sub.UID, sub.RecentLimit, cfg.Interval))

	case strings.HasPrefix(text, "/stop"):
		if err := store.DeleteSubscription(chatID); err != nil {
			return err
		}
		return bot.SendMessage(chatID, "已停止当前聊天的微博监控。发送 /start 可以重新订阅。")

	case uidRe.MatchString(text):
		sub := subscriptionFromMessage(cfg, *msg)
		sub.UID = text
		sub.WaitingForUID = false
		if err := store.UpsertSubscription(sub); err != nil {
			return err
		}
		posts, err := initializeSubscription(cfg, client, store, sub)
		if err != nil {
			return bot.SendMessage(chatID, fmt.Sprintf("已记录 UID：%s。\n初始化拉取最近微博时遇到问题：%v\n我会继续按 %s 定时重试。", sub.UID, err, cfg.Interval))
		}
		return bot.SendMessage(chatID, initialSubscriptionMessage(cfg, sub, posts))

	default:
		sub, ok := store.Subscription(chatID)
		if ok && sub.WaitingForUID {
			return bot.SendMessage(chatID, "UID 应该只包含数字。你可以从微博主页链接 https://weibo.com/u/数字 里复制数字部分。请重新发送。")
		}
		return bot.SendMessage(chatID, "我还不认识这个指令。发送 /start 开始，或直接发送微博用户 UID。")
	}
}

func subscriptionFromMessage(cfg Config, msg telegramMessage) Subscription {
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	username := msg.From.Username
	firstName := msg.From.FirstName
	lastName := msg.From.LastName
	if username == "" {
		username = msg.Chat.Username
	}
	if firstName == "" {
		firstName = msg.Chat.FirstName
	}
	if lastName == "" {
		lastName = msg.Chat.LastName
	}

	return Subscription{
		ChatID:         chatID,
		Username:       username,
		FirstName:      firstName,
		LastName:       lastName,
		RecentLimit:    cfg.TelegramBot.DefaultRecentLimit,
		LookbackDays:   cfg.TelegramBot.DefaultLookbackDays,
		MaxPages:       cfg.TelegramBot.DefaultMaxPages,
		IncludeReposts: cfg.TelegramBot.IncludeReposts,
		UpdatedAt:      time.Now().UTC(),
	}
}

func CheckBotSubscriptions(cfg Config, client *http.Client, store *Store, bot *TelegramBot) error {
	accounts := store.AccountsFromSubscriptions()
	if len(accounts) == 0 {
		fmt.Println("bot monitor: no subscriptions yet")
		return nil
	}

	now := time.Now().UTC()
	newPostsByUID := map[string][]DiscoveredPost{}
	for _, account := range accounts {
		posts, err := DiscoverAccountPosts(client, cfg.UserAgent, account, now)
		if err != nil {
			fmt.Printf("discover failed for %s: %v\n", accountLabel(account), err)
			continue
		}

		newCount := 0
		for _, post := range posts {
			_, found := store.TrackDiscovered(post, now)
			if !found {
				newCount++
				newPostsByUID[post.AccountUID] = append(newPostsByUID[post.AccountUID], post)
			}
		}
		if err := store.SaveState(); err != nil {
			return err
		}
		fmt.Printf("bot discover %s: %d posts, %d new\n", accountLabel(account), len(posts), newCount)
	}

	for uid, posts := range newPostsByUID {
		message := newPostsMessage(posts)
		for _, chatID := range store.ChatIDsForUID(uid) {
			if err := bot.SendMessage(chatID, message); err != nil {
				fmt.Printf("telegram new-post notice failed for chat %s: %v\n", chatID, err)
			}
		}
	}

	for _, entry := range store.TrackedPosts(accounts) {
		record, previousFound := checkEntry(cfg, client, entry)
		if err := store.SaveCheck(record); err != nil {
			return err
		}
		if shouldNotifyBotRecord(cfg, record, previousFound) {
			for _, chatID := range store.ChatIDsForUID(record.AccountUID) {
				if err := bot.SendMessage(chatID, notificationTitle(record)+"\n\n"+notificationBody(record)); err != nil {
					fmt.Printf("telegram notify failed for chat %s: %v\n", chatID, err)
				}
			}
		}
	}
	return nil
}

func initializeSubscription(cfg Config, client *http.Client, store *Store, sub Subscription) ([]DiscoveredPost, error) {
	account := Account{
		Name:           sub.UID,
		UID:            sub.UID,
		RecentLimit:    sub.RecentLimit,
		LookbackDays:   sub.LookbackDays,
		MaxPages:       sub.MaxPages,
		IncludeReposts: sub.IncludeReposts,
	}
	now := time.Now().UTC()
	posts, err := DiscoverAccountPosts(client, cfg.UserAgent, account, now)
	if err != nil {
		return nil, err
	}
	for _, post := range posts {
		store.TrackDiscovered(post, now)
	}
	if err := store.SaveState(); err != nil {
		return nil, err
	}
	return posts, nil
}

func initialSubscriptionMessage(cfg Config, sub Subscription, posts []DiscoveredPost) string {
	if len(posts) == 0 {
		return fmt.Sprintf("已记录 UID：%s。\n暂时没有识别到可记录的微博。我会继续每 %s 自动检查。", sub.UID, cfg.Interval)
	}
	return fmt.Sprintf(
		"已记录 UID：%s。\n初始化完成：已记录最近 %d 条微博，之后每 %s 自动检查。\n\n%s",
		sub.UID,
		len(posts),
		cfg.Interval,
		postSummaryList(posts, 8),
	)
}

func newPostsMessage(posts []DiscoveredPost) string {
	if len(posts) == 1 {
		return "已录入最近发布的微博：\n\n" + postSummaryList(posts, 1)
	}
	return fmt.Sprintf("已录入最近发布的 %d 条微博：\n\n%s", len(posts), postSummaryList(posts, 6))
}

func postSummaryList(posts []DiscoveredPost, limit int) string {
	if limit <= 0 {
		limit = len(posts)
	}
	if len(posts) < limit {
		limit = len(posts)
	}
	var lines []string
	for i := 0; i < limit; i++ {
		post := posts[i]
		text := snippet(normalizeSummaryText(post.Text), 72)
		if text == "" {
			text = "(无文字正文)"
		}
		timeLabel := post.RawCreated
		if timeLabel == "" && !post.CreatedAt.IsZero() {
			timeLabel = post.CreatedAt.Local().Format("01-02 15:04")
		}
		if timeLabel != "" {
			lines = append(lines, fmt.Sprintf("%d. [%s] %s\n%s", i+1, timeLabel, text, firstNonEmpty(post.URL, post.MobileURL)))
		} else {
			lines = append(lines, fmt.Sprintf("%d. %s\n%s", i+1, text, firstNonEmpty(post.URL, post.MobileURL)))
		}
	}
	if len(posts) > limit {
		lines = append(lines, fmt.Sprintf("还有 %d 条未展开。", len(posts)-limit))
	}
	return strings.Join(lines, "\n\n")
}

func normalizeSummaryText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	return spaceRe.ReplaceAllString(text, " ")
}

func shouldNotifyBotRecord(cfg Config, record CheckRecord, previousFound bool) bool {
	if !previousFound {
		return record.Status == StatusPublicInvisible
	}
	if record.PreviousStatus == record.Status {
		return false
	}
	if record.Status == StatusPublicInvisible {
		return true
	}
	if isVisibleLike(record.Status) {
		return cfg.Notify.OnRecover && record.PreviousStatus == StatusPublicInvisible
	}
	if isUnknownLike(record.Status) {
		return cfg.Notify.OnUnknown
	}
	return false
}
