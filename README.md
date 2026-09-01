# LLM Proxy Inspector

一个面向 Agentic RAG 调试的 OpenAI-compatible 请求代理。它把每个项目映射到独立的代理 API Key，透明转发模型请求，并在中文 Web 调试台中展示完整消息、原始 JSON、流式 SSE、Headers 和耗时。

## 能力边界

- `POST /v1/chat/completions`
- `GET /v1/models`
- 模型名、消息、工具定义和厂商扩展字段原样转发，不做模型白名单校验
- 同时记录非流式响应和流式 SSE；流式响应额外聚合 assistant 内容、refusal、function/tool calls、finish reason 与 usage
- SQLite 持久化；项目 Key 与上游 Key 使用 AES-256-GCM 加密
- 默认仅监听 `127.0.0.1:8080`，管理台不包含登录系统

其他 `/v1/*` 路径会返回 OpenAI 风格的 `unsupported_endpoint` 错误。

## 快速启动

### Docker Compose

```bash
docker compose up --build
```

打开 <http://127.0.0.1:8080>。Compose 只把端口映射到宿主机回环地址，数据保存在命名卷 `llm-proxy-data`。

### 本地构建

需要 Go 1.22+、Node.js 20+ 和 npm：

```bash
make build
./bin/llm-proxy
```

开发时可分别运行：

```bash
make dev-backend
make dev-ui
```

Vite 调试台会把 `/api`、`/v1` 和 `/healthz` 转发到 `127.0.0.1:8080`。

## 接入 OpenAI 客户端

先在管理台创建项目，填写上游 BaseURL 与上游 API Key，然后复制项目 API Key：

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8080/v1",
    api_key="llmp_项目Key",
)

response = client.chat.completions.create(
    model="任意上游模型名",
    messages=[{"role": "user", "content": "hello"}],
)
```

`stream=True` 无需额外配置。代理逐块 flush 上游 SSE，不会先缓存完整响应。

BaseURL 按 SDK 的 API 根地址处理。例如上游填写 `https://api.openai.com/v1`，代理会追加 `/chat/completions` 或 `/models`。上游 API Key 可留空，以支持本地 OpenAI-compatible 服务。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LLMPROXY_LISTEN` | `127.0.0.1:8080` | HTTP 监听地址 |
| `LLMPROXY_DATA_DIR` | `./data` | SQLite 与主密钥目录 |
| `LLMPROXY_CAPTURE_MAX_BYTES` | `33554432` | 单个请求体或响应体最多记录的字节数；超出后仍继续转发 |
| `LLMPROXY_RETENTION_DAYS` | `7` | 记录保留天数；`0` 表示不按天数清理 |
| `LLMPROXY_MAX_REQUESTS_PER_PROJECT` | `10000` | 每项目最多记录数；`0` 表示不按条数清理 |
| `LLMPROXY_UPSTREAM_HEADER_TIMEOUT` | `5m` | 等待上游响应头的最长时间 |

服务首次启动会在数据目录生成：

- `llm-proxy.db`：项目和抓包记录；请求/响应正文为明文
- `master.key`：加密 API Key 的 32 字节主密钥，权限为 `0600`

备份和恢复时必须同时处理这两个文件。如果数据库存在但主密钥丢失，服务会拒绝启动，避免生成错误密钥导致原凭据不可恢复。

## 安全说明

- 管理台没有身份认证，默认不要监听公网地址。
- `Authorization`、`api-key`、Cookie 等敏感 Header 在抓包记录中会替换为 `[REDACTED]`。
- 请求与响应正文不会脱敏，因为它们是调试内容；请把数据目录视作敏感数据。
- 项目 API Key 可在管理台随时解密查看；轮换后旧 Key 立即失效。
- 上游 API Key 只能更新或清除，管理台只显示掩码。

## 管理 API

- `GET/POST /api/projects`
- `GET/PATCH/DELETE /api/projects/{id}`
- `POST /api/projects/{id}/reveal-key`
- `POST /api/projects/{id}/rotate-key`
- `POST /api/projects/{id}/test-upstream`
- `GET/DELETE /api/projects/{id}/requests`
- `GET /api/requests/{id}`
- `GET /api/events?project_id=...`
- `GET /healthz`

项目和管理 API 响应不启用跨域。请求列表使用 `cursor` 游标分页，支持 `status`、`path`、`model` 和 `streaming` 查询参数。

## 验证

```bash
make test
make test-e2e
```

后端测试包含加密、SSE 聚合、Header 脱敏、透明转发和“日志截断但响应继续完整转发”。浏览器端到端测试会启动本地伪上游，验证项目创建、代理请求、详情展示、连接测试、Key 轮换与项目删除。

