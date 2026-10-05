# VerdantFlare Control Service

VerdantFlare Hub 的 Go 业务后端。首期采用模块化单体，统一暴露 `/api/control/*`，承载 Center Context、组织与权益、Market、Experience、API Center 聚合和内部运营权限。

## 当前能力

- Login subject 到 Center User、Membership、Organization、Role 的映射。
- 当前组织切换与服务端角色复核。
- 按组织权益过滤的应用 Market。
- Experience Session 历史记录查询、关闭；运行资源尚未接入，创建接口返回 503，不生成伪运行记录。
- 组织管理员可创建、查询与撤销限定已上架模型的网关 API Key；明文只在创建响应中出现，历史 Control-only Key 单独标记为不可调用。
- 客户成功管理员可按美元为组织追加网关额度；组织冻结同步停用网关账号。只读连通性测试验证 Key、余额及可见模型，不进行付费推理。
- 模型目录通过专用 Token 读取 new-api `/v1/models`，与运营显式上架记录求交集后提供 WWW 和 Hub；未配置凭据或网关异常时不回退样例。任务接口仍返回空列表，账单接口仍返回 503；API 用量摘要读取网关账户累计额度。
- 组织资料、成员记录及已绑定用户的组织角色／停用状态。
- 应用 Candidate → Preview 发布、客户组织创建/冻结与应用权益分配；运营操作按当前角色校验并持久化。
- 成员邀请在本版保存为待接受业务记录，不创建 Login 账号或发送邮件。
- Hub 管理的公开模型资料及应用版本落库；模型显式上架后同时可供 WWW 和 Hub 读取，但仍以网关当前返回的模型 ID 为准。
- 统一 JSON 错误、Request ID、安全响应头、请求体限制和优雅退出。

## 本地运行

```bash
cp .env.example .env
set -a && source .env && set +a
make run
```

默认监听 `:8080`。开发环境使用 `CONTROL_DEV_LOGIN_SUBJECT` 对接现有测试身份，但不初始化演示业务数据：

模型上架还需设置 `CONTROL_GATEWAY_BASE_URL=https://api.verdantflarehub.com` 和 `CONTROL_GATEWAY_TOKEN`。后者是在 new-api 创建的专用**客户端** Token（不是 DeepSeek/JD 供应商 Key），建议固定 `default` 分组、仅允许需同步的模型。Token 只保存在受控 Secret，不写入仓库；缺失时可编辑草稿，但上架与已有公开模型读取会返回 503。

组织 API Key 与额度管理还需设置 `CONTROL_GATEWAY_ADMIN_TOKEN`，与 new-api 的 `CENTER_CONTROL_TOKEN` 使用同一独立随机服务凭据（至少 32 字符）。它不能替代模型目录 Token，也不能暴露给浏览器。缺失时 Key、余额及额度操作返回 503；组织冻结／解冻失败关闭。

```bash
curl http://localhost:8080/api/control/context
```

Hub 本地开发服务器会把 `/api/control` 代理到 `http://localhost:8080`：

