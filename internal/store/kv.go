// Package store 实现一个分片 + TTL 的内存 KV 存储，
// 用来保存最近一段时间的时间序列点，演示 NoSQL KV 的常见设计。
package store

import (
	"hash/fnv"
	"sync"
	"time"
)

// entry 是一条带过期时间的记录。
type entry struct {
	value    []float64
	expireAt time.Time
}

// shard 是独立加锁的分片，降低锁竞争。
type shard struct {
	mu sync.RWMutex
	m  map[string]*entry
}

// KV 是分片后的键值存储。
type KV struct {
	shards []*shard
	ttl    time.Duration
	stop   chan struct{}
	once   sync.Once
}

// New 创建 KV，并启动后台清理协程。
func New(shards int, ttl time.Duration) *KV {
	if shards <= 0 {
		shards = 16
	}
	kv := &KV{
		shards: make([]*shard, shards),
		ttl:    ttl,
		stop:   make(chan struct{}),
	}
	for i := range kv.shards {
		kv.shards[i] = &shard{m: make(map[string]*entry)}
	}
	go kv.janitor()
	return kv
}

func (kv *KV) shardFor(key string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return kv.shards[int(h.Sum32())%len(kv.shards)]
}

// Append 向某个序列追加一个点，并刷新过期时间。
func (kv *KV) Append(key string, v float64, now time.Time) {
	s := kv.shardFor(key)
	s.mu.Lock()
	e, ok := s.m[key]
	if !ok {
		e = &entry{}
		s.m[key] = e
	}
	e.value = append(e.value, v)
	if len(e.value) > 1024 {
		e.value = e.value[len(e.value)-1024:]
	}
	e.expireAt = now.Add(kv.ttl)
	s.mu.Unlock()
}

// Get 返回某个序列的副本。
func (kv *KV) Get(key string) []float64 {
	s := kv.shardFor(key)
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m[key]
	if !ok {
		return nil
	}
	out := make([]float64, len(e.value))
	copy(out, e.value)
	return out
}

// Len 返回当前存活的序列数量。
func (kv *KV) Len() int {
	n := 0
	for _, s := range kv.shards {
		s.mu.RLock()
		n += len(s.m)
		s.mu.RUnlock()
	}
	return n
}

// Close 停止后台清理。
func (kv *KV) Close() {
	kv.once.Do(func() { close(kv.stop) })
}

func (kv *KV) janitor() {
	ticker := time.NewTicker(kv.ttl / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			for _, s := range kv.shards {
				s.mu.Lock()
				for k, e := range s.m {
					if now.After(e.expireAt) {
						delete(s.m, k)
					}
				}
				s.mu.Unlock()
			}
		case <-kv.stop:
			return
		}
	}
}
