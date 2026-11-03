// Command router 是 aiosight 的分片网关：
// 按指标名做一致性哈希，把同一个指标的写入与查询固定路由到同一台实例，
// 多实例部署时保证数据不分裂；实例增减只影响相邻分片。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/wsy/aiosight/internal/metrics"
	"github.com/wsy/aiosight/internal/ring"
)

func main() {
	addr := flag.String("addr", env("AIOSIGHT_ROUTER_ADDR", ":8081"), "监听地址")
	backendsFlag := flag.String("backends", env("AIOSIGHT_BACKENDS", "http://127.0.0.1:18080"), "后端实例，逗号分隔")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	backends := splitAndTrim(*backendsFlag)
	if len(backends) == 0 {
		log.Error("没有配置后端实例")
		os.Exit(1)
	}

	r := ring.New(150)
	for _, b := range backends {
		r.Add(b)
	}

	reg := metrics.New()
	var roundRobin uint64

	proxyFor := func(target string) *httputil.ReverseProxy {
		u, err := url.Parse(target)
		if err != nil {
			log.Error("后端地址非法", "target", target, "error", err)
			os.Exit(1)
		}
		return &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = u.Scheme
				req.URL.Host = u.Host
				req.Host = u.Host
			},
			ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
				reg.Inc("router_upstream_errors_total", map[string]string{"backend": target})
				log.Error("转发失败", "backend", target, "error", err)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
			},
			Transport: &http.Transport{
				MaxIdleConns:        256,
				MaxIdleConnsPerHost: 64,
				IdleConnTimeout:     60 * time.Second,
			},
		}
	}

	proxies := make(map[string]*httputil.ReverseProxy, len(backends))
	for _, b := range backends {
		proxies[b] = proxyFor(b)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/healthz":
			writeJSON(w, map[string]any{"status": "ok", "backends": r.Nodes()})
			return
		case req.URL.Path == "/api/v1/alerts" && req.Method == http.MethodGet:
			// 告警分散在各个分片上，网关做 scatter-gather 后合并返回。
			mergeAlerts(w, req, backends)
			return
		case req.URL.Path == "/metrics":
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, _ = w.Write([]byte(reg.Render()))
			return
		}

		metric, body, err := extractMetric(req)
		if err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
		}

		target := ""
		if metric != "" {
			target = r.Get(metric)
			reg.Inc("router_routed_total", map[string]string{"mode": "hash"})
		}
		if target == "" {
			// 无法确定分片时退化为轮询，仅用于健康检查等无状态请求。
			target = backends[int(atomic.AddUint64(&roundRobin, 1))%len(backends)]
			reg.Inc("router_routed_total", map[string]string{"mode": "round_robin"})
		}

		reg.Inc("router_requests_total", nil)
		proxies[target].ServeHTTP(w, req)
	})

	srv := &http.Server{Addr: *addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Info("router 已启动", "addr", *addr, "backends", backends)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("router 退出", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	_ = srv.Close()
	log.Info("router 已停止")
}

// extractMetric 从请求里取出指标名；同时把 body 读出来，便于二次读取。
func extractMetric(req *http.Request) (string, []byte, error) {
	if req.Method == http.MethodGet {
		return req.URL.Query().Get("metric"), nil, nil
	}
	if !strings.HasPrefix(req.URL.Path, "/api/") {
		return "", nil, nil
	}

	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		return "", nil, err
	}
	if len(body) == 0 {
		return "", body, nil
	}

	var payload struct {
		Metric string `json:"metric"`
		Points []struct {
			Metric string `json:"metric"`
		} `json:"points"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", body, err
	}
	if payload.Metric != "" {
		return payload.Metric, body, nil
	}
	if len(payload.Points) > 0 {
		return payload.Points[0].Metric, body, nil
	}
	return "", body, nil
}

// mergeAlerts 并发拉取所有分片的告警并合并排序。
func mergeAlerts(w http.ResponseWriter, req *http.Request, backends []string) {
	limit := 20
	if v := req.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	type result struct {
		alerts []json.RawMessage
		err    bool
	}
	results := make([]result, len(backends))

	var wg sync.WaitGroup
	for i, b := range backends {
		wg.Add(1)
		go func(i int, base string) {
			defer wg.Done()
			resp, err := http.Get(base + "/api/v1/alerts?limit=" + strconv.Itoa(limit))
			if err != nil {
				results[i] = result{err: true}
				return
			}
			defer resp.Body.Close()
			var payload struct {
				Alerts []json.RawMessage `json:"alerts"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
				results[i] = result{err: true}
				return
			}
			results[i] = result{alerts: payload.Alerts}
		}(i, b)
	}
	wg.Wait()

	merged := make([]json.RawMessage, 0, limit*len(backends))
	for _, r := range results {
		merged = append(merged, r.alerts...)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		var a, b struct {
			At time.Time `json:"at"`
		}
		_ = json.Unmarshal(merged[i], &a)
		_ = json.Unmarshal(merged[j], &b)
		return a.At.After(b.At)
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"alerts": merged, "merged_from": len(backends)})
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, body any) { writeJSONStatus(w, http.StatusOK, body) }

func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
