# API 文档

Base URL：`http://localhost:8081`（经 router）或 `http://localhost:8080`（直连单实例）。

所有接口返回 JSON；出错时返回 `{"error": "..."}`。

## 健康检查

```bash
curl -s localhost:8081/healthz
```

```json
{"status":"ok","instance":"aiosight-0"}
```

## 写入指标

单个：

```bash
curl -s -X POST localhost:8081/api/v1/points \
  -H 'Content-Type: application/json' \
  -d '{"metric":"http_latency_ms","value":128.5}'
```

```json
{"accepted":1}
```

批量：

```bash
curl -s -X POST localhost:8081/api/v1/points \
  -H 'Content-Type: application/json' \
  -d '{"points":[{"metric":"qps","value":320},{"metric":"cpu_usage","value":0.42}]}'
```

```json
{"accepted":2}
```

## 查询序列

```bash
curl -s "localhost:8081/api/v1/query?metric=http_latency_ms&limit=5"
```

```json
{"metric":"http_latency_ms","points":[120.1,118.7,131.2,127.9,402.6],"count":5}
```

## 查看告警

```bash
curl -s "localhost:8081/api/v1/alerts?limit=10"
```

```json
{"alerts":[{"rule":"zscore-anomaly","metric":"http_latency_ms","value":402.6,"score":4.31,"message":"指标 http_latency_ms 异常，当前值 402.60，偏离基线 4.31 个标准差","at":"2026-11-03T11:20:31+08:00"}]}
```

## AI 根因分析

```bash
curl -s -X POST localhost:8081/api/v1/diagnose \
  -H 'Content-Type: application/json' \
  -d '{"metric":"http_latency_ms"}'
```

**没有配置大模型时**（本地兜底，可直接演示）：

```json
{
  "metric": "http_latency_ms",
  "analysis": "【本地诊断】指标 http_latency_ms 当前偏离基线 4.31 个标准差，趋势持续上升，偏离幅度约 218.4%，判定为严重异常。\n可能原因：1) 下游依赖响应变慢导致请求堆积；2) 流量突增超过容量水位；3) 资源泄漏（连接/内存）逐步累积。\n建议处置：先确认同时段 QPS 与错误率是否同步上升；再检查依赖服务 P99 与连接池占用；必要时对该接口限流并回滚最近变更。",
  "elapsed_ms": 0,
  "points_used": 21
}
```

**配置了大模型时**，`analysis` 字段就是模型生成的中文根因分析（见 [AI.md](./AI.md)）。

熔断中会返回 503：

```json
{"error":"诊断服务暂时不可用（熔断中），请稍后重试"}
```

## 指标与就绪

```bash
curl -s localhost:8081/metrics | head
```

```
router_requests_total 12
router_routed_total{mode="hash"} 11
router_routed_total{mode="round_robin"} 1
```

`/readyz` 额外返回大模型熔断状态：

```json
{"ready":true,"llm_circuit_open":false,"llm_failures":0,"series":3}
```

## 状态码

| 码 | 含义 |
|---|---|
| 200 / 202 | 成功 / 已接收 |
| 400 | 参数缺失或 JSON 非法 |
| 404 | 指标没有足够数据 |
| 429 | 触发限流 |
| 502 | 上游分片不可用 |
| 503 | 大模型熔断中 |
