package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type Notifier struct {
	cfg    NotifyConfig
	client *http.Client
}

func NewNotifier(cfg NotifyConfig, client *http.Client) Notifier {
	return Notifier{cfg: cfg, client: client}
}

func (n Notifier) ShouldNotify(record CheckRecord, previousFound bool) bool {
	if !n.cfg.Enabled {
		return false
	}
	if !previousFound {
		if record.Status == StatusPublicInvisible {
			return true
		}
		return n.cfg.OnFirstCheck
	}
	if record.PreviousStatus == record.Status {
		return false
	}
	if record.Status == StatusPublicInvisible {
		return true
	}
	if isVisibleLike(record.Status) {
		return n.cfg.OnRecover && record.PreviousStatus == StatusPublicInvisible
	}
	if isUnknownLike(record.Status) {
		return n.cfg.OnUnknown
	}
	return false
}

func (n Notifier) Notify(record CheckRecord) error {
	if !n.cfg.Enabled {
		return nil
	}

	var errs []string
	enabledChannels := 0
	for _, channel := range n.cfg.Channels {
		if !channel.Enabled {
			continue
		}
		enabledChannels++
		if err := n.notifyChannel(channel, record); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", channel.Type, err))
		}
	}

	if enabledChannels == 0 {
		return errors.New("no enabled notification channels")
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (n Notifier) notifyChannel(channel NotifyChannelConfig, record CheckRecord) error {
	switch strings.ToLower(strings.TrimSpace(channel.Type)) {
	case "ntfy":
		return n.notifyNtfy(channel, record)
	case "telegram":
		return n.notifyTelegram(channel, record)
	case "webhook":
		return n.notifyWebhook(channel, record)
	default:
		return fmt.Errorf("unsupported channel type %q", channel.Type)
	}
}

func (n Notifier) notifyNtfy(channel NotifyChannelConfig, record CheckRecord) error {
	endpoint := strings.TrimSpace(channel.URL)
	if endpoint == "" {
		server := strings.TrimRight(nonEmpty(channel.Server, "https://ntfy.sh"), "/")
		if strings.TrimSpace(channel.Topic) == "" {
			return errors.New("ntfy topic is required when url is empty")
		}
		endpoint = server + "/" + url.PathEscape(channel.Topic)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(notificationBody(record)))
	if err != nil {
		return err
	}
	req.Header.Set("Title", mime.BEncoding.Encode("utf-8", notificationTitle(record)))
	req.Header.Set("Click", record.URL)
	if channel.Priority != "" {
		req.Header.Set("Priority", channel.Priority)
	}
	if len(channel.Tags) > 0 {
		req.Header.Set("Tags", strings.Join(channel.Tags, ","))
	}
	applyAuth(req, channel)

	return expectSuccess(n.client.Do(req))
}

func (n Notifier) notifyTelegram(channel NotifyChannelConfig, record CheckRecord) error {
	botToken := resolveConfigValue(channel.BotToken, channel.BotTokenEnv)
	chatID := resolveConfigValue(channel.ChatID, channel.ChatIDEnv)
	if strings.TrimSpace(botToken) == "" || strings.TrimSpace(chatID) == "" {
		return errors.New("telegram bot_token/chat_id or bot_token_env/chat_id_env are required")
	}
	base := strings.TrimRight(nonEmpty(channel.URL, "https://api.telegram.org"), "/")
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", base, botToken)

	payload := map[string]any{
		"chat_id": chatID,
		"text":    notificationTitle(record) + "\n\n" + notificationBody(record),
	}
	raw, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return expectSuccess(n.client.Do(req))
}

func (n Notifier) notifyWebhook(channel NotifyChannelConfig, record CheckRecord) error {
	if strings.TrimSpace(channel.URL) == "" {
		return errors.New("webhook url is required")
	}

	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	method := strings.ToUpper(nonEmpty(channel.Method, http.MethodPost))
	req, err := http.NewRequest(method, channel.URL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range channel.Headers {
		req.Header.Set(key, value)
	}
	applyAuth(req, channel)

	return expectSuccess(n.client.Do(req))
}

func applyAuth(req *http.Request, channel NotifyChannelConfig) {
	if channel.Token != "" {
		req.Header.Set("Authorization", "Bearer "+channel.Token)
	}
	if channel.Username != "" || channel.Password != "" {
		req.SetBasicAuth(channel.Username, channel.Password)
	}
}

func expectSuccess(resp *http.Response, err error) error {
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := ioReadSmall(resp)
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
}

func ioReadSmall(resp *http.Response) (string, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(resp.Body)
	if err != nil {
		return "", err
	}
	text := buf.String()
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return text, nil
}

func notificationTitle(record CheckRecord) string {
	name := strings.TrimSpace(record.TargetName)
	if name == "" {
		name = record.MID
	}
	if name == "" {
		name = "微博"
	}
	if record.PreviousStatus != "" {
		return fmt.Sprintf("微博状态变化: %s %s -> %s", name, record.PreviousStatus, record.Status)
	}
	return fmt.Sprintf("微博状态: %s %s", name, record.Status)
}

func notificationBody(record CheckRecord) string {
	lines := []string{
		"状态: " + record.Status,
		"链接: " + record.URL,
		"证据: " + record.Evidence,
	}
	if record.AccountUID != "" {
		account := record.AccountUID
		if record.AccountName != "" && record.AccountName != record.AccountUID {
			account = record.AccountName + "(" + record.AccountUID + ")"
		}
		lines = append(lines, "账号: "+account)
	}
	if record.FinalURL != "" && record.FinalURL != record.URL {
		lines = append(lines, "最终地址: "+record.FinalURL)
	}
	if record.PostText != "" {
		lines = append(lines, "正文: "+snippet(record.PostText, 120))
	}
	if record.Title != "" {
		lines = append(lines, "标题: "+record.Title)
	}
	if record.Snippet != "" {
		lines = append(lines, "片段: "+record.Snippet)
	}
	return strings.Join(lines, "\n")
}

func isUnknownLike(status string) bool {
	switch status {
	case StatusUnknown, StatusNetworkError, StatusLoginWall, StatusRateLimited:
		return true
	default:
		return false
	}
}

func isVisibleLike(status string) bool {
	return status == StatusPublicVisible || status == StatusLikelyVisible
}

func resolveConfigValue(value, envName string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	if strings.TrimSpace(envName) == "" {
		return ""
	}
	return os.Getenv(envName)
}
