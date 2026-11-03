// Package gateway 提供网关层常用的中间件：
// 请求 ID、日志、panic 恢复、限流、超时，对应"高可用/容灾"里的基础能力。
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/wsy/aiosight/internal/metrics"
)

// Middleware 是标准的 http 中间件签名。
type Middleware func(http.Handler) http.Handler

// Chain 按顺序套用中间件。
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey string

const requestIDKey ctxKey = "request_id"

// RequestID 为每个请求生成唯一 ID，贯穿日志与链路排查。
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-Id")
			if id == "" {
				id = newID()
			}
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
		})
	}
}

// RequestIDFrom 从上下文取出请求 ID。
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

// Recover 兜住 panic，避免单个请求拖垮进程。
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic recovered", "path", r.URL.Path, "panic", rec)
					http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder 记录响应码，供访问日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// AccessLog 记录结构化访问日志，并上报请求量指标。
func AccessLog(log *slog.Logger, reg *metrics.Registry) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)

			elapsed := time.Since(start)
			reg.Inc("http_requests_total", map[string]string{
				"method": r.Method,
				"status": statusClass(rec.status),
			})
			reg.Add("http_request_duration_seconds_sum", elapsed.Seconds(), nil)
			log.Info("request",
				"request_id", RequestIDFrom(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", elapsed.Milliseconds(),
				"remote", clientIP(r),
			)
		})
	}
}

// Timeout 给每个请求加超时，防止慢请求占满连接。
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Limiter 是令牌桶限流器，按 key（通常是客户端 IP）独立计数。
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64
	burst   float64
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter 创建限流器：rate 为每秒补充的令牌数，burst 为桶容量。
func NewLimiter(rate float64, burst int) *Limiter {
	if rate <= 0 {
		rate = 100
	}
	if burst <= 0 {
		burst = int(rate)
	}
	return &Limiter{buckets: make(map[string]*bucket), rate: rate, burst: float64(burst)}
}

// Allow 判断某个 key 当前是否放行。
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// RateLimit 用 Limiter 保护下游。
func RateLimit(l *Limiter, reg *metrics.Registry) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(clientIP(r), time.Now()) {
				reg.Inc("http_rate_limited_total", nil)
				w.Header().Set("Retry-After", "1")
				http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
