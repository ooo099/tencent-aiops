// Package api 暴露 HTTP 接口：指标写入、查询、告警、诊断与运维端点。
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/wsy/aiosight/internal/alert"
	"github.com/wsy/aiosight/internal/anomaly"
	"github.com/wsy/aiosight/internal/config"
	"github.com/wsy/aiosight/internal/gateway"
	"github.com/wsy/aiosight/internal/llm"
	"github.com/wsy/aiosight/internal/metrics"
	"github.com/wsy/aiosight/internal/store"
)

// Server 组合了所有依赖，便于测试时注入替身。
type Server struct {
	cfg   config.Config
	log   *slog.Logger
	reg   *metrics.Registry
	kv    *store.KV
	det   *anomaly.Detector
	ale   *alert.Engine
	model *llm.Client

	mu     sync.Mutex
	recent []alert.Alert
}

// New 构造 Server。
func New(cfg config.Config, log *slog.Logger, reg *metrics.Registry, kv *store.KV, det *anomaly.Detector, ale *alert.Engine, model *llm.Client) *Server {
	return &Server{cfg: cfg, log: log, reg: reg, kv: kv, det: det, ale: ale, model: model}
}

// Handler 返回带中间件的根路由。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("POST /api/v1/points", s.handlePoints)
	mux.HandleFunc("GET /api/v1/query", s.handleQuery)
	mux.HandleFunc("GET /api/v1/alerts", s.handleAlerts)
	mux.HandleFunc("POST /api/v1/diagnose", s.handleDiagnose)
	mux.HandleFunc("GET /api/v1/stats", s.handleStats)

	limiter := gateway.NewLimiter(s.cfg.RateLimit, s.cfg.RateBurst)
	return gateway.Chain(mux,
		gateway.RequestID(),
		gateway.Recover(s.log),
		gateway.AccessLog(s.log, s.reg),
		gateway.RateLimit(limiter, s.reg),
		gateway.Timeout(10*time.Second),
	)
}

// Observe 是 TCP/UDP 与 HTTP 共用的写入管道。
func (s *Server) Observe(metric string, value float64, at time.Time) {
	s.kv.Append(metric, value, at)
	s.reg.Inc("points_ingested_total", nil)
	s.reg.Set("series_active", float64(s.kv.Len()), nil)

	res := s.det.Observe(metric, value)
	if res.Anomaly {
		s.reg.Inc("anomalies_total", map[string]string{"metric": metric})
		for _, a := range s.ale.Evaluate(metric, value, res.Score, at) {
			s.recordAlert(a)
		}
	}
}

func (s *Server) recordAlert(a alert.Alert) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recent = append(s.recent, a)
	if len(s.recent) > 200 {
		s.recent = s.recent[len(s.recent)-200:]
	}
	s.reg.Inc("alerts_total", map[string]string{"rule": a.Rule})
}

func (s *Server) alertsSnapshot(limit int) []alert.Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.recent) {
		limit = len(s.recent)
	}
	out := make([]alert.Alert, limit)
	copy(out, s.recent[len(s.recent)-limit:])
	return out
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "instance": s.cfg.InstanceID})
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	failures, open := s.model.State(time.Now())
	writeJSON(w, http.StatusOK, map[string]any{
		"ready":            true,
		"llm_circuit_open": open,
		"llm_failures":     failures,
		"series":           s.kv.Len(),
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(s.reg.Render()))
}

type point struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
}

type pointsRequest struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Points []point `json:"points"`
}

func (s *Server) handlePoints(w http.ResponseWriter, r *http.Request) {
	var req pointsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}

	now := time.Now()
	count := 0
	if req.Metric != "" {
		s.Observe(req.Metric, req.Value, now)
		count++
	}
	for _, p := range req.Points {
		if p.Metric == "" {
			continue
		}
		s.Observe(p.Metric, p.Value, now)
		count++
	}
	if count == 0 {
		writeError(w, http.StatusBadRequest, "至少提供 metric/value 或 points")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": count})
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	metric := r.URL.Query().Get("metric")
	if metric == "" {
		writeError(w, http.StatusBadRequest, "缺少 metric 参数")
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}

	series := s.kv.Get(metric)
	if limit > 0 && len(series) > limit {
		series = series[len(series)-limit:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"metric": metric, "points": series, "count": len(series)})
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": s.alertsSnapshot(limit)})
}

type diagnoseRequest struct {
	Metric string `json:"metric"`
}

func (s *Server) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	var req diagnoseRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || req.Metric == "" {
		writeError(w, http.StatusBadRequest, "需要提供 metric")
		return
	}

	series := s.kv.Get(req.Metric)
	if len(series) < 2 {
		writeError(w, http.StatusNotFound, "该指标还没有足够的数据")
		return
	}

	mean, std := meanStd(series)
	start := time.Now()
	text, err := s.model.Diagnose(r.Context(), llm.Request{
		Metric: req.Metric,
		Series: series,
		Mean:   mean,
		Std:    std,
		Score:  score(series[len(series)-1], mean, std),
	})
	if errors.Is(err, llm.ErrCircuitOpen) {
		s.reg.Inc("llm_rejected_total", nil)
		writeError(w, http.StatusServiceUnavailable, "诊断服务暂时不可用（熔断中），请稍后重试")
		return
	}
	if err != nil {
		s.reg.Inc("llm_failed_total", nil)
		writeError(w, http.StatusBadGateway, "诊断失败: "+err.Error())
		return
	}
	s.reg.Inc("llm_calls_total", nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"metric":      req.Metric,
		"analysis":    text,
		"elapsed_ms":  time.Since(start).Milliseconds(),
		"points_used": len(series),
	})
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"series":      s.kv.Len(),
		"alerts":      len(s.alertsSnapshot(0)),
		"rate_limit":  s.cfg.RateLimit,
		"listen_http": s.cfg.HTTPAddr,
		"instance":    s.cfg.InstanceID,
	})
}

func meanStd(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))
	var sq float64
	for _, v := range values {
		d := v - mean
		sq += d * d
	}
	return mean, math.Sqrt(sq / float64(len(values)))
}

func score(v, mean, std float64) float64 {
	if std < 1e-9 {
		return 0
	}
	d := v - mean
	if d < 0 {
		d = -d
	}
	return d / std
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
