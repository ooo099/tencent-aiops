# 部署说明

## 一、本地单实例（最快）

```bash
AIOSIGHT_DEMO=1 go run ./cmd/aiosight
curl -s localhost:8080/healthz
```

## 二、Docker Compose：3 分片 + 路由 + 监控

```bash
cd deploy
docker compose up --build
```

起 6 个容器：

| 服务 | 端口 | 说明 |
|---|---|---|
| aiosight-1/2/3 | 内部 8080 | 3 个分片实例 |
| router | 8081 | 一致性哈希分片网关 |
| prometheus | 9090 | 抓取 3 个实例 + router |
| grafana | 3000 | admin / admin |

验证分片生效：

```bash
# 同一个指标反复写，永远落在同一个实例
for i in $(seq 1 20); do
  curl -s -X POST localhost:8081/api/v1/points -d '{"metric":"orders_qps","value":1}' > /dev/null
done
curl -s "localhost:8081/api/v1/query?metric=orders_qps" | python3 -c "import json,sys;print(json.load(sys.stdin)['count'])"
# 输出 20，说明读写都路由到同一分片
```

## 三、Kubernetes

镜像先构建好（或用 Docker Hub 上的 `ooo099/aiosight:latest`）：

```bash
docker build -f deploy/Dockerfile -t ooo099/aiosight:latest .
```

### 用 kind 本地验证

```bash
kind create cluster --name aiosight
kind load docker-image ooo099/aiosight:latest --name aiosight
kubectl apply -f deploy/k8s/all.yaml
kubectl -n aiosight get pods,svc,hpa
```

访问（NodePort 30081）：

```bash
curl -s localhost:30081/healthz
```

### 清单里有什么

| 资源 | 作用 |
|---|---|
| StatefulSet `aiosight`（3 副本） | 固定 Pod 名，router 通过 headless Service 的稳定 DNS 寻址 |
| headless Service | 给每个 Pod 一个 `aiosight-N.aiosight-headless` 地址 |
| Deployment `aiosight-router`（2 副本） | 无状态分片网关，可自由扩缩 |
| Service `aiosight-router`（NodePort） | 对外入口 |
| HPA | 只对 router 按 CPU 70% 扩缩（2–10） |
| PDB | 保证滚动更新时至少 2 个分片可用 |

### 验证自动扩容

```bash
kubectl -n aiosight get hpa -w
# 另开终端压测（任意压测工具）
kubectl -n aiosight run load --rm -it --image=busybox -- sh -c \
  'while true; do wget -q -O- http://aiosight-router/healthz > /dev/null; done'
```

观察 `aiosight-router` 副本数从 2 涨上去（需要集群有 metrics-server）。

## 四、滚动更新与容灾

```bash
kubectl -n aiosight set image statefulset/aiosight aiosight=ooo099/aiosight:v0.2.0
kubectl -n aiosight rollout status statefulset/aiosight
```

- StatefulSet 默认**逆序**更新，配合 `PodDisruptionBudget(minAvailable=2)`，任意时刻至少 2 个分片在线；
- 但注意：Pod 重启会丢失该分片的内存数据（已知限制，见 `docs/ARCHITECTURE.md`）。

## 五、镜像发布

打 tag 自动触发 GitHub Actions 构建多架构镜像并推送 Docker Hub：

```bash
git tag v0.1.0 && git push origin v0.1.0
```

需要先在仓库 Secrets 配置：

| Secret | 内容 |
|---|---|
| `DOCKERHUB_USERNAME` | Docker Hub 用户名 |
| `DOCKERHUB_TOKEN` | Docker Hub Access Token（不是密码） |

## 六、可观测性

- 每个实例暴露 `/metrics`，Prometheus 按 Pod 抓取；
- 关键指标：
  - `points_ingested_total`、`series_active`（写入量）
  - `anomalies_total`、`alerts_total`（异常与告警）
  - `llm_calls_total`、`llm_failed_total`、`llm_rejected_total`（AI 调用）
  - `router_routed_total{mode}`、`router_upstream_errors_total`（分片路由）
- Grafana 直接导入 Prometheus 数据源即可看板。
