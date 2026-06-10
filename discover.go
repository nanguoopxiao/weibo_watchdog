package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type timelineResponse struct {
	OK   int          `json:"ok"`
	Data timelineData `json:"data"`
}

type timelineData struct {
	Cards []timelineCard `json:"cards"`
}

type timelineCard struct {
	CardType  int            `json:"card_type"`
	Mblog     *timelineMblog `json:"mblog"`
	CardGroup []timelineCard `json:"card_group"`
}

type timelineMblog struct {
	ID              any            `json:"id"`
	MID             any            `json:"mid"`
	BID             any            `json:"bid"`
	CreatedAt       any            `json:"created_at"`
	Text            any            `json:"text"`
	TextRaw         any            `json:"text_raw"`
	RetweetedStatus *timelineMblog `json:"retweeted_status"`
}

var (
	minutesAgoRe = regexp.MustCompile(`^(\d+)\s*分钟前$`)
	hoursAgoRe   = regexp.MustCompile(`^(\d+)\s*小时前$`)
	secondsAgoRe = regexp.MustCompile(`^(\d+)\s*秒前$`)
	todayRe      = regexp.MustCompile(`^今天\s*(\d{1,2}):(\d{2})$`)
	yesterdayRe  = regexp.MustCompile(`^昨天\s*(\d{1,2}):(\d{2})$`)
	monthDayRe   = regexp.MustCompile(`^(\d{1,2})-(\d{1,2})(?:\s+(\d{1,2}):(\d{2}))?$`)
	requestIDRe  = regexp.MustCompile(`request_id\s*=\s*"([^"]+)"`)
)

var discoverEndpointBase = "https://m.weibo.cn/api/container/getIndex"
var visitorEndpointBase = "https://visitor.passport.weibo.cn"

func DiscoverAccountPosts(client *http.Client, userAgent string, account Account, now time.Time) ([]DiscoveredPost, error) {
	maxPages := account.MaxPages
	if maxPages <= 0 {
		maxPages = 1
	}

	var posts []DiscoveredPost
	seen := map[string]bool{}
	cutoff := time.Time{}
	if account.LookbackDays > 0 {
		cutoff = now.Add(-time.Duration(account.LookbackDays) * 24 * time.Hour)
	}

	for page := 1; page <= maxPages; page++ {
		pagePosts, err := discoverAccountPage(client, userAgent, account, page)
		if isTimelineVisitorBlocked(err) {
			if visitorErr := ensureWeiboVisitor(client, userAgent, account.UID); visitorErr == nil {
				pagePosts, err = discoverAccountPage(client, userAgent, account, page)
			} else {
				err = fmt.Errorf("%w; visitor bootstrap failed: %v", err, visitorErr)
			}
		}
		if err != nil {
			if page == 1 {
				return posts, err
			}
			break
		}
		if len(pagePosts) == 0 {
			break
		}

		olderOnPage := 0
		for _, post := range pagePosts {
			key := postKey(post.AccountUID, post.MID, post.ID, post.URL)
			if seen[key] {
				continue
			}
			seen[key] = true

			if !account.IncludeReposts && post.IsRepost {
				continue
			}
			if !cutoff.IsZero() && !post.CreatedAt.IsZero() && post.CreatedAt.Before(cutoff) {
				olderOnPage++
				continue
			}

			posts = append(posts, post)
			if account.RecentLimit > 0 && len(posts) >= account.RecentLimit {
				return posts, nil
			}
		}

		if account.LookbackDays > 0 && olderOnPage == len(pagePosts) {
			break
		}
	}

	return posts, nil
}

func discoverAccountPage(client *http.Client, userAgent string, account Account, page int) ([]DiscoveredPost, error) {
	values := url.Values{}
	values.Set("type", "uid")
	values.Set("value", account.UID)
	values.Set("containerid", "107603"+account.UID)
	values.Set("page", strconv.Itoa(page))
	endpoint := discoverEndpointBase + "?" + values.Encode()

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://m.weibo.cn/u/"+account.UID)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, timelineHTTPError{StatusCode: resp.StatusCode, Body: snippet(string(body), 180)}
	}

	var timeline timelineResponse
	if err := json.Unmarshal(body, &timeline); err != nil {
		return nil, fmt.Errorf("timeline JSON parse failed: %w", err)
	}
	if timeline.OK == 0 && len(timeline.Data.Cards) == 0 {
		return nil, fmt.Errorf("timeline returned ok=0")
	}

	var posts []DiscoveredPost
	for _, card := range timeline.Data.Cards {
		for _, mblog := range extractMblogs(card) {
			post := mblogToPost(account, mblog, time.Now())
			if post.URL == "" {
				continue
			}
			posts = append(posts, post)
		}
	}
	return posts, nil
}

type timelineHTTPError struct {
	StatusCode int
	Body       string
}

func (e timelineHTTPError) Error() string {
	return fmt.Sprintf("timeline HTTP %d: %s", e.StatusCode, e.Body)
}

