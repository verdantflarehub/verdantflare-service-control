# VerdantFlare Control Service

VerdantFlare Hub 的 Go 业务后端。首期采用模块化单体，统一暴露 `/api/control/*`，承载 Center Context、组织与权益、Market、Experience、API Center 聚合和内部运营权限。

## 当前能力

- Login subject 到 Center User、Membership、Organization、Role 的映射。
- 当前组织切换与服务端角色复核。
- 按组织权益过滤的应用 Market。
- Experience Session 创建、并发/额度校验、关闭与清理状态。
- API Key 创建、范围和有效期校验、撤销；完整 Secret 只返回一次，服务端只保留 SHA-256。
- 模型目录、任务和用量聚合接口。
- 组织资料、成员邀请和账单摘要。
- 应用发布与客户运营接口的内部角色校验。
- 统一 JSON 错误、Request ID、安全响应头、请求体限制和优雅退出。

## 本地运行

```bash
cp .env.example .env
set -a && source .env && set +a
make run
```

默认监听 `:8080`。开发环境使用 `CONTROL_DEV_LOGIN_SUBJECT` 建立演示身份：

```bash
curl http://localhost:8080/api/control/context
```

Hub 本地开发服务器会把 `/api/control` 代理到 `http://localhost:8080`。启动 Hub 时关闭前端 Mock：

```bash
VITE_USE_MOCK=false npm run dev
```

## 认证边界

生产环境必须：

```text
CONTROL_ENV=production
CONTROL_DEV_LOGIN_SUBJECT=
CONTROL_TRUST_AUTH_HEADERS=true
```

认证代理验证 Login 会话后设置 `X-VF-Login-Subject`。代理必须删除客户端自行提交的同名 Header，Control Service 不接收密码或前端持久化 Token。后续接入服务端 JWT 验证时可替换该适配层，不改变业务模块。

## 主要接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/healthz`、`/readyz` | 存活和就绪检查 |
| GET | `/api/control/context` | Center Context |
| PUT | `/api/control/context/active-organization` | 切换当前组织 |
| GET | `/api/control/overview` | Hub 首页聚合摘要 |
| GET | `/api/control/market/apps` | 当前组织可用应用 |
| GET | `/api/control/market/apps/{id}` | 应用详情 |
| GET/POST | `/api/control/experience/sessions` | Session 列表和创建 |
| DELETE | `/api/control/experience/sessions/{id}` | 关闭 Session |
| GET/POST | `/api/control/api-keys` | API Key 列表和创建 |
| DELETE | `/api/control/api-keys/{id}` | 撤销 API Key |
| GET | `/api/control/api/models` | 已授权模型目录 |
| GET | `/api/control/api/tasks` | API 任务摘要 |
| GET | `/api/control/api/usage` | API 用量摘要 |
| GET/PATCH | `/api/control/settings/organization` | 组织资料 |
| GET/POST | `/api/control/settings/members` | 成员列表和邀请 |
| GET | `/api/control/settings/billing` | 账单摘要 |
| GET | `/api/control/ops/releases` | 应用发布运营 |
| GET | `/api/control/ops/organizations` | 客户组织运营 |

## 数据存储

领域层只依赖 `store.Repository`。配置 `CONTROL_DATABASE_URL` 后，首版 PostgreSQL Repository 将模块化单体状态保存在一个 JSONB 聚合行中，所有写操作通过行锁短事务串行化，支持 Pod 重启和单副本滚动发布。开发环境未配置数据库时仍可使用内存实现。

该聚合存储是首发过渡边界；业务量增长或需要多副本高写入吞吐时，应将组织、成员、Session 和 API Key 拆为规范化表，并将模型、Token、原始任务和余额接口逐步接入 `verdantflare-api`。

## 验证

```bash
make test
make vet
make build
```