```bash
npm run dev
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
| GET | `/api/control/market/apps` | 已公开应用与当前组织获授权应用，附 `entitled` 状态 |
| GET | `/api/control/market/apps/{id}` | 应用详情 |
| GET/POST | `/api/control/experience/sessions` | Session 历史记录；POST 在运行资源接入前返回 503 |
| DELETE | `/api/control/experience/sessions/{id}` | 关闭 Session |
| GET/POST | `/api/control/api-keys` | 当前组织网关 Key 与历史记录列表；组织管理员创建限定模型 Key |
| DELETE | `/api/control/api-keys/{id}` | 撤销 API Key |
| GET | `/api/control/api-keys/{id}/probe` | 只读验证 Key、余额与可见模型 |
| GET/POST | `/api/control/ops/organizations/{id}/api-credit` | 客户成功管理员查询／追加美元额度 |
| GET | `/api/control/api/models` | 当前已上架且在专用网关 Token 的模型列表中可见的目录；不授予客户调用权限 |
| GET | `/api/control/api/tasks` | API 任务摘要 |
| GET | `/api/control/api/usage` | API 用量摘要 |
| GET/PATCH | `/api/control/settings/organization` | 组织资料 |
| GET/POST | `/api/control/settings/members` | 成员列表和邀请 |
| PATCH | `/api/control/settings/members/{id}` | 更新当前组织成员角色和状态 |
| GET | `/api/control/settings/billing` | 套餐标签、网关实际额度与管理员赠送额度流水；付费账单尚未接入 |
| GET | `/api/control/ops/releases` | 应用发布运营 |
| GET | `/api/control/public/catalog` | 匿名公开模型与应用目录，仅包含公开字段 |
| GET/POST | `/api/control/ops/models` | 查看、新建模型公开资料，需 `api_ops_admin` |
| GET | `/api/control/ops/gateway-models` | 查询专用网关 Token 当前可见的模型 ID，需 `api_ops_admin` |
| PATCH | `/api/control/ops/models/{id}` | 编辑模型资料与公开状态，需 `api_ops_admin` |
| GET | `/api/control/ops/organizations` | 客户组织运营 |
| POST | `/api/control/ops/apps` | 创建 Candidate 应用，需 `app_ops_admin` |
| GET/PATCH | `/api/control/ops/apps/{id}` | 查看、编辑应用与发布通道，需 `app_ops_admin` |
| POST | `/api/control/ops/organizations` | 创建无权益客户组织，需 `customer_success_admin` |
| GET/PATCH | `/api/control/ops/organizations/{id}` | 查看、更新套餐/冻结状态/应用权益，需 `customer_success_admin` |
| POST/PATCH | `/api/control/ops/organizations/{id}/members[/{memberId}]` | 创建或更新客户组织成员记录，需 `customer_success_admin` |

## 数据存储

领域层只依赖 `store.Repository`。配置 `CONTROL_DATABASE_URL` 后，首版 PostgreSQL Repository 将模块化单体状态保存在一个 JSONB 聚合行中，所有写操作通过行锁短事务串行化，支持 Pod 重启和单副本滚动发布。开发环境未配置数据库时仍可使用内存实现。新数据库仅初始化管理身份与空组织，不写入样例应用、模型、任务、Key、用量或账单；现有数据库不会被启动过程覆盖或自动清理。

该聚合存储是首发过渡边界；组织和成员仍由 Control 持有，网关 Token 和余额以 `verdantflare-api` 为事实源。业务量增长或需要多副本高写入吞吐时，再将其余业务数据拆为规范化表。

创建的应用默认 `Candidate`，不会出现在客户 Market。运营发布到 `Preview` 后，还需在客户组织详情授权；冻结组织、暂停或退回 Candidate 的应用都不会在 Market 显示。组织权益更新使用 `expectedEntitlementVersion` 检查并发修改，冲突返回 409；未发布应用的已有授权会保留，避免修改套餐时被隐式删除。

首版尚未打通 Login 账号邀请/全局禁用、邮件投递、Studio/Station 的真实安装与运行。Control 历史 Key 不等于模型网关凭证；Hub 调用示例会产生真实费用，但页面自身不代用户执行推理。只读连通性测试也不证明供应商推理健康。页面不应把待接受成员记录或体验 Session 记录当作这些能力已生效。存量样例清理和备份以工作区设计与运维记录为准。

## 验证

```bash
make test
make vet
make build
```

持久化回归测试使用独立、可丢弃的 PostgreSQL 数据库。先应用 `migrations/postgres/001_init.sql`，再设置 `CONTROL_TEST_DATABASE_URL` 运行 `go test ./internal/store -run TestManagedAppSurvivesPostgresReopen -v`。

也可在工作区根目录运行 `scripts/tests/hub_control_integration.sh`，自动使用临时 PostgreSQL 容器执行 Control 全量 Go 测试、静态检查与 Hub 构建。脚本只操作自己创建的临时容器和 `control_test` 数据库。
