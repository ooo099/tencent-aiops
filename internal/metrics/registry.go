// Package metrics 是一个极简的指标注册表：
// 只依赖标准库，直接把指标渲染成 Prometheus 文本格式，
// 避免为了暴露 /metrics 引入重量级依赖。
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry 并发安全地保存计数器与瞬时值。
type Registry struct {
	mu       sync.RWMutex
	counters map[string]float64
	gauges   map[string]float64
}

// New 创建一个空的注册表。
func New() *Registry {
	return &Registry{
		counters: make(map[string]float64),
		gauges:   make(map[string]float64),
	}
}

// Inc 让计数器加一。
func (r *Registry) Inc(name string, labels map[string]string) {
	r.Add(name, 1, labels)
}

// Add 让计数器增加 v。
func (r *Registry) Add(name string, v float64, labels map[string]string) {
	key := formatKey(name, labels)
	r.mu.Lock()
	r.counters[key] += v
	r.mu.Unlock()
}

// Set 设置瞬时值（例如当前队列长度）。
func (r *Registry) Set(name string, v float64, labels map[string]string) {
	key := formatKey(name, labels)
	r.mu.Lock()
	r.gauges[key] = v
	r.mu.Unlock()
}

// Value 读取计数器当前值，主要给测试使用。
func (r *Registry) Value(name string, labels map[string]string) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.counters[formatKey(name, labels)]
}

// Render 输出 Prometheus 文本格式。
func (r *Registry) Render() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var b strings.Builder
	for _, k := range sortedKeys(r.counters) {
		fmt.Fprintf(&b, "%s %g\n", k, r.counters[k])
	}
	for _, k := range sortedKeys(r.gauges) {
		fmt.Fprintf(&b, "%s %g\n", k, r.gauges[k])
	}
	return b.String()
}

func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func formatKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, k, escape(labels[k])))
	}
	return name + "{" + strings.Join(parts, ",") + "}"
}

func escape(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return strings.ReplaceAll(v, "\n", `\n`)
}
