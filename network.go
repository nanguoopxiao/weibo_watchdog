package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

func NewHTTPClient(cfg NetworkConfig) (*http.Client, error) {
	timeout, err := time.ParseDuration(nonEmpty(cfg.Timeout, defaultTimeout))
	if err != nil {
		return nil, err
	}

	network := "tcp"
	switch strings.ToLower(strings.TrimSpace(cfg.IPFamily)) {
	case "", "auto":
		network = "tcp"
	case "ipv4":
		network = "tcp4"
	case "ipv6":
		network = "tcp6"
	default:
		return nil, fmt.Errorf("invalid ip_family %q", cfg.IPFamily)
	}

	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, _ string, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	proxy := strings.TrimSpace(cfg.Proxy)
	if strings.EqualFold(proxy, "none") {
		transport.Proxy = nil
	} else if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		Jar:       jar,
	}, nil
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
