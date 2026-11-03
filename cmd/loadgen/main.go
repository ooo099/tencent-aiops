// Command loadgen 是一个简单的压测工具：
// 并发写入指标点，统计 QPS 与 P50/P90/P99 延迟。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:18080/api/v1/points", "写入接口地址")
	workers := flag.Int("workers", 64, "并发数")
	duration := flag.Duration("duration", 5*time.Second, "压测时长")
	metric := flag.String("metric", "load_test_metric", "指标名")
	flag.Parse()

	var (
		mu        sync.Mutex
		latencies []time.Duration
		failed    int
		sent      int
	)

	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 256,
			MaxConnsPerHost:     256,
		},
		Timeout: 5 * time.Second,
	}

	body := fmt.Sprintf(`{"metric":%q,"value":1}`, *metric)
	deadline := time.Now().Add(*duration)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]time.Duration, 0, 1024)
			for time.Now().Before(deadline) {
				start := time.Now()
				req, _ := http.NewRequest(http.MethodPost, *url, bytes.NewBufferString(body))
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				elapsed := time.Since(start)
				if err != nil {
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				resp.Body.Close()
				local = append(local, elapsed)
				mu.Lock()
				sent++
				mu.Unlock()
			}
			mu.Lock()
			latencies = append(latencies, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	total := len(latencies)
	if total == 0 {
		fmt.Println("没有成功的请求")
		return
	}
	p := func(q float64) time.Duration {
		idx := int(float64(total-1) * q)
		return latencies[idx]
	}
	fmt.Printf("并发=%d 时长=%s 成功=%d 失败=%d\n", *workers, *duration, sent, failed)
	fmt.Printf("QPS=%.0f\n", float64(total)/duration.Seconds())
	fmt.Printf("P50=%s P90=%s P99=%s\n", p(0.50), p(0.90), p(0.99))
}
