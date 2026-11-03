// Command aiosight 启动 AIOps 故障诊断服务。
//
// 一条命令就能跑起来：
//
//	AIOSIGHT_DEMO=1 go run ./cmd/aiosight
//
// 它会监听 :8080（HTTP）、:9000（TCP 指标）、:9001（UDP 指标）。
package main

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wsy/aiosight/internal/alert"
	"github.com/wsy/aiosight/internal/anomaly"
	"github.com/wsy/aiosight/internal/api"
	"github.com/wsy/aiosight/internal/config"
	"github.com/wsy/aiosight/internal/ingest"
	"github.com/wsy/aiosight/internal/llm"
	"github.com/wsy/aiosight/internal/metrics"
	"github.com/wsy/aiosight/internal/store"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	reg := metrics.New()
	kv := store.New(16, 10*time.Minute)
	defer kv.Close()

	detector := anomaly.New(0.3, 3.0, 10)
	engine := alert.New([]alert.Rule{
		{Name: "zscore-anomaly", Threshold: 3.0, Cooldown: time.Minute},
	})
	model := llm.New(cfg.LLM)

	server := api.New(cfg, log, reg, kv, detector, engine, model)

	tcpIngest := ingest.NewTCP(cfg.TCPAddr, log, server.Observe)
	if err := tcpIngest.Start(); err != nil {
		log.Error("TCP 采集启动失败", "addr", cfg.TCPAddr, "error", err)
		os.Exit(1)
	}
	defer tcpIngest.Close()

	udpIngest := ingest.NewUDP(cfg.UDPAddr, log, server.Observe)
	if err := udpIngest.Start(); err != nil {
		log.Error("UDP 采集启动失败", "addr", cfg.UDPAddr, "error", err)
		os.Exit(1)
	}
	defer udpIngest.Close()

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("HTTP 服务异常退出", "error", err)
			os.Exit(1)
		}
	}()

	if os.Getenv("AIOSIGHT_DEMO") == "1" {
		go runDemo(server, log)
	}

	log.Info("aiosight 已启动",
		"http", cfg.HTTPAddr,
		"tcp", cfg.TCPAddr,
		"udp", cfg.UDPAddr,
		"demo", os.Getenv("AIOSIGHT_DEMO") == "1",
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Info("收到退出信号，开始优雅关闭")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP 关闭失败", "error", err)
	}
	log.Info("已退出")
}

// runDemo 生成带周期性尖峰的合成流量，方便本地演示异常检测与诊断效果。
func runDemo(srv *api.Server, log *slog.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	start := time.Now()
	for now := range ticker.C {
		t := now.Sub(start).Seconds()
		base := 120 + 25*math.Sin(t/15)
		latency := base + rand.Float64()*8
		// 每 40 秒制造一次毛刺，触发 Z-Score 告警。
		if int(t)%40 == 35 {
			latency += 260
		}
		srv.Observe("http_latency_ms", math.Round(latency*100)/100, now)
		srv.Observe("qps", math.Round((300+40*math.Sin(t/10))*100)/100, now)
	}
	log.Info("演示数据生成器已停止")
}
