# 接口工坊 · API-to-MCP

将公司 REST API 配置为可被 AI 客户端发现和调用的 MCP 工具。前端使用 Vue 3 + TypeScript，后端使用 Go，数据库使用 PostgreSQL，直接提供独立 MCP 服务。

## 已实现范围

- 开放注册、账户登录和退出；初始管理员由环境配置创建。
- 成员只管理自己的接口；管理员可查看和管理全部接口、停用和启用成员账户。
- 每个接口配置 URL、请求方式、用途描述、超时和参数表。
- 参数支持查询、路径、请求头、JSON 请求体；支持字符串、布尔、整数、数字、对象和数组。
- 固定参数由服务端填写且禁止客户端覆盖；动态参数生成 MCP 输入结构。
- 草稿保存、真实试调用、发布、停用和删除。已发布接口必须先停用才可修改。
- 个人 MCP 令牌创建与撤销；令牌仅调用所属用户自己的已发布工具，管理员令牌也不会自动调用他人接口。
- 导出 OpenAPI 与 Higress YAML；核心转换复用原项目的解析器、转换器和配置模型。

## 快速启动

需要 Go 1.25、Node.js 24、Docker 与 Docker Compose。以下命令均在项目根目录执行。

```bash
go run ./cmd/server -init
docker compose up -d --wait db
npm --prefix web ci
npm --prefix web run build
go run ./cmd/server
```

打开 <http://127.0.0.1:8088>，可以直接注册成员账户。管理员用户名默认 `admin`，随机初始密码保存在本地 `.env` 的 `ADMIN_PASSWORD` 中，初始化命令不会打印密码。

`.env` 使用 `0600` 文件权限，已被 Git 与 Docker 构建上下文排除。重复执行 `-init` 会拒绝覆盖现有文件。

数据库地址默认为 `127.0.0.1:55432`。数据持久化在 Docker 卷中，停止服务使用 `docker compose stop db`；不要随意删除数据库卷。

### 前端开发

保持 Go 服务运行，在另一个终端执行：

```bash
npm --prefix web run dev
```

开发地址为 <http://127.0.0.1:5178>，Vite 将 `/api` 和 `/mcp` 代理到 Go 服务。

### 完整容器运行

初始化 `.env` 后可以执行：

```bash
docker compose --profile full up -d --build
```

默认只绑定本机端口。提供给同事使用时，应通过 HTTPS 反向代理暴露服务，保留请求 Host，并设置 `COOKIE_SECURE=true`。认证使用同源 HttpOnly Cookie，写操作检查 Origin；MCP 则单独使用 Bearer 令牌。

## 配置一个接口

在接口库选择“新建接口”，或“使用 Pod 查询示例”。示例对应：

```text
GET http://10.12.72.42:9007/ws/aquaman/kubernetes/pods/getPodInstance?showAllPods=false&podIp=10.60.144.183
```

参数配置为：

| 名称 | 位置 | 类型 | 映射 | 必填 |
| --- | --- | --- | --- | --- |
| showAllPods | query | boolean | 固定值 `false` | 由服务端保证 |
| podIp | query | string | 客户端填写 | 是 |

保存后，用以下参数试调用，再发布工具：

```json
{"podIp":"10.60.144.183"}
```

JSON 请求体由 `body` 位置的参数组成一个对象。嵌套数据用 `object` 或 `array` 类型参数传入。数组和对象不支持放入 query/path/header。路径变量使用 `{id}`，其动态参数必须标记为必填。

整数限制在 JavaScript 安全整数范围 `±9007199254740991` 内；更长的业务 ID 应配置为字符串，避免浏览器或 JSON 数值处理静默丢失精度。

URL 中原有查询字符串会保留为固定基础值，参数表同名 query 参数会覆盖基础值。推荐使用“提取 URL 查询参数”将它们显式放到参数表中；提取时按字符串保留值，不猜测接口类型。

实际凭据可配置为固定请求头，例如 `Authorization` 或 `X-API-Key`。完整接口定义通过 AES-256-GCM 加密后存入数据库，只有接口所有者和管理员可以读取和导出。

## 上游访问范围

只有 `.env` 中 `UPSTREAM_ORIGINS` 列出的源地址能被发布或调用：

```dotenv
UPSTREAM_ORIGINS=http://10.12.72.42:9007,https://api.example.com
```

