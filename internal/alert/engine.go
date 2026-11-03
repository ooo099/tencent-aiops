// Package alert 负责把异常检测结果转成告警：
// 规则匹配 + 冷却去重，避免同一问题刷屏。
package alert

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Rule 是一条告警规则。
type Rule struct {
	Name      string        // 规则名
	Prefix    string        // 只匹配该前缀的指标，空表示全部
	Threshold float64       // 异常分数阈值
	Cooldown  time.Duration // 同一指标的冷却时间
}

// Alert 是一条告警。
type Alert struct {
	Rule    string    `json:"rule"`
	Metric  string    `json:"metric"`
	Value   float64   `json:"value"`
	Score   float64   `json:"score"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// Engine 持有规则和冷却状态。
type Engine struct {
	rules []Rule

	mu        sync.Mutex
	lastFired map[string]time.Time
}

// New 创建告警引擎。
func New(rules []Rule) *Engine {
	return &Engine{rules: rules, lastFired: make(map[string]time.Time)}
}

// Evaluate 返回命中的告警；未命中或处于冷却期返回 nil。
func (e *Engine) Evaluate(metric string, value, score float64, now time.Time) []Alert {
	var out []Alert
	for _, rule := range e.rules {
		if rule.Prefix != "" && !strings.HasPrefix(metric, rule.Prefix) {
			continue
		}
		if score < rule.Threshold && rule.Threshold > 0 {
			continue
		}

		key := rule.Name + "|" + metric
		e.mu.Lock()
		last, seen := e.lastFired[key]
		if seen && now.Sub(last) < rule.Cooldown {
			e.mu.Unlock()
			continue
		}
		e.lastFired[key] = now
		e.mu.Unlock()

		out = append(out, Alert{
			Rule:    rule.Name,
			Metric:  metric,
			Value:   value,
			Score:   score,
			Message: fmt.Sprintf("指标 %s 异常，当前值 %.2f，偏离基线 %.2f 个标准差", metric, value, score),
			At:      now,
		})
	}
	return out
}