func isTimelineVisitorBlocked(err error) bool {
	if err == nil {
		return false
	}
	if typed, ok := err.(timelineHTTPError); ok {
		return typed.StatusCode == 432
	}
	return false
}

func ensureWeiboVisitor(client *http.Client, userAgent string, uid string) error {
	returnURL := "https://m.weibo.cn/u/" + uid
	visitorURL := visitorEndpointBase + "/visitor/visitor?entry=sinawap&a=enter&url=" +
		url.QueryEscape(returnURL) +
		"&domain=.weibo.cn&sudaref=&ua=php-sso_sdk_client-0.6.36&_rand=" +
		strconv.FormatInt(time.Now().UnixMilli(), 10)

	requestID := ""
	req, err := http.NewRequest(http.MethodGet, visitorURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := client.Do(req)
	if err == nil {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode >= 400 {
			return fmt.Errorf("visitor page HTTP %d", resp.StatusCode)
		}
		if match := requestIDRe.FindSubmatch(body); len(match) == 2 {
			requestID = string(match[1])
		}
	} else {
		return err
	}

	form := url.Values{}
	form.Set("cb", "visitor_gray_callback")
	form.Set("ver", "20250916")
	form.Set("request_id", requestID)
	form.Set("tid", "")
	form.Set("from", "weibo")
	form.Set("webdriver", "false")
	form.Set("rid", strconv.FormatInt(time.Now().UnixMilli(), 10))
	form.Set("return_url", returnURL)

	req, err = http.NewRequest(http.MethodPost, visitorEndpointBase+"/visitor/genvisitor2", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Accept", "text/javascript, application/javascript, */*")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", visitorURL)

	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("visitor genvisitor2 HTTP %d: %s", resp.StatusCode, snippet(string(body), 180))
	}
	if !bytes.Contains(body, []byte(`"retcode":20000000`)) {
		return fmt.Errorf("visitor genvisitor2 did not return success: %s", snippet(string(body), 180))
	}
	return nil
}

func extractMblogs(card timelineCard) []*timelineMblog {
	var out []*timelineMblog
	if card.Mblog != nil {
		out = append(out, card.Mblog)
	}
	for _, child := range card.CardGroup {
		out = append(out, extractMblogs(child)...)
	}
	return out
}

func mblogToPost(account Account, mblog *timelineMblog, now time.Time) DiscoveredPost {
	id := asString(mblog.ID)
	mid := asString(mblog.MID)
	bid := asString(mblog.BID)
	if mid == "" {
		mid = id
	}

	rawText := firstNonEmpty(asString(mblog.TextRaw), asString(mblog.Text))
	text := htmlToText(rawText)
	rawCreated := asString(mblog.CreatedAt)
	createdAt := parseWeiboTime(rawCreated, now)
	urlID := firstNonEmpty(mid, id, bid)

	return DiscoveredPost{
		AccountUID:  account.UID,
		AccountName: account.Name,
		ID:          id,
		MID:         mid,
		BID:         bid,
		URL:         "https://weibo.com/" + account.UID + "/" + urlID,
		MobileURL:   "https://m.weibo.cn/status/" + urlID,
		Text:        text,
		RawText:     rawText,
		CreatedAt:   createdAt,
		RawCreated:  rawCreated,
		IsRepost:    mblog.RetweetedStatus != nil,
	}
}

func asString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		if typed == math.Trunc(typed) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return fmt.Sprint(typed)
	}
}

func parseWeiboTime(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if raw == "刚刚" {
		return now
	}
	if match := secondsAgoRe.FindStringSubmatch(raw); len(match) == 2 {
		value, _ := strconv.Atoi(match[1])
		return now.Add(-time.Duration(value) * time.Second)
	}
	if match := minutesAgoRe.FindStringSubmatch(raw); len(match) == 2 {
		value, _ := strconv.Atoi(match[1])
		return now.Add(-time.Duration(value) * time.Minute)
	}
	if match := hoursAgoRe.FindStringSubmatch(raw); len(match) == 2 {
		value, _ := strconv.Atoi(match[1])
		return now.Add(-time.Duration(value) * time.Hour)
	}

	loc := now.Location()
	if match := todayRe.FindStringSubmatch(raw); len(match) == 3 {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	}
	if match := yesterdayRe.FindStringSubmatch(raw); len(match) == 3 {
		hour, _ := strconv.Atoi(match[1])
		minute, _ := strconv.Atoi(match[2])
		day := now.AddDate(0, 0, -1)
		return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
	}
	if match := monthDayRe.FindStringSubmatch(raw); len(match) == 5 {
		month, _ := strconv.Atoi(match[1])
		day, _ := strconv.Atoi(match[2])
		hour := 0
		minute := 0
		if match[3] != "" {
			hour, _ = strconv.Atoi(match[3])
			minute, _ = strconv.Atoi(match[4])
		}
		return time.Date(now.Year(), time.Month(month), day, hour, minute, 0, 0, loc)
	}

	layouts := []string{
		"Mon Jan 02 15:04:05 -0700 2006",
		"2006-01-02 15:04",
		"2006-01-02",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
