# LLM Proxy Inspector

一个面向 Agentic RAG 调试的 OpenAI-compatible 请求代理。它把每个项目映射到独立的代理 API Key，透明转发模型请求，并在中文 Web 调试台中展示完整消息、原始 JSON、流式 SSE、Headers 和耗时。

## 能力边界

- `POST /v1/chat/completions`
- `GET /v1/models`
- 模型名、消息、工具定义和厂商扩展字段原样转发，不做模型白名单校验
- 同时记录非流式响应和流式 SSE；流式响应额外聚合 assistant 内容、refusal、function/tool calls、finish reason 与 usage
- 请求详情展示本轮模型总耗时；相邻请求的 `tool_call_id` 匹配时，还会展示从上一轮响应结束到工具结果回传的推算耗时（包含 Agent 调度开销，并发工具共享批次耗时）
- 可按项目开启或暂停抓包；暂停后可勾选指定请求保存为命名分组，并按分组回看
- SQLite 持久化；项目 Key 与上游 Key 使用 AES-256-GCM 加密
- 默认仅监听 `127.0.0.1:8080`，管理台使用单用户账号登录

其他 `/v1/*` 路径会返回 OpenAI 风格的 `unsupported_endpoint` 错误。

## 快速启动

### Docker Compose

```bash
docker compose up --build
```

打开 <http://127.0.0.1:8080>，默认账号和密码均为 `admin`。Compose 只把端口映射到宿主机回环地址，数据保存在命名卷 `llm-proxy-data`。

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

服务启动时会自动读取当前目录的 `.env`，操作系统环境变量的优先级高于 `.env`。修改登录账号或密码后需要重启服务。

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
| `LLMPROXY_USERNAME` | `admin` | 管理台单用户账号，明文配置 |
| `LLMPROXY_PASSWORD` | `admin` | 管理台密码，明文配置且无强度限制 |
| `NOAUTH` | `false` | 设置为 `true`、`1`、`yes` 或 `on` 时关闭管理台登录 |
| `LLMPROXY_CAPTURE_MAX_BYTES` | `33554432` | 单个请求体或响应体最多记录的字节数；超出后仍继续转发 |
| `LLMPROXY_RETENTION_DAYS` | `7` | 记录保留天数；`0` 表示不按天数清理 |
| `LLMPROXY_MAX_REQUESTS_PER_PROJECT` | `10000` | 每项目最多记录数；`0` 表示不按条数清理 |
| `LLMPROXY_UPSTREAM_HEADER_TIMEOUT` | `5m` | 等待上游响应头的最长时间 |

服务首次启动会在数据目录生成：

- `llm-proxy.db`：项目和抓包记录；请求/响应正文为明文
- `master.key`：加密 API Key 的 32 字节主密钥，权限为 `0600`

备份和恢复时必须同时处理这两个文件。如果数据库存在但主密钥丢失，服务会拒绝启动，避免生成错误密钥导致原凭据不可恢复。

## 安全说明

- 管理台默认使用 `.env` 中的单用户账号认证。默认密码仅用于初始化，请按需直接修改 `.env` 并重启服务。
- 登录 Cookie 为 HttpOnly、SameSite=Strict 的会话 Cookie，服务重启后已有会话会失效。通过 HTTPS 访问时还会设置 Secure 属性。
- `NOAUTH=true` 会完全关闭管理 API 的登录保护，仅应在可信网络中使用。
- `/v1/*` 仍使用每个项目自己的 API Key，不受管理台登录和 `NOAUTH` 设置影响；`/healthz` 也保持公开。
- `Authorization`、`api-key`、Cookie 等敏感 Header 在抓包记录中会替换为 `[REDACTED]`。
- 请求与响应正文不会脱敏，因为它们是调试内容；请把数据目录视作敏感数据。
- 项目 API Key 可在管理台随时解密查看；轮换后旧 Key 立即失效。
- 上游 API Key 只能更新或清除，管理台只显示掩码。

## 管理 API

- `GET /api/auth/status`
- `POST /api/auth/login`（请求体为 `{ "username": "...", "password": "..." }`）
- `POST /api/auth/logout`
- `GET/POST /api/projects`
- `GET/PATCH/DELETE /api/projects/{id}`
- `POST /api/projects/{id}/reveal-key`
- `POST /api/projects/{id}/rotate-key`
- `POST /api/projects/{id}/test-upstream`
- `POST /api/projects/{id}/capture/start`
- `POST /api/projects/{id}/capture/pause`
- `POST /api/projects/{id}/capture/clear`（删除本轮已结束的请求，保留历史分组和运行中的请求）
- `GET/POST /api/projects/{id}/capture-groups`（POST 请求体为 `{ "name": "...", "requestIds": ["req_..."] }`，需选择 1–100 条已结束、非运行中的请求）
- `GET/DELETE /api/projects/{id}/requests`
- `POST /api/projects/{id}/requests/batch-delete`（请求体为 `{ "ids": ["req_..."] }`，单次最多 100 条）
- `GET /api/requests/{id}`
- `GET /api/events?project_id=...`
- `GET /healthz`

项目和管理 API 响应不启用跨域。请求列表使用 `cursor` 游标分页，支持 `status`、`path`、`model`、`streaming` 和 `groupId` 查询参数。暂停抓包只停止记录新请求，不影响代理转发，也不会中断暂停前已经开始的请求。

## 验证

```bash
make test
make test-e2e
```

后端测试包含加密、SSE 聚合、Header 脱敏、透明转发和“日志截断但响应继续完整转发”。浏览器端到端测试会启动本地伪上游，验证项目创建、代理请求、详情展示、连接测试、Key 轮换与项目删除。
