// Package config 从环境变量加载配置，便于容器化部署。
package config

import (
	"os"
	"strconv"
	"time"
)

// Config 是服务的全部可配置项。
type Config struct {
	HTTPAddr     string        // HTTP 监听地址
	TCPAddr      string        // TCP 指标采集地址
	UDPAddr      string        // UDP 指标上报地址
	ShutdownWait time.Duration // 优雅退出等待时间
	RateLimit    float64       // 每个 IP 每秒允许的请求数
	RateBurst    int           // 允许的突发量

	LLM LLMConfig
}

// LLMConfig 是故障诊断所用大模型的配置。
type LLMConfig struct {
	Endpoint  string        // OpenAI 兼容的 /chat/completions 地址
	APIKey    string        // 为空时使用本地启发式诊断，保证离线可跑
	Model     string        // 模型名
	Timeout   time.Duration // 单次调用超时
	MaxTokens int           // 输出上限
}

// Load 读取环境变量并填充默认值。
func Load() Config {
	return Config{
		HTTPAddr:     env("AIOSIGHT_HTTP_ADDR", ":8080"),
		TCPAddr:      env("AIOSIGHT_TCP_ADDR", ":9000"),
		UDPAddr:      env("AIOSIGHT_UDP_ADDR", ":9001"),
		ShutdownWait: envDuration("AIOSIGHT_SHUTDOWN_WAIT", 10*time.Second),
		RateLimit:    envFloat("AIOSIGHT_RATE_LIMIT", 200),
		RateBurst:    envInt("AIOSIGHT_RATE_BURST", 400),
		LLM: LLMConfig{
			Endpoint:  env("AIOSIGHT_LLM_ENDPOINT", ""),
			APIKey:    env("AIOSIGHT_LLM_API_KEY", ""),
			Model:     env("AIOSIGHT_LLM_MODEL", "glm-4-flash"),
			Timeout:   envDuration("AIOSIGHT_LLM_TIMEOUT", 8*time.Second),
			MaxTokens: envInt("AIOSIGHT_LLM_MAX_TOKENS", 512),
		},
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
