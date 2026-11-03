# AI 根因分析：怎么接、怎么验证

## 一、它是怎么工作的

```
指标点 → EWMA/Z-Score 判定异常 → 告警引擎冷却去重
                                      │
                                      ▼
                         /api/v1/diagnose（手动或后续自动触发）
                                      │
                         组装 prompt（指标名 + 最近 10 个点 + 基线）
                                      │
                                      ▼
                     OpenAI 兼容接口（超时 8s / 重试 3 次 / 熔断）
                                      │
                                      ▼
                              中文根因分析 + 处置建议
```

## 二、接入任意大模型（OpenAI 兼容）

支持所有提供 `/chat/completions` 的服务（智谱 GLM、通义千问、DeepSeek、OpenAI 等）。

```bash
export AIOSIGHT_LLM_ENDPOINT="https://open.bigmodel.cn/api/paas/v4/chat/completions"
export AIOSIGHT_LLM_API_KEY="你的 key"
export AIOSIGHT_LLM_MODEL="glm-4-flash"
export AIOSIGHT_LLM_TIMEOUT="8s"
export AIOSIGHT_LLM_MAX_TOKENS="512"

go run ./cmd/aiosight
```

Docker 里通过环境变量传：

```yaml
environment:
  AIOSIGHT_LLM_ENDPOINT: "https://open.bigmodel.cn/api/paas/v4/chat/completions"
  AIOSIGHT_LLM_API_KEY: "your-key"
  AIOSIGHT_LLM_MODEL: "glm-4-flash"
```

## 三、不配 key 也能演示

这是刻意设计的：`llm.Client` 在 `APIKey` 为空时走**本地启发式兜底**，输出结构化的中文分析（均值、标准差、偏离幅度、趋势、可能原因、处置建议）。

好处：
- 面试现场没有网络/额度也能演示完整链路；
- 单测可以断言确定性的输出，不依赖外部服务。

## 四、怎么验证 AI 功能真的在工作

```bash
# 1. 造一段基线 + 一个尖峰
for i in $(seq 1 20); do
  v=$((100 + i % 3))
  curl -s -X POST localhost:8081/api/v1/points -d "{\"metric\":\"http_latency_ms\",\"value\":$v}" > /dev/null
done
curl -s -X POST localhost:8081/api/v1/points -d '{"metric":"http_latency_ms","value":520}' > /dev/null

# 2. 诊断
curl -s -X POST localhost:8081/api/v1/diagnose -d '{"metric":"http_latency_ms"}' | python3 -m json.tool
```

预期：`analysis` 里出现「偏离基线 X 个标准差」「趋势」「可能原因」「建议处置」。

## 五、可靠性设计（面试重点）

| 机制 | 取值 | 原因 |
|---|---|---|
| 单次超时 | 8s | 诊断是辅助功能，不能阻塞主链路 |
| 重试 | 3 次，100/300/900ms 指数退避 | 偶发网络抖动可自愈 |
| 熔断 | 连续 5 次失败，断 30s，之后半开探测 | 下游持续故障时快速失败 |
| 降级 | 无 key / 调用失败 → 本地兜底 | 保证接口永远有可用输出 |

## 六、成本控制

- prompt 只带最近 10 个采样点，不传全量序列；
- `max_tokens` 限制 512，输出要求「不超过 200 字」；
- 诊断是**按需触发**，不是每个异常都调用模型（异常会自动告警，但诊断由人/规则触发），避免刷爆额度。
