package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const maxBodyBytes = 2 * 1024 * 1024

var (
	titleRe       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	scriptStyleRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>|<noscript[^>]*>.*?</noscript>`)
	tagRe         = regexp.MustCompile(`(?is)<[^>]+>`)
	spaceRe       = regexp.MustCompile(`\s+`)
)

func ProbeHTTP(client *http.Client, userAgent string, target Target) (ProbeResult, string) {
	req, err := http.NewRequest(http.MethodGet, target.URL, nil)
	if err != nil {
		return ProbeResult{Error: err.Error()}, "invalid target URL"
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return ProbeResult{Error: err.Error()}, "network request failed"
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return ProbeResult{
			StatusCode: resp.StatusCode,
			FinalURL:   finalURL(resp),
			Error:      err.Error(),
		}, "response body read failed"
	}

	raw := string(body)
	text := htmlToText(raw)
	result := ProbeResult{
		StatusCode: resp.StatusCode,
		FinalURL:   finalURL(resp),
		Title:      extractTitle(raw),
		Text:       text,
		Snippet:    snippet(text, 360),
	}
	status, evidence := Classify(target, result)
	return result, fmt.Sprintf("%s: %s", status, evidence)
}

func ProbeVisibility(client *http.Client, userAgent string, target Target) (ProbeResult, string, string) {
	primary, _ := ProbeHTTP(client, userAgent, target)
	status, evidence := Classify(target, primary)
	if isDecisiveStatus(status) || target.MobileURL == "" || target.MobileURL == target.URL {
		return primary, status, evidence
	}

	if target.MID != "" {
		api, apiErr := ProbeStatusAPI(client, userAgent, target)
		if apiErr == nil {
			apiStatus, apiEvidence := Classify(target, api)
			if isDecisiveStatus(apiStatus) {
				return api, apiStatus, "status API fallback: " + apiEvidence
			}
		}
	}

	mobileTarget := target
	mobileTarget.URL = target.MobileURL
	mobile, _ := ProbeHTTP(client, userAgent, mobileTarget)
	mobileStatus, mobileEvidence := Classify(mobileTarget, mobile)
	if isDecisiveStatus(mobileStatus) {
		return mobile, mobileStatus, "mobile fallback: " + mobileEvidence
	}

	return primary, status, evidence
}

func ProbeStatusAPI(client *http.Client, userAgent string, target Target) (ProbeResult, error) {
	statusID := firstNonEmpty(target.MID, target.BID)
	endpoint := "https://m.weibo.cn/statuses/show?id=" + url.QueryEscape(statusID)
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", target.MobileURL)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := client.Do(req)
	if err != nil {
		return ProbeResult{Error: err.Error()}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return ProbeResult{StatusCode: resp.StatusCode, FinalURL: finalURL(resp), Error: err.Error()}, err
	}

	text := statusAPIText(body)
	if text == "" {
		text = htmlToText(string(body))
	}
	return ProbeResult{
		StatusCode: resp.StatusCode,
		FinalURL:   finalURL(resp),
		Title:      "m.weibo.cn/statuses/show",
		Text:       text,
		Snippet:    snippet(text, 360),
	}, nil
}

func statusAPIText(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	source := payload
	if data, ok := payload["data"].(map[string]any); ok {
		source = data
	}

	text := firstNonEmpty(asString(source["text_raw"]), asString(source["text"]))
	return htmlToText(text)
}

func Classify(target Target, result ProbeResult) (string, string) {
	if result.Error != "" {
		return StatusNetworkError, result.Error
	}

	finalLower := strings.ToLower(result.FinalURL)
	text := result.Text

	if containsAnyLower(finalLower, []string{"sorry", "pagenotfound", "notfound"}) {
		return StatusPublicInvisible, "final URL contains sorry/pagenotfound marker"
	}

	if result.StatusCode == http.StatusForbidden || result.StatusCode == http.StatusNotFound || result.StatusCode == http.StatusGone {
		return StatusPublicInvisible, fmt.Sprintf("HTTP status is %d", result.StatusCode)
	}

	if containsAny(text, []string{"验证码", "访问过于频繁", "安全验证", "异常访问", "访问受限", "请稍后再试"}) {
		return StatusRateLimited, "page contains anti-abuse or verification marker"
	}

	expected := expectedTexts(target)
	if matched, item := containsExpectedText(text, expected); matched {
		return StatusPublicVisible, "expected text was found in public page: " + snippet(item, 60)
	}

	if containsAny(text, []string{"该微博不存在", "暂无查看权限", "页面不存在", "内容不存在", "仅自己可见", "由于作者设置", "作者已删除"}) {
		return StatusPublicInvisible, "page contains invisible/deleted marker"
	}

	if containsAny(text, []string{"微博登录", "登录微博", "立即登录", "注册微博", "请输入手机号"}) {
		return StatusLoginWall, "page appears to be a login wall and expected text was not found"
	}

	if result.StatusCode >= 500 {
		return StatusUnknown, fmt.Sprintf("server returned HTTP %d", result.StatusCode)
	}

	if result.StatusCode >= 200 && result.StatusCode < 300 && len(expected) == 0 {
		return StatusLikelyVisible, "HTTP 2xx and no invisible/login markers; no expected_text configured"
	}

	return StatusUnknown, "no decisive marker found"
}

func isDecisiveStatus(status string) bool {
	switch status {
	case StatusPublicVisible, StatusPublicInvisible, StatusRateLimited:
		return true
	default:
		return false
	}
}

func htmlToText(raw string) string {
	cleaned := scriptStyleRe.ReplaceAllString(raw, " ")
	cleaned = tagRe.ReplaceAllString(cleaned, " ")
	cleaned = html.UnescapeString(cleaned)
	cleaned = spaceRe.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleaned)
}

func extractTitle(raw string) string {
	match := titleRe.FindStringSubmatch(raw)
	if len(match) < 2 {
		return ""
	}
	return snippet(html.UnescapeString(spaceRe.ReplaceAllString(match[1], " ")), 160)
}

func finalURL(resp *http.Response) string {
	if resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	return resp.Request.URL.String()
}

func expectedTexts(target Target) []string {
	var values []string
	if strings.TrimSpace(target.ExpectedText) != "" {
		values = append(values, target.ExpectedText)
		values = append(values, evidenceChunks(target.ExpectedText)...)
	}
	values = append(values, target.ExpectedTexts...)
	return values
}

func containsExpectedText(pageText string, candidates []string) (bool, string) {
	normalizedPage := normalizeEvidenceText(pageText)
	for _, item := range candidates {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(pageText, item) {
			return true, item
		}
		normalizedItem := normalizeEvidenceText(item)
		if normalizedItem != "" && strings.Contains(normalizedPage, normalizedItem) {
			return true, item
		}
	}
	return false, ""
}

func evidenceChunks(text string) []string {
	normalized := normalizeEvidenceText(text)
	if normalized == "" {
		return nil
	}
	runes := []rune(normalized)
	if len(runes) <= 18 {
		return []string{normalized}
	}
	chunkLen := 18
	var chunks []string
	for start := 0; start < len(runes); start += chunkLen {
		end := start + chunkLen
		if end > len(runes) {
			end = len(runes)
		}
		if end-start >= 8 {
			chunks = append(chunks, string(runes[start:end]))
		}
		if len(chunks) >= 3 {
			break
		}
	}
	return chunks
}

func normalizeEvidenceText(text string) string {
	var builder strings.Builder
	for _, r := range text {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func containsAny(text string, markers []string) bool {
	for _, marker := range markers {
		if marker != "" && strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func containsAnyLower(text string, markers []string) bool {
	lower := strings.ToLower(text)
	for _, marker := range markers {
		if marker != "" && strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func snippet(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}

	count := 0
	for idx := range text {
		if count == max {
			return strings.TrimSpace(text[:idx]) + "..."
		}
		count++
	}
	return text
}
