package main

import "testing"

func TestClassifyPublicVisible(t *testing.T) {
	status, evidence := Classify(Target{ExpectedText: "这有什么好屏蔽的呢"}, ProbeResult{
		StatusCode: 200,
		FinalURL:   "https://weibo.com/1234567890/5307864509055967",
		Text:       "来自这太不朋克了 这有什么好屏蔽的呢 转发 评论",
	})
	if status != StatusPublicVisible {
		t.Fatalf("status = %s, want %s, evidence=%s", status, StatusPublicVisible, evidence)
	}
}

func TestClassifyPublicInvisibleByFinalURL(t *testing.T) {
	status, _ := Classify(Target{ExpectedText: "anything"}, ProbeResult{
		StatusCode: 200,
		FinalURL:   "https://weibo.com/sorry?pagenotfound",
		Text:       "微博",
	})
	if status != StatusPublicInvisible {
		t.Fatalf("status = %s, want %s", status, StatusPublicInvisible)
	}
}

func TestClassifyLoginWall(t *testing.T) {
	status, _ := Classify(Target{ExpectedText: "正文"}, ProbeResult{
		StatusCode: 200,
		FinalURL:   "https://weibo.com/123/456",
		Text:       "微博登录 立即登录 注册微博",
	})
	if status != StatusLoginWall {
		t.Fatalf("status = %s, want %s", status, StatusLoginWall)
	}
}

func TestClassifyLikelyVisibleWithoutExpectedText(t *testing.T) {
	status, _ := Classify(Target{}, ProbeResult{
		StatusCode: 200,
		FinalURL:   "https://weibo.com/123/456",
		Text:       "普通页面内容",
	})
	if status != StatusLikelyVisible {
		t.Fatalf("status = %s, want %s", status, StatusLikelyVisible)
	}
}
