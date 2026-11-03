package ingest

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		line    string
		name    string
		value   float64
		wantErr bool
	}{
		{"cpu_usage 0.42", "cpu_usage", 0.42, false},
		{"  http_latency_ms   380  ", "http_latency_ms", 380, false},
		{"qps 1e3", "qps", 1000, false},
		{"metric_only", "", 0, true},
		{"metric abc", "", 0, true},
		{"", "", 0, true},
	}

	for _, tt := range tests {
		name, value, err := parse(tt.line)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("输入 %q 应报错", tt.line)
			}
			continue
		}
		if err != nil {
			t.Fatalf("输入 %q 解析失败: %v", tt.line, err)
		}
		if name != tt.name || value != tt.value {
			t.Fatalf("输入 %q 解析为 (%s, %v)，期望 (%s, %v)", tt.line, name, value, tt.name, tt.value)
		}
	}
}
