# 开发验收记录

日期：2026-11-14。环境：macOS、Go 1.25.1、Node.js 24、Docker PostgreSQL 17、无界面 Chrome。

## 已通过

- `go test -race ./... -count=1`，显式设置独立的 `TEST_DATABASE_URL`，PostgreSQL/MCP 集成测试实际执行，未跳过。
- `go vet ./...`。
- `go build -o .runtime/api-to-mcp ./cmd/server`。
- `npm --prefix web run build`，包含严格 TypeScript 检查和 Vite 生产构建。
- `PLAYWRIGHT_CHANNEL=chrome npx playwright test`，完整浏览器流程通过，无 JavaScript 页面异常。

## 关键行为

后端验证了成员注册与登录、注册不能提权、错误密码、成员越权访问、管理员保护、接口加密落库、持久化读取、固定值禁止覆盖、未知与空参数拒绝、大整数精度边界、路径转义、请求头换行与连接控制字段限制、JSON 请求体映射、上游白名单、超时、重定向不跟随和响应大小限制。

MCP 验证使用官方 Go SDK 客户端连接真实 HTTP 服务，完成草稿隐藏、发布后发现、工具调用、固定参数与动态参数合并、按用户隔离、停用后移除、令牌撤销后拒绝调用；停用成员会撤销其会话及令牌，重新启用也不会恢复旧令牌。

前端验证从注册开始，经 Pod 示例配置、保存、真实 HTTP 试调用、OpenAPI/Higress 导出下载、发布后的编辑锁定、个人令牌创建与撤销、工具停用、删除和退出登录。手机宽度下整页横向溢出问题已修复，参数表在自身区域内横向滚动。

## 截图

以下截图来自隔离测试后端和本地模拟的 Pod API，不代表公司真实接口的返回结果。

- [登录页](screenshots/login-desktop.png)
- [桌面编辑与试调用](screenshots/editor-desktop.png)
- [手机编辑页](screenshots/editor-mobile.png)
- [接口库](screenshots/library-desktop.png)

截图已自行核对，未见整体溢出或控件重叠。手机参数表保留局部横向滚动。

## 未执行

- 未验证公司内网真实 API；此前该地址连接超时，鉴权和响应结构尚未确认。
- 未执行完整 Docker 应用镜像构建或整栈部署；已实际运行并验证 PostgreSQL 数据库容器。
- 未运行 Higress 运行时兼容性测试；独立 MCP 服务的调用链已验证。
- 新仓库未配置远端、未提交或推送，也未发布外部服务。
