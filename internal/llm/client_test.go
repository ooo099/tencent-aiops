package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wsy/aiosight/internal/config"
)

func testRequest() Request {
	return Request{
		Metric: "http_latency_ms",
		Series: []float64{100, 101, 99, 100, 380},
		Score:  5.2,
		Mean:   100,
		Std:    2,
	}
}

func TestMockDiagnoseWithoutKey(t *testing.T) {
	c := New(config.LLMConfig{})
	text, err := c.Diagnose(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("无密钥时应走本地兜底，却返回错误: %v", err)
	}
	if !strings.Contains(text, "本地诊断") {
		t.Fatalf("兜底文本不符合预期: %s", text)
	}
}

func TestCallSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("鉴权头不正确: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"根因是连接池耗尽"}}]}`))
	}))
	defer srv.Close()

	c := New(config.LLMConfig{Endpoint: srv.URL, APIKey: "test-key", Timeout: 2 * time.Second, Model: "m"})
	text, err := c.Diagnose(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if text != "根因是连接池耗尽" {
		t.Fatalf("返回内容不符: %s", text)
	}
}

func TestRetryThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := New(config.LLMConfig{Endpoint: srv.URL, APIKey: "k", Timeout: 2 * time.Second})
	text, err := c.Diagnose(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if text != "ok" || atomic.LoadInt32(&calls) != 3 {
		t.Fatalf("期望第 3 次成功，实际 calls=%d text=%s", calls, text)
	}
}

func TestCircuitBreakerOpens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(config.LLMConfig{Endpoint: srv.URL, APIKey: "k", Timeout: time.Second})
	for i := 0; i < maxFailures; i++ {
		if _, err := c.Diagnose(context.Background(), testRequest()); err == nil {
			t.Fatal("持续失败时应返回错误")
		}
	}

	_, err := c.Diagnose(context.Background(), testRequest())
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("熔断后应返回 ErrCircuitOpen，实际 %v", err)
	}

	failures, open := c.State(time.Now())
	if failures < maxFailures || !open {
		t.Fatalf("熔断状态不正确 failures=%d open=%v", failures, open)
	}
}

func TestRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"late"}}]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	c := New(config.LLMConfig{Endpoint: srv.URL, APIKey: "k", Timeout: time.Second})
	if _, err := c.Diagnose(ctx, testRequest()); err == nil {
		t.Fatal("上下文超时应返回错误")
	}
}
