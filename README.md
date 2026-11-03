# aiosight

面向「AI + 后端」方向的实践项目：**轻量 AIOps 故障诊断服务**。

用 Go 标准库实现指标采集（TCP/UDP）、在线异常检测（EWMA + Z-Score）、告警去重、
限流/熔断/重试，并在检测到异常时调用大模型生成中文根因分析。
支持**多实例分片部署**（一致性哈希路由）、Docker / Kubernetes、GitHub Actions CI 与镜像发布。

[![CI](https://github.com/ooo099/tencent-aiops/actions/workflows/ci.yml/badge.svg)](https://github.com/ooo099/tencent-aiops/actions/workflows/ci.yml)

## 架构

```
                       ┌──────────────────────────────┐
  客户端 / 采集器 ───▶ │  router（无状态，可水平扩容） │
                       │  一致性哈希：metric → 实例    │
                       └───────┬──────────┬───────────┘
                               │          │
                 ┌─────────────▼──┐  ┌────▼───────────┐
                 │ aiosight-0     │  │ aiosight-1     │  … aiosight-2
                 │ KV + 检测器     │  │ KV + 检测器     │
                 └───────┬────────┘  └───────┬────────┘
                         └────────┬──────────┘
                                  ▼
                     Prometheus 抓取每个 Pod 的 /metrics → Grafana
```

完整设计见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 快速开始

```bash
# 单实例 + 演示数据（每 40 秒制造一次毛刺，触发告警）
AIOSIGHT_DEMO=1 go run ./cmd/aiosight

curl -s localhost:8080/healthz
curl -s -X POST localhost:8080/api/v1/points -d '{"metric":"cpu_usage","value":0.42}'
curl -s -X POST localhost:8080/api/v1/diagnose -d '{"metric":"http_latency_ms"}'
```

多实例（3 分片 + 路由 + Prometheus + Grafana）：

```bash
cd deploy && docker compose up --build
curl -s localhost:8081/healthz
```

部署到 Kubernetes（kind / minikube）：

```bash
kubectl apply -f deploy/k8s/all.yaml
curl -s localhost:30081/healthz
```

详见 [docs/DEPLOY.md](docs/DEPLOY.md)。

## 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/points` | 写入单个或批量指标点 |
| GET | `/api/v1/query?metric=&limit=` | 查询某指标最近的点 |
| GET | `/api/v1/alerts?limit=` | 查询最近告警 |
| POST | `/api/v1/diagnose` | 对指定指标做 AI 根因分析 |
| GET | `/metrics` | Prometheus 文本指标 |
| GET | `/healthz` `/readyz` | 存活 / 就绪探针 |

示例与返回结构见 [docs/API.md](docs/API.md)；大模型接入与验证见 [docs/AI.md](docs/AI.md)。

TCP 采集协议（每行一条）：

```
cpu_usage 0.42
http_latency_ms 380
```

## 压测数据

环境：Apple M 系列（10 核）/ macOS 26.6，服务端开启结构化日志与 Prometheus 指标采集，
压测客户端与服务端同机。每次请求写一条指标点（JSON 解析 + KV 写入 + 异常检测 + 指标累加 + 访问日志）。

| 并发 | 请求数 | QPS | P50 | P90 | P99 | 错误 |
|---|---|---|---|---|---|---|
| 64 | 456,489 | 91,298 | 0.45 ms | 1.45 ms | 2.63 ms | 0 |
| 128 | 534,067 | 106,813 | 1.18 ms | 2.36 ms | 3.82 ms | 0 |

复现：

```bash
AIOSIGHT_RATE_LIMIT=1000000 AIOSIGHT_RATE_BURST=1000000 go run ./cmd/aiosight
go run ./cmd/loadgen -workers 64 -duration 5s
```

## 设计取舍

| 决策 | 原因 |
|---|---|
| 不引入 Prometheus 客户端库 | 只需要计数器与瞬时值，自研 80 行足够，顺便控制二进制体积 |
| EWMA 而不是固定阈值 | 业务指标有周期性和基线漂移，固定阈值误报率高 |
| 先算分再更新基线 | 异常点不会立刻把基线带偏，避免"一次毛刺污染后续判断" |
| 告警带冷却窗口 | 同一指标持续异常时避免刷屏 |
| 熔断器 5 次 / 30 秒 | 大模型是外部依赖，连续失败应快速失败 |
| 无密钥时本地兜底 | 保证离线可演示，同时方便写确定性测试 |
| 分片而不是共享存储 | 状态是"最近窗口的热数据"，按 key 切分更简单；共享存储会多一个故障点 |
| 只对 router 做 HPA | 分片实例有状态，扩缩容需配合数据迁移（已知限制） |

## 目录

```
cmd/aiosight        服务入口：装配依赖、启动 TCP/UDP/HTTP、优雅退出
cmd/router          分片网关：一致性哈希路由
cmd/loadgen         压测工具：QPS / P50 / P90 / P99
internal/config     环境变量配置
internal/ingest     TCP / UDP 指标采集
internal/store      分片 + TTL 的内存 KV
internal/anomaly    EWMA + Z-Score 检测器
internal/alert      规则引擎与冷却去重
internal/llm        模型客户端（超时/重试/熔断/兜底）
internal/gateway    请求 ID、日志、恢复、限流、超时
internal/ring       一致性哈希环
internal/api        HTTP 路由与处理器
internal/metrics    指标注册表与 Prometheus 渲染
deploy              Dockerfile / compose / Prometheus / K8s 清单
docs                架构、API、AI 接入、部署文档
tests               e2e 冒烟测试
```

## 测试

```bash
go test ./... -race          # 单测 + 数据竞争检测
./tests/e2e.sh               # 端到端（需先启动服务）
```

## 对应岗位要求

| JD 要求 | 本项目 |
|---|---|
| 熟练掌握 Go | 标准库为主，goroutine/context/sync 全用上 |
| TCP/UDP、进程间通信 | TCP 行协议采集 + UDP 上报 + 优雅关闭 |
| 分布式设计 | 一致性哈希分片、多实例路由、StatefulSet 稳定寻址 |
| 负载均衡 / 容灾 | 分片网关、限流、熔断、重试、超时、健康检查、PDB |
| 数据库 / KV 原理 | 分片哈希、TTL 清理、读写锁、副本返回 |
| AI 智能运维 / 故障预测 | 在线异常检测 + 大模型根因分析（含本地兜底） |
| 云原生 | 多阶段构建、Compose、K8s（HPA/PDB/探针）、Prometheus/Grafana、GitHub Actions |
