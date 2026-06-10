package main

import "time"

const (
	StatusPublicVisible   = "PUBLIC_VISIBLE"
	StatusLikelyVisible   = "LIKELY_VISIBLE"
	StatusPublicInvisible = "PUBLIC_INVISIBLE"
	StatusLoginWall       = "LOGIN_WALL"
	StatusRateLimited     = "RATE_LIMITED"
	StatusUnknown         = "UNKNOWN"
	StatusNetworkError    = "NETWORK_ERROR"
)

type Config struct {
	Interval    string            `json:"interval"`
	DataDir     string            `json:"data_dir"`
	UserAgent   string            `json:"user_agent"`
	Network     NetworkConfig     `json:"network"`
	Accounts    []Account         `json:"accounts"`
	TelegramBot TelegramBotConfig `json:"telegram_bot"`
	Notify      NotifyConfig      `json:"notify"`
}

type NetworkConfig struct {
	Timeout  string `json:"timeout"`
	IPFamily string `json:"ip_family"`
	Proxy    string `json:"proxy"`
}

type Target struct {
	Name          string   `json:"name"`
	AccountUID    string   `json:"account_uid"`
	AccountName   string   `json:"account_name"`
	URL           string   `json:"url"`
	MobileURL     string   `json:"mobile_url"`
	MID           string   `json:"mid"`
	BID           string   `json:"bid"`
	ExpectedText  string   `json:"expected_text"`
	ExpectedTexts []string `json:"expected_texts"`
}

type Account struct {
	Name           string `json:"name"`
	UID            string `json:"uid"`
	RecentLimit    int    `json:"recent_limit"`
	LookbackDays   int    `json:"lookback_days"`
	MaxPages       int    `json:"max_pages"`
	IncludeReposts bool   `json:"include_reposts"`
}

type NotifyConfig struct {
	Enabled      bool                  `json:"enabled"`
	OnFirstCheck bool                  `json:"on_first_check"`
	OnRecover    bool                  `json:"on_recover"`
	OnUnknown    bool                  `json:"on_unknown"`
	Channels     []NotifyChannelConfig `json:"channels"`
}

type NotifyChannelConfig struct {
	Type        string            `json:"type"`
	Enabled     bool              `json:"enabled"`
	URL         string            `json:"url"`
	Server      string            `json:"server"`
	Topic       string            `json:"topic"`
	Token       string            `json:"token"`
	Username    string            `json:"username"`
	Password    string            `json:"password"`
	Priority    string            `json:"priority"`
	Tags        []string          `json:"tags"`
	BotToken    string            `json:"bot_token"`
	BotTokenEnv string            `json:"bot_token_env"`
	ChatID      string            `json:"chat_id"`
	ChatIDEnv   string            `json:"chat_id_env"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers"`
}

type TelegramBotConfig struct {
	Enabled             bool   `json:"enabled"`
	URL                 string `json:"url"`
	BotToken            string `json:"bot_token"`
	BotTokenEnv         string `json:"bot_token_env"`
	PollTimeout         string `json:"poll_timeout"`
	DefaultRecentLimit  int    `json:"default_recent_limit"`
	DefaultLookbackDays int    `json:"default_lookback_days"`
	DefaultMaxPages     int    `json:"default_max_pages"`
	IncludeReposts      bool   `json:"include_reposts"`
}

type ProbeResult struct {
	StatusCode int
	FinalURL   string
	Title      string
	Text       string
	Snippet    string
	Error      string
}

type CheckRecord struct {
	CheckedAt      time.Time `json:"checked_at"`
	TargetName     string    `json:"target_name"`
	AccountUID     string    `json:"account_uid,omitempty"`
	AccountName    string    `json:"account_name,omitempty"`
	URL            string    `json:"url"`
	MobileURL      string    `json:"mobile_url,omitempty"`
	MID            string    `json:"mid,omitempty"`
	BID            string    `json:"bid,omitempty"`
	PostText       string    `json:"post_text,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	Status         string    `json:"status"`
	PreviousStatus string    `json:"previous_status,omitempty"`
	HTTPStatus     int       `json:"http_status,omitempty"`
	FinalURL       string    `json:"final_url,omitempty"`
	Title          string    `json:"title,omitempty"`
	Evidence       string    `json:"evidence"`
	Snippet        string    `json:"snippet,omitempty"`
	Error          string    `json:"error,omitempty"`
}

type StateEntry struct {
	CheckedAt   time.Time `json:"checked_at,omitempty"`
	FirstSeenAt time.Time `json:"first_seen_at,omitempty"`
	LastSeenAt  time.Time `json:"last_seen_at,omitempty"`
	TargetName  string    `json:"target_name"`
	AccountUID  string    `json:"account_uid,omitempty"`
	AccountName string    `json:"account_name,omitempty"`
	URL         string    `json:"url"`
	MobileURL   string    `json:"mobile_url,omitempty"`
	MID         string    `json:"mid,omitempty"`
	BID         string    `json:"bid,omitempty"`
	PostText    string    `json:"post_text,omitempty"`
	RawText     string    `json:"raw_text,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	RawCreated  string    `json:"raw_created,omitempty"`
	Status      string    `json:"status,omitempty"`
	HTTPStatus  int       `json:"http_status,omitempty"`
	FinalURL    string    `json:"final_url,omitempty"`
	Title       string    `json:"title,omitempty"`
	Evidence    string    `json:"evidence,omitempty"`
}

type DiscoveredPost struct {
	AccountUID  string
	AccountName string
	ID          string
	MID         string
	BID         string
	URL         string
	MobileURL   string
	Text        string
	RawText     string
	CreatedAt   time.Time
	RawCreated  string
	IsRepost    bool
}

type BotState struct {
	UpdateOffset  int64                   `json:"update_offset"`
	Subscriptions map[string]Subscription `json:"subscriptions"`
}

type Subscription struct {
	ChatID         string    `json:"chat_id"`
	Username       string    `json:"username,omitempty"`
	FirstName      string    `json:"first_name,omitempty"`
	LastName       string    `json:"last_name,omitempty"`
	UID            string    `json:"uid,omitempty"`
	RecentLimit    int       `json:"recent_limit"`
	LookbackDays   int       `json:"lookback_days"`
	MaxPages       int       `json:"max_pages"`
	IncludeReposts bool      `json:"include_reposts"`
	WaitingForUID  bool      `json:"waiting_for_uid"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