每项必须是完整的协议、主机及可选端口，不能使用通配符、路径或账号信息。修改后需重启 Go 服务。不会使用系统 HTTP 代理，不跟随上游重定向；默认超时 15 秒，允许配置 1–60 秒，响应限制为 2 MiB，不自动重试写请求。

## 连接 MCP 客户端

1. 发布至少一个自己的接口。
2. 打开“连接与令牌”，创建个人访问令牌并立即保存；完整值只展示一次。
3. 在支持 Streamable HTTP 的客户端中配置地址和请求头：

```text
地址：http://127.0.0.1:8088/mcp
请求头：Authorization: Bearer <个人访问令牌>
```

客户端可通过 `tools/list` 发现工具，再通过 `tools/call` 调用。工具列表按用户隔离且每次从数据库读取；发布、停用及令牌撤销在后续请求生效。上游非 2xx 响应作为 MCP 工具错误返回，同时保留状态码和响应正文。

管理员停用成员时，会撤销其全部会话和 MCP 令牌；重新启用账户后，需要重新登录和创建令牌。

## 验证

```bash
go test ./...
go vet ./...
npm --prefix web run build
```

PostgreSQL/MCP 集成测试使用独立测试数据库，并在随机 schema 中运行，结束后只删除该 schema：

```bash
docker compose exec -T db createdb -U api_mcp api_to_mcp_test
# 使用测试数据库连接串，数据库名必须以 _test 结尾。
TEST_DATABASE_URL='postgres://api_mcp:<DB_PASSWORD>@127.0.0.1:55432/api_to_mcp_test?sslmode=disable' go test -race ./... -count=1
```

未提供 `TEST_DATABASE_URL` 时，集成测试会明确跳过。端到端浏览器测试见 [web/e2e](web/e2e)：

```bash
cd web
npx playwright install chromium
npm run build
npx playwright test
```

浏览器测试始终无界面运行，自动启动独立测试后端 `8089` 和模拟上游 `8099`，使用 `_test` 数据库，不使用工作台的正式数据库。它会注册测试账户，结束时删除其工具与令牌，但保留测试账户；仅在专用测试库中运行。也可用 `PLAYWRIGHT_CHANNEL=chrome npx playwright test` 复用本机 Chrome，无需下载 Chromium。

## 结构与复用

```text
cmd/server/          启动、环境配置初始化
internal/platform/  账户、加密存储、接口定义、HTTP 映射、MCP 和导出
web/                Vue 3 + TypeScript 工作台
compose.yaml        PostgreSQL 与完整容器部署配置
DESIGN.md           界面规范
```

通过 Go module 固定引用原项目 `github.com/higress-group/openapi-to-mcpserver` 的 `e938e6304912` 版本，导出链路调用 `parser.NewParser`、`converter.NewConverter` 和 `models.MCPConfig`，无需依赖同级项目目录或本地 replace。

独立运行层使用[官方 Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0)。选择 PostgreSQL 是为了公司账户与配置的集中管理和后续扩展；SQLite 也适用于低写并发单机应用，相关边界见 [SQLite 官方说明](https://sqlite.org/whentouse.html)。

## 当前边界

- 首版包含管理员和成员两种角色，不含 SSO、组织分组、审批和账户找回。
- 账户可以自行注册；能注册不代表能访问任意网络，上游源地址仍由部署者控制。
- MCP 使用静态个人 Bearer 令牌，未实现 OAuth 授权流程；客户端需支持自定义认证请求头。
- 不自动请求接口来猜测类型、业务语义或响应 schema。试调用会真实执行请求，写接口也会产生实际业务影响。
- 工具以 JSON 对象请求体为主，不支持 multipart 文件上传、流式大响应和任意模板执行。
- 登录限流为单进程内 IP 限流，多实例部署需要共享限流。Origin 校验依赖反向代理保留 Host。
- 请备份数据库与 `ENCRYPTION_KEY`；丢失密钥后无法恢复已加密的接口配置。修改 `.env` 的初始管理员密码不会重置已存在账户。
- 导出可能包含固定凭据。Higress 导出将固定 query/path/header 写入请求模板；固定 body 字段以单值枚举和默认值表示，未做 Higress 运行时兼容性验收。
- 公司内网 Pod 示例在此前环境中连接超时，实际接口响应和鉴权要求仍需在公司网络中验证。
