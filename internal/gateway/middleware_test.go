package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterBurstAndRefill(t *testing.T) {
	l := NewLimiter(10, 2)
	now := time.Now()

	if !l.Allow("1.1.1.1", now) || !l.Allow("1.1.1.1", now) {
		t.Fatal("突发额度内应放行")
	}
	if l.Allow("1.1.1.1", now) {
		t.Fatal("令牌耗尽后应拒绝")
	}
	// 其他 key 互不影响
	if !l.Allow("2.2.2.2", now) {
		t.Fatal("不同 key 应独立计数")
	}
	// 200ms 补充 2 个令牌
	if !l.Allow("1.1.1.1", now.Add(200*time.Millisecond)) {
		t.Fatal("补充令牌后应放行")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	l := NewLimiter(1, 1)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), RateLimit(l, newTestRegistry()))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "3.3.3.3:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("首次请求应 200，实际 %d", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("第二次请求应 429，实际 %d", rec2.Code)
	}
}

func TestRequestIDGenerated(t *testing.T) {
	var got string
	h := Chain(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = RequestIDFrom(r.Context())
	}), RequestID())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got == "" {
		t.Fatal("应生成请求 ID")
	}
	if rec.Header().Get("X-Request-Id") != got {
		t.Fatal("响应头里的 ID 应与上下文一致")
	}
}
