// Package llm 封装大模型调用：超时、重试、熔断，以及无密钥时的本地兜底。
// 在"AI 智能运维"里，它负责把异常数据翻译成人能看懂的根因分析。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wsy/aiosight/internal/config"
)

// ErrCircuitOpen 表示熔断器打开，请求被直接拒绝。
var ErrCircuitOpen = errors.New("llm: circuit breaker is open")

// Request 是一次诊断请求。
type Request struct {
	Metric string
	Series []float64
	Score  float64
	Mean   float64
	Std    float64
}

// Client 是带熔断与重试的模型客户端。
type Client struct {
	cfg  config.LLMConfig
	http *http.Client

	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

const (
	maxFailures  = 5
	openDuration = 30 * time.Second
	maxRetries   = 3
	breakerReset = 60 * time.Second
)

// New 创建客户端。
func New(cfg config.LLMConfig) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// Diagnose 返回一段根因分析文本。
func (c *Client) Diagnose(ctx context.Context, req Request) (string, error) {
	if !c.allow(time.Now()) {
		return "", ErrCircuitOpen
	}

	// 未配置密钥时走本地启发式分析，保证离线环境也能完整演示。
	if c.cfg.APIKey == "" || c.cfg.Endpoint == "" {
		return mockDiagnose(req), nil
	}

	var lastErr error
	backoff := 100 * time.Millisecond
	for attempt := 0; attempt < maxRetries; attempt++ {
		text, err := c.call(ctx, req)
		if err == nil {
			c.onSuccess()
			return text, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(backoff):
			backoff *= 3
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	c.onFailure(time.Now())
	return "", fmt.Errorf("llm: 重试 %d 次仍失败: %w", maxRetries, lastErr)
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *Client) call(ctx context.Context, req Request) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: "你是资深 SRE，请用中文给出简洁的根因分析与处置建议，不超过 200 字。"},
			{Role: "user", Content: buildPrompt(req)},
		},
		MaxTokens: c.cfg.MaxTokens,
	})
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("llm: 返回结果为空")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}

func buildPrompt(req Request) string {
	recent := req.Series
	if len(recent) > 10 {
		recent = recent[len(recent)-10:]
	}
	return fmt.Sprintf(
		"监控指标 %s 出现异常。\n最近采样值：%v\n基线均值：%.2f，标准差：%.2f，偏离 %.2f 个标准差。\n请判断可能原因并给出排查步骤。",
		req.Metric, recent, req.Mean, req.Std, req.Score,
	)
}

// mockDiagnose 是无密钥时的确定性兜底，便于离线演示与测试。
func mockDiagnose(req Request) string {
	trend := "平稳"
	if len(req.Series) >= 2 {
		first, last := req.Series[0], req.Series[len(req.Series)-1]
		switch {
		case last > first*1.2:
			trend = "持续上升"
		case last < first*0.8:
			trend = "持续下降"
		}
	}
	severity := "轻微"
	if req.Score >= 6 {
		severity = "严重"
	} else if req.Score >= 4 {
		severity = "中等"
	}
	ratio := 0.0
	if math.Abs(req.Mean) > 1e-9 {
		ratio = (req.Series[len(req.Series)-1] - req.Mean) / math.Abs(req.Mean) * 100
	}
	return fmt.Sprintf(
		"【本地诊断】指标 %s 当前偏离基线 %.2f 个标准差，趋势%s，偏离幅度约 %.1f%%，判定为%s异常。\n"+
			"可能原因：1) 下游依赖响应变慢导致请求堆积；2) 流量突增超过容量水位；3) 资源泄漏（连接/内存）逐步累积。\n"+
			"建议处置：先确认同时段 QPS 与错误率是否同步上升；再检查依赖服务 P99 与连接池占用；必要时对该接口限流并回滚最近变更。",
		req.Metric, req.Score, trend, ratio, severity,
	)
}

// ---- 熔断器 ----

func (c *Client) allow(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.openUntil) {
		return false
	}
	// 打开时间已过，进入半开状态：清空计数放行一次探测。
	if !c.openUntil.IsZero() && now.Sub(c.openUntil) > breakerReset {
		c.failures = 0
		c.openUntil = time.Time{}
	}
	return true
}

func (c *Client) onSuccess() {
	c.mu.Lock()
	c.failures = 0
	c.mu.Unlock()
}

func (c *Client) onFailure(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= maxFailures {
		c.openUntil = now.Add(openDuration)
	}
}

// State 返回当前熔断状态，供 /healthz 与指标使用。
func (c *Client) State(now time.Time) (failures int, open bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failures, now.Before(c.openUntil)
}
