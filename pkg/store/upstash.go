package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"rapid-screening/pkg/domain"
)

const (
	upstashTimeout         = 1500 * time.Millisecond
	upstashRateLimitPrefix = "rs:rl:"
	upstashTrackPrefix     = "rs:track:"
	upstashRecentKey       = "rs:recent"
	upstashRecentMaxSize   = 20
)

type upstashClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type upstashCommandResponse struct {
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error,omitempty"`
}

func newUpstashClientFromEnv() *upstashClient {
	baseURL := strings.TrimSpace(os.Getenv("UPSTASH_REDIS_REST_URL"))
	token := strings.TrimSpace(os.Getenv("UPSTASH_REDIS_REST_TOKEN"))

	if baseURL == "" || token == "" {
		baseURL = strings.TrimSpace(os.Getenv("KV_REST_API_URL"))
		token = strings.TrimSpace(os.Getenv("KV_REST_API_TOKEN"))
	}

	if baseURL == "" || token == "" {
		return nil
	}

	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return nil
	}
	scheme := strings.ToLower(parsed.Scheme)
	if strings.EqualFold(strings.TrimSpace(os.Getenv("VERCEL_ENV")), "production") {
		if scheme != "https" {
			return nil
		}
	} else if scheme != "https" && scheme != "http" {
		return nil
	}

	return &upstashClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: upstashTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (c *upstashClient) exec(ctx context.Context, args ...any) (json.RawMessage, error) {
	if c == nil {
		return nil, errors.New("upstash client not configured")
	}

	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal upstash command: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create upstash request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute upstash request: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read upstash response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstash status %d: %s", resp.StatusCode, strings.TrimSpace(string(rawBody)))
	}

	var parsed upstashCommandResponse
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode upstash response: %w", err)
	}
	if parsed.Error != "" {
		return nil, fmt.Errorf("upstash error: %s", parsed.Error)
	}
	return parsed.Result, nil
}

func (c *upstashClient) getString(ctx context.Context, key string) (string, bool, error) {
	raw, err := c.exec(ctx, "GET", key)
	if err != nil {
		return "", false, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return "", false, nil
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return "", false, fmt.Errorf("decode upstash string: %w", err)
	}
	if val == "" {
		return "", false, nil
	}
	return val, true, nil
}

func (c *upstashClient) setString(ctx context.Context, key, val string, ttl time.Duration) error {
	secs := int(ttl.Seconds())
	if secs < 60 {
		secs = 60
	}
	_, err := c.exec(ctx, "SET", key, val, "EX", secs)
	return err
}

func (c *upstashClient) getTracking(code string) (*cacheEntry, bool) {
	if c == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), upstashTimeout)
	defer cancel()

	raw, ok, err := c.getString(ctx, upstashTrackPrefix+code)
	if err != nil || !ok {
		return nil, false
	}

	var entry cacheEntry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil || entry.Result == nil {
		return nil, false
	}
	return &entry, true
}

func (c *upstashClient) saveTracking(code string, entry *cacheEntry, ttl time.Duration, recent []domain.RecentTrackingSummary) {
	if c == nil || entry == nil || entry.Result == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), upstashTimeout)
	defer cancel()

	// Preserva o registro no Redis por pelo menos 2h (ou 24h se entregue) para suportar stale fallback em cold starts.
	storeTTL := ttl
	if storeTTL < 2*time.Hour {
		storeTTL = 2 * time.Hour
	}

	if payload, err := json.Marshal(entry); err == nil {
		_ = c.setString(ctx, upstashTrackPrefix+code, string(payload), storeTTL)
	}

	if len(recent) > 0 {
		if len(recent) > upstashRecentMaxSize {
			recent = recent[:upstashRecentMaxSize]
		}
		if recentPayload, err := json.Marshal(recent); err == nil {
			_ = c.setString(ctx, upstashRecentKey, string(recentPayload), 7*24*time.Hour)
		}
	}
}

func (c *upstashClient) getRecent(limit int) ([]domain.RecentTrackingSummary, bool) {
	if c == nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), upstashTimeout)
	defer cancel()

	raw, ok, err := c.getString(ctx, upstashRecentKey)
	if err != nil || !ok {
		return nil, false
	}

	var items []domain.RecentTrackingSummary
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, false
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, true
}

// checkRateLimit implementa rate limiting distribuído por janela fixa (60s) no Upstash Redis.
// Retorna (allowed, checked). Se checked == false (erro de rede/Redis), o chamador faz fallback ao limiter local.
func (c *upstashClient) checkRateLimit(ip string, maxPerWindow int, windowSecs int) (bool, bool) {
	if c == nil || ip == "" || maxPerWindow <= 0 || windowSecs <= 0 {
		return true, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	key := upstashRateLimitPrefix + ip
	raw, err := c.exec(ctx, "INCR", key)
	if err != nil {
		return true, false
	}
	var count int64
	if err := json.Unmarshal(raw, &count); err != nil {
		return true, false
	}
	if count == 1 {
		_, _ = c.exec(ctx, "EXPIRE", key, windowSecs)
	}
	return count <= int64(maxPerWindow), true
}
