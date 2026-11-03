package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wsy/aiosight/internal/alert"
	"github.com/wsy/aiosight/internal/anomaly"
	"github.com/wsy/aiosight/internal/config"
	"github.com/wsy/aiosight/internal/llm"
	"github.com/wsy/aiosight/internal/metrics"
	"github.com/wsy/aiosight/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()

	cfg := config.Config{RateLimit: 10000, RateBurst: 10000}
	reg := metrics.New()
	kv := store.New(4, time.Minute)
	det := anomaly.New(0.3, 3, 5)
	ale := alert.New([]alert.Rule{{Name: "zscore", Threshold: 3, Cooldown: time.Minute}})
	model := llm.New(config.LLMConfig{})
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	s := New(cfg, log, reg, kv, det, ale, model)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		kv.Close()
	})
	return ts, s
}

func TestIngestAndQuery(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Post(ts.URL+"/api/v1/points", "application/json", strings.NewReader(`{"metric":"qps","value":42}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("写入应返回 202，实际 %d", resp.StatusCode)
	}

	resp2, err := http.Get(ts.URL + "/api/v1/query?metric=qps")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	var body struct {
		Points []float64 `json:"points"`
		Count  int       `json:"count"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 1 || len(body.Points) != 1 || body.Points[0] != 42 {
		t.Fatalf("查询结果不符: %+v", body)
	}
}

func TestRejectsBadRequest(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Post(ts.URL+"/api/v1/points", "application/json", strings.NewReader(`not-json`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法请求应返回 400，实际 %d", resp.StatusCode)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	s.Observe("qps", 1, time.Now())

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "points_ingested_total 1") {
		t.Fatalf("指标输出缺少计数:\n%s", raw)
	}
}

func TestDiagnoseWithoutEnoughData(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, err := http.Post(ts.URL+"/api/v1/diagnose", "application/json", strings.NewReader(`{"metric":"unknown"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("数据不足应返回 404，实际 %d", resp.StatusCode)
	}
}

func TestDiagnoseUsesFallbackAnalysis(t *testing.T) {
	ts, s := newTestServer(t)
	for i := 0; i < 20; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 102
		}
		s.Observe("latency", v, time.Now())
	}
	s.Observe("latency", 400, time.Now())

	resp, err := http.Post(ts.URL+"/api/v1/diagnose", "application/json", strings.NewReader(`{"metric":"latency"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("诊断应返回 200，实际 %d", resp.StatusCode)
	}

	var body struct {
		Analysis string `json:"analysis"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Analysis, "本地诊断") {
		t.Fatalf("未使用兜底分析: %s", body.Analysis)
	}
}

func TestHealthEndpoints(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/stats"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s 应返回 200，实际 %d", path, resp.StatusCode)
		}
	}
}
