package store

import (
	"testing"
	"time"
)

func TestAppendAndGet(t *testing.T) {
	kv := New(4, time.Minute)
	defer kv.Close()

	now := time.Now()
	kv.Append("qps", 1, now)
	kv.Append("qps", 2, now)

	got := kv.Get("qps")
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("读取结果不符: %v", got)
	}
	if kv.Len() != 1 {
		t.Fatalf("序列数应为 1，实际 %d", kv.Len())
	}
}

func TestExpire(t *testing.T) {
	kv := New(4, 40*time.Millisecond)
	defer kv.Close()

	kv.Append("tmp", 1, time.Now())
	if kv.Len() != 1 {
		t.Fatal("写入后应存在")
	}

	time.Sleep(120 * time.Millisecond)
	if kv.Len() != 0 {
		t.Fatalf("过期后应被清理，实际仍有 %d 条", kv.Len())
	}
}

func TestGetReturnsCopy(t *testing.T) {
	kv := New(2, time.Minute)
	defer kv.Close()

	kv.Append("m", 1, time.Now())
	got := kv.Get("m")
	got[0] = 999

	if kv.Get("m")[0] != 1 {
		t.Fatal("Get 应返回副本，外部修改不应影响存储")
	}
}
