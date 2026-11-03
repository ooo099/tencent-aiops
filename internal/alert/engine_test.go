package alert

import (
	"testing"
	"time"
)

func TestEvaluateMatchesRule(t *testing.T) {
	e := New([]Rule{{Name: "zscore", Prefix: "http_", Threshold: 3, Cooldown: time.Minute}})
	now := time.Now()

	got := e.Evaluate("http_latency_ms", 520, 4.2, now)
	if len(got) != 1 {
		t.Fatalf("应命中一条告警，实际 %d", len(got))
	}
	if got[0].Rule != "zscore" || got[0].Metric != "http_latency_ms" {
		t.Fatalf("告警字段不符: %+v", got[0])
	}
	if got[0].Message == "" || got[0].At.IsZero() {
		t.Fatal("告警应带描述与时间")
	}
}

func TestEvaluateCooldown(t *testing.T) {
	e := New([]Rule{{Name: "zscore", Threshold: 3, Cooldown: time.Minute}})
	now := time.Now()

	if len(e.Evaluate("latency", 1, 5, now)) != 1 {
		t.Fatal("首次应告警")
	}
	if len(e.Evaluate("latency", 1, 5, now.Add(10*time.Second))) != 0 {
		t.Fatal("冷却期内不应重复告警")
	}
	if len(e.Evaluate("latency", 1, 5, now.Add(61*time.Second))) != 1 {
		t.Fatal("冷却期过后应再次告警")
	}
}

func TestEvaluateFilters(t *testing.T) {
	e := New([]Rule{
		{Name: "http", Prefix: "http_", Threshold: 3, Cooldown: time.Minute},
		{Name: "global", Threshold: 10, Cooldown: 0},
	})
	now := time.Now()

	if got := e.Evaluate("db_qps", 1, 5, now); len(got) != 0 {
		t.Fatalf("前缀不匹配且未达全局阈值时不应告警，实际 %d 条", len(got))
	}
	if len(e.Evaluate("http_qps", 1, 2, now)) != 0 {
		t.Fatal("低于阈值不应告警")
	}
	if len(e.Evaluate("http_qps", 1, 12, now)) != 2 {
		t.Fatal("同时命中前缀规则与全局规则时应返回 2 条")
	}
}
