package anomaly

import "testing"

func TestDetectorWarmupThenAnomaly(t *testing.T) {
	d := New(0.3, 3.0, 5)

	// 预热：在 100 附近小幅波动
	for i := 0; i < 30; i++ {
		v := 100.0
		if i%2 == 0 {
			v = 102
		}
		res := d.Observe("http_latency_ms", v)
		if res.Anomaly {
			t.Fatalf("预热阶段不应告警，第 %d 次就误报了", i)
		}
	}

	// 突增到 300，应判定为异常
	res := d.Observe("http_latency_ms", 300)
	if !res.Anomaly {
		t.Fatalf("突增应触发异常，实际 score=%.2f", res.Score)
	}
	if res.Score < 3 {
		t.Fatalf("异常分数应大于阈值，实际 %.2f", res.Score)
	}
}

func TestDetectorSeparatesSeries(t *testing.T) {
	d := New(0.5, 3.0, 3)

	for i := 0; i < 10; i++ {
		d.Observe("a", 10)
		d.Observe("b", 1000)
	}

	res := d.Observe("a", 10.5)
	if res.Anomaly {
		t.Fatal("不同序列的基线不应互相污染")
	}
}
