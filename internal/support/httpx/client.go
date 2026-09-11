package httpx

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base   *http.Client
	logger *slog.Logger
	module string
}

func New(logger *slog.Logger, module string) *Client {
	return &Client{
		base:   &http.Client{Timeout: 90 * time.Second},
		logger: logger,
		module: module,
	}
}

// NewWithTimeout — клиент с нестандартным общим таймаутом (перегруженные
// публичные API вроде Overpass отвечают десятки секунд при заявленных
// в QL таймаутах 30–60s; прежний хардкод 10s рвал запрос раньше ответа).
func NewWithTimeout(logger *slog.Logger, module string, timeout time.Duration) *Client {
	return &Client{
		base:   &http.Client{Timeout: timeout},
		logger: logger,
		module: module,
	}
}

func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	start := time.Now()
	req = req.WithContext(ctx)
	redactedURL := redactURL(req.URL.String())
	if c.logger != nil {
		c.logger.Debug("external request", "module", c.module, "method", req.Method, "url", redactedURL)
	}
	resp, err := c.base.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		if c.logger != nil {
			c.logger.Warn("external request failed", "module", c.module, "method", req.Method, "host", req.URL.Host, "elapsed_ms", elapsed, "error", err)
		}
		return nil, err
	}
	if c.logger != nil {
		c.logger.Info("external request", "module", c.module, "method", req.Method, "host", req.URL.Host, "path", req.URL.Path, "status", resp.StatusCode, "elapsed_ms", elapsed)
		if c.logger.Enabled(ctx, slog.LevelDebug) {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), resp.Body))
			c.logger.Debug("external response", "module", c.module, "status", resp.StatusCode, "body", string(body))
		}
	}
	return resp, nil
}

func redactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	q := u.Query()
	changed := false
	for k := range q {
		lk := strings.ToLower(k)
		if lk == "apikey" || lk == "api_key" || lk == "key" || lk == "token" {
			q.Set(k, "***")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.String()
}
