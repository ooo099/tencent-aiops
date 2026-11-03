# aiosight

一个面向「AI + 后端」岗位的演示项目：**轻量 AIOps 故障诊断服务**。

用 Go 标准库实现指标采集（TCP/UDP）、在线异常检测（EWMA + Z-Score）、
告警去重、限流/熔断/重试，并在检测到异常时调用大模型生成根因分析。
**零第三方依赖**，单二进制运行。

## 架构

```
          TCP :9000                        ┌─────────────────────────────┐
  业务埋点 ─────────┐                      │            HTTP :8080       │
          UDP :9001 ┴──► ingest ──► Observe ┤ /api/v1/points  写入指标      │
                                          │ /api/v1/query   查询序列      │
                                          │ /api/v1/alerts  最近告警      │
                                          │ /api/v1/diagnose 触发 AI 诊断 │
                                          │ /metrics        Prometheus   │
                                          │ /healthz /readyz             │
                                          └───────────┬─────────────────┘
                                                      │
                        ┌─────────────────────────────┼──────────────────────┐
                        ▼                             ▼                      ▼
                 store.KV (分片+TTL)          anomaly.Detector        alert.Engine
                 保存最近 1024 个点            EWMA + Z-Score          规则 + 冷却去重
                                                      │
                                                      ▼
                                                 llm.Client
                                       超时 / 重试 / 熔断 / 无密钥兜底
```

## 快速开始

```bash
# 本地运行（自带演示数据，会自动制造毛刺触发告警）
AIOSIGHT_DEMO=1 go run ./cmd/aiosight

# 另开一个终端
curl -s localhost:8080/healthz
curl -s -X POST localhost:8080/api/v1/points -d '{"metric":"cpu_usage","value":0.42}'
curl -s "localhost:8080/api/v1/query?metric=cpu_usage"
curl -s -X POST localhost:8080/api/v1/diagnose -d '{"metric":"http_latency_ms"}'
```

容器化（服务 + Prometheus + Grafana）：

```bash
cd deploy && docker compose up --build
# Grafana: http://localhost:3000 (admin/admin)  Prometheus: http://localhost:9090
```

## 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/points` | 写入单个或批量指标点 |
| GET | `/api/v1/query?metric=&limit=` | 查询某指标最近的点 |
| GET | `/api/v1/alerts?limit=` | 查询最近告警 |
| POST | `/api/v1/diagnose` | 对指定指标做根因分析 |
| GET | `/metrics` | Prometheus 文本指标 |
| GET | `/healthz` `/readyz` | 存活 / 就绪探针 |

TCP 采集协议（每行一条）：

```
cpu_usage 0.42
http_latency_ms 380
```

## 设计取舍

| 决策 | 原因 |
|---|---|
| 不引入 Prometheus 客户端库 | 只需要计数器与瞬时值，自研 80 行足够，顺便控制二进制体积 |
| EWMA 而不是固定阈值 | 业务指标有周期性和基线漂移，固定阈值误报率高 |
| 先算分再更新基线 | 异常点不会立刻把基线带偏，避免"一次毛刺污染后续判断" |
| 告警带冷却窗口 | 同一指标持续异常时避免刷屏，工程上比"每次都发"更实用 |
| 熔断器阈值 5 次 / 30 秒 | 大模型调用是外部依赖，连续失败时应快速失败而不是拖垮请求 |
| 无密钥时本地兜底 | 保证离线也能完整演示，同时方便写确定性测试 |


## 压测数据

环境：Apple M 系列（10 核）/ macOS 26.6，服务端开启结构化日志与 Prometheus 指标采集，压测客户端与服务端同机。
每次请求写一条指标点（含 JSON 解析、KV 写入、异常检测、指标累加、访问日志）。

| 并发 | 请求数 | QPS | P50 | P90 | P99 | 错误 |
|---|---|---|---|---|---|---|
| 64 | 456,489 | 91,298 | 0.45 ms | 1.45 ms | 2.63 ms | 0 |
| 128 | 534,067 | 106,813 | 1.18 ms | 2.36 ms | 3.82 ms | 0 |

复现方式：

```bash
# 服务端（关闭限流以免影响测量）
AIOSIGHT_RATE_LIMIT=1000000 AIOSIGHT_RATE_BURST=1000000 go run ./cmd/aiosight

# 压测端
go run ./cmd/loadgen -workers 64 -duration 5s
```

## 目录

```
cmd/aiosight        程序入口，装配依赖与优雅退出
internal/config     环境变量配置
internal/ingest     TCP / UDP 指标采集
internal/store      分片 + TTL 的内存 KV
internal/anomaly    EWMA + Z-Score 检测器
internal/alert      规则引擎与冷却去重
internal/llm        模型客户端（超时/重试/熔断/兜底）
internal/gateway    请求 ID、日志、恢复、限流、超时
internal/api        HTTP 路由与处理器
internal/metrics    指标注册表与 Prometheus 渲染
deploy              Dockerfile / compose / Prometheus 配置
```

## 测试

```bash
go test ./... -race
```

覆盖异常检测的预热与误报、KV 过期与副本语义、限流令牌桶、
模型重试与熔断、接口正常/异常路径。

## 对应岗位要求

| JD 要求 | 本项目 |
|---|---|
| 熟练掌握 Go | 纯标准库，goroutine/context/sync 全用上 |
| TCP/UDP、进程间通信 | TCP 行协议采集 + UDP 上报 + 优雅关闭 |
| 分布式/负载均衡/容灾 | 限流、熔断、重试、超时、健康检查（单机 9 万 QPS、P99 2.6ms） |
| 数据库 / KV 原理 | 分片哈希、TTL 清理、读写锁、副本返回 |
| AI 智能运维 / 故障预测 | 在线异常检测 + 大模型根因分析 |
| 云原生 | 多阶段构建、docker compose、Prometheus/Grafana |
| AI 编程工具 / 人机协同 | 需求拆解 + 代码评审 + 测试驱动 |
