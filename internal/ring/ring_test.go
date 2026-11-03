package ring

import "testing"

func TestRingDistributionAndStability(t *testing.T) {
	r := New(100)
	r.Add("a:8080")
	r.Add("b:8080")
	r.Add("c:8080")

	first := r.Get("http_latency_ms")
	if first == "" {
		t.Fatal("应命中一个节点")
	}
	for i := 0; i < 10; i++ {
		if got := r.Get("http_latency_ms"); got != first {
			t.Fatalf("同一 key 应稳定命中，期望 %s，实际 %s", first, got)
		}
	}

	// 1000 个 key 应大致均匀分布在 3 个节点上
	counts := map[string]int{}
	for i := 0; i < 1000; i++ {
		counts[r.Get(string(rune('a'+i%26))+string(rune(i)))]++
	}
	if len(counts) != 3 {
		t.Fatalf("应命中 3 个节点，实际 %d", len(counts))
	}
	for node, n := range counts {
		if n < 200 || n > 470 {
			t.Fatalf("节点 %s 分布不均: %d", node, n)
		}
	}
}

func TestRingAddRemove(t *testing.T) {
	r := New(50)
	r.Add("a")
	r.Add("b")
	key := "qps"
	owner := r.Get(key)

	r.Add("c")
	if r.Get(key) != owner && r.Get(key) == "" {
		t.Fatal("加节点后应仍能命中")
	}

	r.Remove("a")
	r.Remove("b")
	r.Remove("c")
	if got := r.Get(key); got != "" {
		t.Fatalf("空环应返回空字符串，实际 %s", got)
	}
}
