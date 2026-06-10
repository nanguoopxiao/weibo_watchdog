package main

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseWeiboTimeRelative(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 30, 0, 0, time.Local)
	got := parseWeiboTime("10分钟前", now)
	want := now.Add(-10 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestDiscoverAccountPostsBootstrapsVisitorOn432(t *testing.T) {
	apiCalls := 0
	visitorCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/container/getIndex":
			apiCalls++
			if _, err := r.Cookie("SUB"); err != nil {
				w.WriteHeader(432)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":1,"data":{"cards":[{"card_type":9,"mblog":{"id":"1","mid":"1","created_at":"今天 08:05","text_raw":"访客成功后微博"}}]}}`))
		case "/visitor/visitor":
			_, _ = w.Write([]byte(`var request_id = "rid-for-test";`))
		case "/visitor/genvisitor2":
			visitorCalls++
			http.SetCookie(w, &http.Cookie{Name: "SUB", Value: "visitor-sub", Path: "/"})
			_, _ = w.Write([]byte(`window.visitor_gray_callback && visitor_gray_callback({"retcode":20000000,"msg":"succ","data":{"sub":"visitor-sub","subp":"visitor-subp"}});`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldDiscover := discoverEndpointBase
	oldVisitor := visitorEndpointBase
	discoverEndpointBase = server.URL + "/api/container/getIndex"
	visitorEndpointBase = server.URL
	defer func() {
		discoverEndpointBase = oldDiscover
		visitorEndpointBase = oldVisitor
	}()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	client := server.Client()
	client.Jar = jar

	posts, err := DiscoverAccountPosts(client, defaultUA, Account{UID: "1234567890", RecentLimit: 1, MaxPages: 1}, time.Now())
	if err != nil {
		t.Fatalf("DiscoverAccountPosts() error = %v", err)
	}
	if len(posts) != 1 || posts[0].Text != "访客成功后微博" {
		t.Fatalf("posts = %+v", posts)
	}
	if apiCalls != 2 || visitorCalls != 1 {
		t.Fatalf("apiCalls=%d visitorCalls=%d", apiCalls, visitorCalls)
	}
}

func TestParseWeiboTimeToday(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 30, 0, 0, time.Local)
	got := parseWeiboTime("今天 08:05", now)
	want := time.Date(2026, 6, 9, 8, 5, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestMblogToPostBuildsURLsAndText(t *testing.T) {
	post := mblogToPost(Account{Name: "测试账号", UID: "1234567890"}, &timelineMblog{
		ID:        "5307864509055967",
		MID:       "5307864509055967",
		BID:       "P1abc",
		CreatedAt: "今天 08:05",
		Text:      "<span>这有什么好屏蔽的呢</span>",
	}, time.Date(2026, 6, 9, 12, 30, 0, 0, time.Local))

	if post.URL != "https://weibo.com/1234567890/5307864509055967" {
		t.Fatalf("URL = %s", post.URL)
	}
	if post.MobileURL != "https://m.weibo.cn/status/5307864509055967" {
		t.Fatalf("MobileURL = %s", post.MobileURL)
	}
	if post.Text != "这有什么好屏蔽的呢" {
		t.Fatalf("Text = %s", post.Text)
	}
}

func TestStatusAPITextWrappedData(t *testing.T) {
	body := []byte(`{"ok":1,"data":{"text_raw":"公开正文"}}`)
	if got := statusAPIText(body); got != "公开正文" {
		t.Fatalf("statusAPIText() = %q", got)
	}
}

func TestDiscoverAccountPageParsesCards(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":1,"data":{"cards":[{"card_type":9,"mblog":{"id":"1","mid":"1","created_at":"今天 08:05","text_raw":"正文一"}}]}}`))
	}))
	defer server.Close()

	old := discoverEndpointBase
	discoverEndpointBase = server.URL + "/api/container/getIndex"
	defer func() { discoverEndpointBase = old }()

	posts, err := discoverAccountPage(server.Client(), defaultUA, Account{UID: "1234567890"}, 1)
	if err != nil {
		t.Fatalf("discoverAccountPage() error = %v", err)
	}
	if len(posts) != 1 || posts[0].Text != "正文一" {
		t.Fatalf("unexpected posts: %+v", posts)
	}
}
