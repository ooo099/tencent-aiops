// Package ring 实现一致性哈希环，用于把同一个指标稳定路由到同一台实例。
// 多实例部署时，指标按名字分片，查询也走同一个分片，避免数据不一致。
package ring

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"sync"
)

// Ring 是一致性哈希环，带回环虚拟节点以均衡负载。
type Ring struct {
	mu       sync.RWMutex
	replicas int
	nodes    []string
	keys     []uint32
	hashMap  map[uint32]string
}

// New 创建哈希环。
func New(replicas int) *Ring {
	if replicas <= 0 {
		replicas = 100
	}
	return &Ring{replicas: replicas, hashMap: make(map[uint32]string)}
}

// Add 加入节点，并生成虚拟节点。
func (r *Ring) Add(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, n := range r.nodes {
		if n == node {
			return
		}
	}
	r.nodes = append(r.nodes, node)

	for i := 0; i < r.replicas; i++ {
		r.hashMap[hashKey(fmt.Sprintf("%s#%d", node, i))] = node
	}
	r.rebuild()
}

// Remove 移除节点。
func (r *Ring) Remove(node string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	kept := r.nodes[:0]
	for _, n := range r.nodes {
		if n != node {
			kept = append(kept, n)
		}
	}
	r.nodes = kept

	for i := 0; i < r.replicas; i++ {
		delete(r.hashMap, hashKey(fmt.Sprintf("%s#%d", node, i)))
	}
	r.rebuild()
}

// Replace 用新节点列表替换整个环，适合从配置或服务发现刷新。
func (r *Ring) Replace(nodes []string) {
	r.mu.Lock()
	r.nodes = nil
	r.hashMap = make(map[uint32]string)
	r.mu.Unlock()

	for _, n := range nodes {
		r.Add(n)
	}
}

// Get 返回 key 命中的节点；环为空返回空字符串。
func (r *Ring) Get(key string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.keys) == 0 {
		return ""
	}
	h := hashKey(key)
	idx := sort.Search(len(r.keys), func(i int) bool { return r.keys[i] >= h })
	if idx == len(r.keys) {
		idx = 0
	}
	return r.hashMap[r.keys[idx]]
}

// Nodes 返回当前节点快照。
func (r *Ring) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.nodes))
	copy(out, r.nodes)
	return out
}

// rebuild 重建排序后的 key 列表，调用方必须已持锁。
func (r *Ring) rebuild() {
	r.keys = r.keys[:0]
	for k := range r.hashMap {
		r.keys = append(r.keys, k)
	}
	sort.Slice(r.keys, func(i, j int) bool { return r.keys[i] < r.keys[j] })
}

// hashKey 用 SHA-256 取前 4 字节。
// FNV 对 "a0/b1/c2" 这类低熵短串的雪崩性不足，会导致分片严重倾斜。
func hashKey(s string) uint32 {
	sum := sha256.Sum256([]byte(s))
	return uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])
}
