# MyProject

基于 Gin 框架的 Go Web 项目模板，采用清晰的分层架构设计，适用于中小型 Web API 项目快速开发。

## 目录

- [项目结构](#项目结构)
  - [依赖方向](#依赖方向)
  - [每个目录的核心职责](#每个目录的核心职责)
  - [一个请求怎么流过这些目录](#一个请求怎么流过这些目录)
  - [新增一个业务模块要改哪些文件](#新增一个业务模块要改哪些文件)
- [技术栈](#技术栈)
- [快速开始](#快速开始)
- [配置说明](#配置说明)
- [分层架构](#分层架构)
- [可观测性](#可观测性)
- [优雅退出与摘流](#优雅退出与摘流)
- [API 接口](#api-接口)
- [数据模型](#数据模型)
- [错误码](#错误码)
- [测试](#测试)
- [常用命令](#常用命令)
- [License](#license)

## 项目结构

先看一眼顶层目录各自负责什么，再往下看细节：

| 目录 | 一句话职责 | 什么时候你会改它 |
|------|-----------|-----------------|
| `cmd/` | 可执行程序入口，每个子目录编译出一个二进制 | 新增一个独立进程（如 worker、定时任务）时 |
| `configs/` | 配置**数据**（yaml），按环境分层覆盖 | 调端口、连接池、限流阈值等运行参数时 |
| `internal/` | 本项目私有代码，Go 编译器禁止外部 module 引用 | 绝大多数业务开发都在这里 |
| `pkg/` | 与业务无关的基础设施，不依赖 `internal/` | 换日志库、加一种缓存、扩错误码体系时 |
| `test/` | 跨层的集成/端到端测试 | 补接口级、中间件级回归时 |
| `docs/` `scripts/` `logs/` | API 文档产物、构建部署脚本、运行期日志 | 一般不手改 |

```
myproject/
├── cmd/                            # 程序入口目录
│   ├── server/
│   │   └── main.go                 # 主程序入口：装配依赖、启动/优雅退出
│   └── migrate/
│       └── main.go                 # 数据库迁移入口（make migrate）
│
├── configs/                        # 配置数据（只放 yaml，不放 Go 代码）
│   ├── config.yaml                 # 默认配置（不含任何真实凭据）
│   ├── config.dev.yaml             # 开发环境配置（覆盖默认配置）
│   ├── config.test.yaml            # 测试环境配置
│   ├── config.prod.yaml            # 生产环境配置
│   └── config.local.yaml           # 本机凭据覆盖（.gitignore，仅非生产环境加载）
│
├── internal/                       # 内部包（Go 编译器强制：外部项目无法引用）
│   ├── config/                     # 配置结构、加载、环境变量绑定、启动校验
│   │   ├── config.go               # 结构体定义、默认值、加载与合并
│   │   └── validate.go             # 启动校验（什么样的配置算合法）
│   │
│   ├── bootstrap/                  # 进程装配与生命周期（main 只调它）
│   │   └── bootstrap.go            # 初始化 DB/Redis/两个 server + 启停摘流优雅关闭
│   │
│   ├── resource/                   # 进程级共享资源（DB / JWT / Redis / Cfg）
│   │   └── resource.go             # bootstrap 注入一次；DB(ctx) 自动认领事务
│   │
│   ├── controller/                 # HTTP 层（全部包级函数，无 struct 无构造函数）
│   │   ├── common.go               # 本层公共：参数绑定、pathID、校验错误中文化 + 413 识别
│   │   ├── system.go               # 系统端点：探针 livez/readyz + 404/405
│   │   ├── user.go                 # 用户相关接口
│   │   └── order.go                # 订单相关接口
│   │
│   ├── service/                    # 业务逻辑（包级函数，不写 SQL）
│   │   ├── user.go                 # 用户业务（注册、登录、资料、列表）
│   │   └── order.go                # 订单业务（创建、状态流转+流水、归属校验）
│   │
│   ├── data/                       # 数据访问层（包级函数，唯一写 SQL 的地方）
│   │   ├── common.go               # connDb(ctx) 事务感知连接 + IsNotFound/IsDuplicate
│   │   ├── user.go                 # 用户表读写
│   │   └── order.go                # 订单表与流水表读写
│   │
│   ├── model/                      # 数据模型定义
│   │   ├── common.go               # 分页请求 + 全项目唯一的分页归一化 NormalizePage
│   │   ├── user.go                 # 用户模型、请求/响应结构体
│   │   └── order.go                # 订单模型、请求/响应结构体
│   │
│   ├── middleware/                 # HTTP 中间件
│   │   ├── common.go               # 本层公共：NoOp 占位中间件
│   │   ├── requestid.go            # 请求 ID 生成/透传，绑定 ctx logger
│   │   ├── recovery.go             # panic 恢复（结构化日志 + 堆栈 + 指标）
│   │   ├── metrics.go              # RED 指标采集（route 标签用路由模板）
│   │   ├── logger.go               # 访问日志（脱敏，按需记录 body）
│   │   ├── secure.go               # 安全响应头（nosniff / DENY / CSP / HSTS）
│   │   ├── auth.go                 # JWT 认证 + SelfOnly 归属校验
│   │   ├── cors.go                 # 跨域（白名单来自配置）
│   │   ├── ratelimit.go            # 单机令牌桶限流（按 IP，分 global/auth 配额）
│   │   ├── bodylimit.go            # 请求体大小上限（超限返回 413）
│   │   └── timeout.go              # 单请求 ctx 超时
│   │
│   └── router/                     # 路由配置
│       └── router.go               # 引擎设置 + 中间件顺序 + 全部路由表
│
├── pkg/                            # 与业务无关的基础设施，不依赖 internal/
│   ├── auth/                       # 认证原语
│   │   ├── jwt.go                  # JWT 生成与解析
│   │   └── password.go             # 密码哈希与校验（bcrypt）
│   ├── database/mysql.go           # MySQL 连接池 + GORM 日志接入
│   ├── cache/redis.go              # Redis 客户端封装
│   ├── logger/                     # 基于 log/slog 的日志
│   │   ├── logger.go               # slog 初始化、级别、ctx 贯穿
│   │   └── rotate.go               # 按小时轮转的文件写入器 + 保留策略
│   ├── response/response.go        # 统一响应封装
│   ├── errcode/errcode.go          # 错误码体系（支持 Unwrap/Is）
│   ├── health/                     # 依赖健康检查
│   │   ├── health.go               # Registry：注册、探测、结果缓存、摘流状态
│   │   └── std.go                  # 包级默认注册表（RegisterFunc / Check 直接调用）
│   ├── metrics/metrics.go          # Prometheus 指标定义 + DB 连接池采集
│   ├── admin/admin.go              # 内部端口：/metrics、/debug/pprof、/version
│   ├── transaction/transaction.go  # 事务边界：Do(ctx, fn)，句柄放 ctx，支持嵌套
│   ├── safego/safego.go            # 带 panic 保护的 goroutine 启动方式
│   └── buildinfo/buildinfo.go      # 编译期注入的版本信息
│
├── scripts/                        # 脚本目录
│   ├── build.sh                    # 构建脚本
│   └── deploy.sh                   # 部署脚本
│
├── docs/swagger/                   # Swagger API 文档（make swagger 生成）
├── logs/                           # 日志目录，按小时轮转 app_YYYYMMDDHH.log
│
├── test/                           # 真库集成测试
│   ├── setup_test.go               # TestMain：加载配置 / AutoMigrate / resource.Set / 建 engine
│   ├── user_api_test.go            # 用户接口：注册登录、越权、分页、脱敏
│   ├── order_api_test.go           # 订单接口：状态流转与流水、归属隔离
│   ├── framework_test.go           # 框架层（免 DB）：405/413/限流/探针/panic/指标基数/安全头
│   ├── layering_test.go            # 分层边界（免 DB）：扫 import 表，service 不许 import gorm
│   └── tx_test.go                  # 事务：提交、回滚、嵌套、句柄失效
│                                   # 另有 internal/config/config_test.go、pkg/logger/rotate_test.go
│                                   #      pkg/transaction/transaction_test.go、internal/middleware/logger_test.go
│
├── .github/workflows/ci.yml        # CI：fmt / vet / race test / lint / govulncheck / docker
├── .golangci.yml                   # 静态检查配置
├── docker-compose.yml              # 本地依赖（MySQL + Redis）
├── .air.toml                       # 热重载配置（make dev）
├── Dockerfile                      # 多阶段构建镜像
├── .dockerignore                   # 排除凭据/日志/.git，避免进 builder 层
├── .gitignore                      # Git 忽略配置
├── go.mod / go.sum                 # 依赖定义与锁定
├── Makefile                        # 构建命令
└── README.md                       # 项目说明
```

### 依赖方向

这套结构的核心约束只有一条：**依赖单向向下，不许回头**。

```
cmd/  ──▶  internal/bootstrap  ──▶  internal/router ──▶ internal/middleware
                  │                        │
                  │                        ▼
                  │                internal/controller  HTTP 边界：绑定、校验、写响应
                  │                        │
                  │                        ▼
                  │                internal/service     业务规则、事务边界、错误映射
                  │                        │
                  │                        ▼
                  │                internal/data        唯一允许写 SQL 的地方
                  │                        │
                  │                        ▼
                  │                internal/model       结构体，谁都能依赖
                  │
                  ├──▶ internal/resource   进程级资源，data/controller/middleware 读
                  ▼
             pkg/*   基础设施，不依赖 internal/ 任何东西
```

两条能自检的规则：

- `pkg/` 里如果出现 `import "myproject/internal/..."`，就是写错了。基础设施一旦反向依赖业务配置，它就没法被单独复用，`config` 字段改名也会波及到它。所以 `database.NewMySQL` 收的是自己的 `Options`，由 `bootstrap` 负责把 `config` 翻译过去。
- 下层不认识上层。`service` 不接触 `*gin.Context`、不知道 HTTP 状态码（它返回 `errcode` 里的业务错误，由 `pkg/response` 决定映射成几号）、也不写 SQL；`controller` 不碰数据库、不做业务判断；`data` 不认识 `errcode` 与 gin。

这三条边界不靠人守 —— `test/layering_test.go` 直接扫 import 表，`service` import 了 gorm、`controller` import 了 `internal/data`、`data` import 了 `errcode`，测试就红。

### 每个目录的核心职责

#### 入口与装配

**`cmd/`** — 每个子目录编译出一个独立二进制，目录名就是产物名。这里只做「决定用哪份配置 + 调用装配 + 决定退出码」，不写业务。`cmd/server/main.go` 全文 77 行，一眼能读完启动顺序；`cmd/migrate/main.go` 用 GORM AutoMigrate 同步表结构，复用 `bootstrap.DBOptions` 拿到同一套连接参数。

**`internal/bootstrap/`** — 进程的装配车间与生命周期管理者，是理解这个框架最该先读的目录。

整个包只有一个 `bootstrap.go`，按进程生命周期顺序读下来就是全部：

- `Init(cfg)` 按依赖顺序创建组件 —— 数据库（强依赖，失败即退出）→ Redis（可配置关闭）→ 交给 `internal/resource` → HTTP Server → admin Server；同时把 DB/Redis 注册进健康检查、把连接池指标注册进 Prometheus。中途失败会释放已建立的资源（此时调用方的 `defer app.Close()` 还没注册）。业务各层不在这里装配，因为已经没有需要装配的东西了。
- `Run()` 并发启动业务端口与 admin 端口（admin 起不来只告警不退出），等 SIGINT/SIGTERM，先 `drain()` 摘流再 `Shutdown()`；关闭期间单独监听第二次信号，给运维留「再按一次立刻退出」的逃生口。
- `Close()` 逆序释放资源。

把这些从 main 里搬出来的好处是：**新增一个依赖只改这一个文件，main 永远不变**。

#### 配置

**`configs/`** — 只放 yaml，不放 Go 代码。四层覆盖，优先级从低到高：`config.yaml`（入库，不含任何真实凭据）→ `config.<env>.yaml` → `config.local.yaml`（本机凭据，已 gitignore）→ 环境变量 `APP_*`。

两条与安全相关的加载规则：`-env` 取值被白名单限定为 `dev/test/prod`（拼错直接启动失败，而不是静默按默认值跑）；`config.local.yaml` **只在非生产环境加载** —— 它优先级高于环境配置，一旦随 `configs/` 目录同步到生产机会静默替换生产的库地址与 JWT secret。`config.prod.yaml` 在 `-env=prod` 时必须存在。

**`internal/config/`** — 配置的 Go 侧，两个文件各管一件事：`config.go` 是结构体定义、默认值、加载合并与环境变量绑定；`validate.go` 是**启动即校验**，会在启动时直接拒绝「JWT secret 用了占位符」「生产环境 secret 短于 32 字节」「CORS 通配符 + allow_credentials」这类问题，而不是等到线上才暴露。拆开是因为「配置怎么加载」和「什么样的配置算合法」是两件独立的事，排查时也总是只看其中一件。放在 `internal/` 是因为配置结构是应用私有的，外部 module 没有理由引用它。

#### HTTP 边界

**`internal/router/`** — 决定「请求进来先经过什么」，以及「有哪些路由」。只有一个 `router.go`，分两段读。

前半段是**服务的形状**，`Setup` 读下来就是启动顺序：

```go
useBaseMiddleware(r, cfg)      // RequestID → Metrics → BodyLimit → Logger → Recovery → 安全头 → CORS
registerSystemRoutes(r)        // 探针必须夹在这里：gin 的 Use 只作用于之后注册的路由
useThrottleMiddleware(r, cfg)  // 限流 + 超时
registerAPIRoutes(r, cfg)      // /api/v1 业务路由
registerFallbackRoutes(r)      // 404 / 405
```

再往下是 `// ---------- 路由表 ----------`，**全部路由集中在这里**：

```go
v1 := r.Group("/api/v1")
registerUser(v1, auth, authLimit)
registerOrder(v1, auth)
```

**新增接口就在对应的 `registerXxx` 里加一行**，新增模块就加一个 `registerXxx` 函数并在 `registerAPIRoutes` 里调一次 —— `Setup` 一行都不用动。路由集中在一处的好处是「这个服务对外提供什么」有唯一答案，不需要翻 N 个模块文件去拼。

**`internal/resource/`** — 进程级共享资源的持有者，这是本框架「装配」的全部内容。

```go
// bootstrap 启动时注入一次
resource.Set(cfg, db, redisClient, jwtManager)

// data 层取连接（其他层不该调 DB）
db := resource.DB(ctx)   // ctx 里有事务句柄就复用事务，否则用默认连接
// controller 取 JWT 管理器
jwt := resource.JWT()
```

为什么用全局单例而不是层层注入：DB、JWT 这些东西进程内只有一份、生命周期与进程等长，「初始化一次 + 全局读取」是最直接的表达。原来为了能替换实现，每加一个模块要写 data 接口 + 实现 + 构造函数、service 接口 + 实现 + 构造函数，再在 module 包里把它们串起来 —— 五六十行没有一行业务逻辑。现在这些全部消失，分层还在（`internal/data` 仍是唯一写 SQL 的地方），只是层与层之间用包级函数调用而不是接口 + 注入。

代价写在明面上：**业务层不能再用 mock 替换数据库**，所以测试改走真库集成测试（见「测试」一节）。这是个取舍，不是免费的。

**`internal/middleware/`** — 横切关注点，每个文件一个独立能力，装配顺序即执行顺序：

| 文件 | 作用 | 为什么在这个位置 |
|------|------|-----------------|
| `requestid.go` | 生成/透传 X-Request-ID，绑进 ctx logger | 最先，后续所有日志都要带它 |
| `metrics.go` | RED 指标采集（收尾在 defer 里） | 放在限流**之前**，被拒的请求也要计入 QPS；用 defer 才能让 panic 请求也计数、in_flight 能归零 |
| `bodylimit.go` | 请求体上限，超限 413 | 必须早于任何读 Body 的中间件，否则 MaxBytesReader 包不到真实 Body |
| `logger.go` | 访问日志，query 与 body 都脱敏，按需记 body | 记日志的那份 body 会截断，但交给 controller 的 Body 始终完整；收尾在 defer 里，panic 请求也留日志 |
| `recovery.go` | panic 恢复 + 堆栈 + 指标 | 在 Metrics/Logger 的**内层**：先写好 500，外层才能观测到真实状态码（放外层会记成 200）|
| `secure.go` | nosniff / DENY / CSP / HSTS 响应头 | —— |
| `cors.go` | 跨域，白名单来自配置 | —— |
| `ratelimit.go` | 单机令牌桶，按 IP，分 global/auth 两档配额；桶数量有上限，清理由请求驱动 | 认证接口单独限流：bcrypt 是 CPU 放大器 |
| `timeout.go` | 单请求 ctx 超时 | 最后，包住真正的业务处理 |

`auth.go` 不在全局链上，它是按路由组挂的：`Auth()` 校验 JWT，`SelfOnly()` 做资源归属校验（防止任何登录用户删除任意账号）。

**`internal/controller/`** — HTTP 与业务的翻译层。职责被刻意限制在四件事：绑参、校验、调 service、写响应。**不写业务规则，不碰数据库**。

- `common.go`：本层公共能力集中在这一个文件 —— 泛型 `bindJSON[T]` / `bindQuery[T]`（把「重复的 ShouldBind 样板 + 校验错误中文化 + 413 识别」收敛成一处，校验失败返回的字段名用 json tag，不泄露内部结构体名）、`pathID` 路径参数解析、`InitValidator()`（由 `router.Setup` 调一次）
- `system.go`：系统端点 —— `/livez`（只看进程活着）、`/readyz`（真探下游，不健康返 503）、404 / 405 统一成 JSON 而不是 gin 默认的纯文本。它们不属于任何业务模块也不经过 service，所以单独一个文件
- `user.go` / `order.go`：业务接口，每个模块一个文件

#### 业务核心

**`internal/service/`** — 业务规则、事务边界、错误映射，全部是**包级函数**。

```go
// 没有 interface、没有 struct、没有构造函数，controller 直接调
func CreateOrder(ctx context.Context, userID uint64, req *model.CreateOrderRequest) (*model.OrderResponse, error) {
    ...
    err := data.CreateOrder(ctx, order)          // 不碰 gorm
    if data.IsDuplicate(err) { ... }             // 数据层错误 → 业务错误
}
```

- `user.go`：用户业务（注册、登录、资料更新、列表）
- `order.go`：创建重试、状态流转校验 + 流水、归属校验、删除限制

**`internal/data/`** — 数据访问层，也全部是包级函数。这是**唯一允许出现 SQL 与 gorm 调用的地方**。

```go
// 需要连接就调 connDb(ctx) —— 事务中自动复用事务句柄，
// 所以同一个函数在事务内外都能用，不需要写第二套 XxxWithTx
func UpdateOrderStatus(ctx context.Context, id uint64, from, to int8) (int64, error) {
    res := connDb(ctx).Model(&model.Order{}).
        Where("id = ? AND status = ?", id, from).
        Update("status", to)
    return res.RowsAffected, res.Error
}
```

三条约定写在包注释里：没有 interface 与构造函数；不认识业务错误（只返回 gorm 原始错误，service 用 `data.IsNotFound` / `data.IsDuplicate` 判定后翻译成 `errcode`）；不做业务判断（归属校验、状态流转、分页上限都属于 service）。

**为什么不用「每张表一个 interface + 实现 + 构造函数」的传统 Repository**：那套样板的收益是换实现和 mock 注入，本项目两者都不需要（测试走真库）。包级函数保住了「复杂 SQL 有地方放、业务层看不见 ORM」这两个真收益，去掉了接口声明与装配。将来真要拆多数据源，再给具体函数加分支就行。

需要「同时写两张表且要么都成功」时在 service 用 `transaction.Do(ctx, fn)` 包住，`data` 层的 `connDb(ctx)` 会自动认领 ctx 里的事务句柄，所以事务内外的 data 函数写法完全一样。

**`internal/model/`** — 结构体定义，无行为逻辑。每个模型文件包含三类：数据库实体（`User`）、请求体（`UserRegisterRequest`，带 validator tag）、响应体（`UserResponse`，通过 `ToResponse()` 转换）。**请求/响应与实体分离**是为了不把 `password` 这类字段意外序列化给客户端。

响应体还按「谁在看」分了两个：`UserResponse` 含 email/phone/status，只用于本人视角（`/users/profile`）；`UserPublicResponse` 只有 id/username/avatar/created_at，用于列表和查他人 —— 那两个接口只校验登录，用同一个响应体等于让任何注册用户批量导出全库 PII。

`common.go` 除了 `PageRequest` 还放着 `NormalizePage` —— **全项目唯一一份分页归一化逻辑**。controller 用它回显实际生效的分页，service 用它兜底（绕过 HTTP 层直接调 service 时 binding 的 `max=100` 不生效）。此前 controller 侧和 service 侧各写了一份，改上限只改一边就会出现「回显 100 实际查 1000」这种错位。

#### 基础设施 `pkg/`

判断标准：**换个项目也能直接拷走用的，才放这里**。所有包都不 import `internal/`，需要参数的一律定义自己的 `Options` 结构体，由 `bootstrap` 负责翻译。

| 包 | 核心职责 | 关键设计 |
|----|---------|---------|
| `auth/` | 认证原语 | `jwt.go` 显式限定签名算法与签发者（防算法混淆攻击）；`password.go` 是 bcrypt 封装 |
| `database/` | MySQL 连接池 + GORM 接入 | SQL 日志走应用 logger 同格式；开 `TranslateError` 才能识别唯一键冲突；日志级别与慢查询阈值可配（生产不打印 SQL 参数） |
| `cache/` | Redis 客户端 | 只保留 Get/Set/Del + `Client()` 逃生口，不做无意义的命令透传 |
| `logger/` | 基于标准库 `log/slog` | `logger.C(ctx)` 自动带上 request_id；日志按小时轮转（app_YYYYMMDDHH.log），保留天数/份数可配 |
| `response/` | 统一响应封装 | 生产环境不外泄错误细节（`SetExposeDetails`） |
| `errcode/` | 错误码体系 | 支持 `Unwrap`/`Is`，可被 `%w` 包装后仍判定类型；`WithCause` 留底层错误进日志但不返给客户端 |
| `health/` | 依赖健康检查注册表 | 探测结果缓存 2 秒（`/readyz` 无认证，不缓存会被当放大器压 DB）；带摘流状态；一批探测有整体超时且同一时刻只跑一批（不理 ctx 的 checker 挂死时不会持续堆 goroutine）|
| `metrics/` | Prometheus 指标 | route 标签用**路由模板**而不是真实路径，避免标签基数爆炸；采集 DB 连接池等待数 |
| `admin/` | 内部管理端口 | `/metrics`、`/debug/pprof`、`/version` 挂在**独立端口且默认只监听回环** —— 这些端点会暴露路由清单与堆信息，不该挂业务端口 |
| `transaction/` | 事务边界 | 事务句柄放 ctx，`data` 层的 `connDb(ctx)` 自动认领；支持嵌套复用（SavePoint 语义）；写入口不导出（外部无法用普通 `*gorm.DB` 冒充事务），句柄在 `Do` 返回后失效 |
| `safego/` | 带 panic 保护的 goroutine | 裸 `go func()` 里的 panic 无法被中间件 recover，会直接崩进程 |
| `buildinfo/` | 编译期注入的版本信息 | 由 Makefile 通过 `-ldflags` 写入 |

#### 辅助目录

- **`test/`** — 真库集成测试：`setup_test.go` 在 `TestMain` 里连库、AutoMigrate、`resource.Set` 并建好 engine，`user_api_test.go` / `order_api_test.go` / `tx_test.go` 走完整链路打真实数据库，`framework_test.go` 与 `layering_test.go` 不依赖 DB（前者验框架行为，后者扫 import 表守分层边界）。连不上库时 DB 相关用例会显式 skip 并打印如何起库。单包内的测试放在各自包里（`internal/config/config_test.go`、`pkg/transaction/transaction_test.go` 等）。
- **`docs/swagger/`** — `make swagger` 生成的 API 文档产物。
- **`scripts/`** — `build.sh` / `deploy.sh`。
- **`logs/`** — 运行期日志输出，内容已 gitignore，只保留 `.gitkeep`。

### 一个请求怎么流过这些目录

以 `POST /api/v1/orders`（需登录）为例：

```
1. cmd/server/main.go          进程已启动，bootstrap 装配好的 engine 正在监听
2. internal/middleware/        requestid → metrics → bodylimit → logger
                              → recovery → secure → cors → ratelimit → timeout
                              （探针路由注册在 ratelimit 之前，不受限流与超时约束）
3. internal/middleware/auth.go Auth() 解析 Bearer token，把 userID 放进 ctx
4. internal/router/router.go   registerOrder 里声明的路由，匹配到 controller.CreateOrder
5. internal/controller/order.go RequireUserID 取身份 + bindJSON 绑定校验，失败直接 400/401/413
6. internal/service/order.go   业务规则校验；多次写入用 transaction.Do 包住
7. internal/data/order.go      执行 SQL；connDb(ctx) 复用事务句柄
8. internal/model/order.go     实体 → ToResponse() 转成响应体（不含内部字段）
9. pkg/response                统一包装成 {code, message, data}；错误经 errcode 映射 HTTP 状态码
```

排查问题时，用 `X-Request-ID` 在日志里就能串起第 2 步到第 9 步的全部记录。

### 新增一个业务模块要改哪些文件

以加一个 `product` 模块为例：

1. `internal/model/product.go` — 实体 + `TableName()` + 请求/响应结构体 + `ToResponse()`
2. `internal/data/product.go` — 数据访问函数（包级函数，`connDb(ctx)` 取连接写 SQL）
3. `internal/service/product.go` — 业务函数（包级函数，调 `data.Xxx`，把 gorm 错误翻成 `errcode`）
4. `internal/controller/product.go` — 接口函数（包级函数，绑参 → 调 service → 写响应）
5. `internal/router/router.go` — 加一个 `registerProduct(g, auth)` 并在 `registerAPIRoutes` 里调一次
6. `cmd/migrate/main.go` — 把 `&model.Product{}` 加进 `models` 列表
7. `pkg/errcode/errcode.go` — 如需新错误码，按段位追加

**4 个新文件 + 2 处登记点**（路由、迁移清单），全程不需要动 `cmd/server/main.go`、`internal/bootstrap/`、`internal/resource/`。

在已有模块上加一个接口：data 加一个函数、service 加一个函数、controller 加一个函数、router 加一行，没有接口声明要同步，也没有 mock 要更新。

## 技术栈

| 类别 | 技术 | 版本 |
|------|------|------|
| 语言 | Go | 1.25+ |
| Web 框架 | [Gin](https://github.com/gin-gonic/gin) | 1.9.1 |
| ORM | [GORM](https://gorm.io/) | 1.25.5 |
| 配置管理 | [Viper](https://github.com/spf13/viper) | 1.18.2 |
| JWT | [golang-jwt](https://github.com/golang-jwt/jwt) | 5.3.1 |
| 指标 | [prometheus/client_golang](https://github.com/prometheus/client_golang) | 1.24.1 |
| 数据库 | MySQL | 8.0+ |
| 缓存 | Redis | 6.0+ |

## 快速开始

### 环境要求

- Go 1.25+（`go.mod` 的 go 指令即为下限，Dockerfile / CI 必须与之一致）
- MySQL 8.0+
- Redis 6.0+（可选）

也可以用 `make up` 一键起本地 MySQL + Redis（见 `docker-compose.yml`），不必在本机装。

### 1. 克隆项目

```bash
git clone <repository-url>
cd myproject
```

### 2. 安装依赖

```bash
make deps
# 或
go mod download
```

### 3. 配置数据库

先起依赖（可选，已有本地 MySQL/Redis 可跳过）：

```bash
make up      # docker compose up -d，起 MySQL + Redis
make down    # 用完停掉
```

**不要把凭据写进 `configs/config.yaml`（该文件入库）**。两种方式：

方式一，本机开发写进 `configs/config.local.yaml`（已被 `.gitignore` 忽略，优先级高于环境配置）：

```yaml
database:
  host: "127.0.0.1"
  username: "root"
  password: "your-password"
  dbname: "gin"
jwt:
  secret: "local-dev-only-secret-at-least-32-chars"
```

方式二，用环境变量注入（推荐用于测试/生产），命名规则为 `APP_` + 配置路径大写、`.` 换成 `_`：

```bash
export APP_DATABASE_PASSWORD='your-password'
export APP_REDIS_PASSWORD='your-redis-password'
export APP_JWT_SECRET='at-least-32-chars-random-string'
```

`jwt.secret` 为空、或仍是占位符、或生产环境长度不足 32 位时，**启动会直接失败**并打印原因。

### 4. 创建数据库表

```bash
# 先建库
mysql -e "CREATE DATABASE IF NOT EXISTS gin DEFAULT CHARACTER SET utf8mb4;"

# 再迁移表结构（基于 GORM AutoMigrate）
make migrate ENV=dev
```

也可手工建表：

```sql
-- 用户表
CREATE TABLE users (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(50) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,
    email VARCHAR(100) UNIQUE,
    phone VARCHAR(20),
    avatar VARCHAR(255),
    status TINYINT DEFAULT 1 COMMENT '1-正常 0-禁用',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

-- 订单表（金额用 BIGINT 存「分」，不用 DECIMAL/FLOAT 走浮点）
CREATE TABLE orders (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    order_no VARCHAR(64) NOT NULL UNIQUE,
    user_id BIGINT UNSIGNED NOT NULL,
    total_amount_cents BIGINT NOT NULL COMMENT '金额，单位：分',
    status TINYINT DEFAULT 0 COMMENT '0-待支付 1-已支付 2-已发货 3-已完成 4-已取消',
    remark VARCHAR(255),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_user_id (user_id)
);

-- 订单状态流水表（状态变更审计，改状态和写流水在同一个事务里）
CREATE TABLE order_status_logs (
    id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    order_id BIGINT UNSIGNED NOT NULL,
    from_status TINYINT NOT NULL,
    to_status TINYINT NOT NULL,
    operator_id BIGINT UNSIGNED NOT NULL COMMENT '操作人 user_id',
    created_at DATETIME(3),
    INDEX idx_order_status_logs_order_id (order_id)
);
```

> 若已有旧的 `total_amount DECIMAL(10,2)` 数据，迁移方式：
> `ALTER TABLE orders ADD COLUMN total_amount_cents BIGINT NOT NULL DEFAULT 0;`
> `UPDATE orders SET total_amount_cents = ROUND(total_amount * 100);`
> 确认无误后再删除旧列。`AutoMigrate` 不会做这类数据搬迁。

### 5. 运行项目

```bash
# 开发模式
make run

# 或直接运行
go run cmd/server/main.go

# 指定环境
go run cmd/server/main.go -env=prod
```

### 6. 访问测试

```bash
# 存活探针（不探测依赖，恒为 200，回显版本号）
curl http://localhost:8080/livez

# 就绪探针（DB/Redis 不可用时返回 503）
curl -i http://localhost:8080/readyz

# 用户注册
curl -X POST http://localhost:8080/api/v1/users/register \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser","password":"test123456","email":"test@example.com"}'

# 用户登录（返回 token 与 expires_in）
curl -X POST http://localhost:8080/api/v1/users/login \
  -H "Content-Type: application/json" \
  -d '{"username":"testuser","password":"test123456"}'
```

## 配置说明

### 配置文件结构

```yaml
# configs/config.yaml —— 入库文件，不写任何真实凭据

server:
  addr: ":8080"                # 监听地址
  mode: "debug"                # debug / release / test（会传给 gin.SetMode）
  read_timeout: 10             # 读超时（秒）
  write_timeout: 10            # 写超时（秒）
  idle_timeout: 60             # keep-alive 空闲超时（秒）
  read_header_timeout: 5       # 读 header 超时（秒），防慢连接攻击
  request_timeout: 10          # 单请求 ctx 超时（秒），0 表示不限制
  shutdown_timeout: 15         # 优雅退出等待（秒）
  max_header_bytes: 1048576
  max_body_bytes: 1048576      # 请求体上限（字节），超限返回 413；0 表示不限制
  trusted_proxies: []          # 可信代理网段，留空表示只信任 RemoteAddr
  drain_delay: 5               # SIGTERM 后先让 /readyz 返回 503 并等待这么久，给 LB 摘流
  enable_hsts: false           # 仅 HTTPS 有意义，证书没配好时开启会导致全站不可用

admin:                         # 内部管理端口
  addr: "127.0.0.1:9090"       # 留空则不启动；/metrics、/debug/pprof、/version
  pprof: true                  # 生产建议 false，需要排查时临时开

database:
  driver: "mysql"
  host: "127.0.0.1"
  port: 3306
  username: "root"
  password: ""                 # 由 APP_DATABASE_PASSWORD / config.local.yaml 注入
  dbname: "gin"
  max_idle_conns: 10
  max_open_conns: 100
  conn_max_lifetime: 60        # 连接最大存活（分钟）
  log_level: "warn"            # silent/error/warn/info，生产勿用 info（会打印全量 SQL）
  slow_threshold: 200          # 慢查询阈值（毫秒），超过按 warn 级别记录
  log_sql_params: false        # 是否把 SQL 绑定参数打进日志；与 log_level 解耦，生产禁止开启

redis:
  enabled: true                # 关闭后不建连、健康检查不含 Redis
  host: "127.0.0.1"
  port: 6379
  password: ""
  db: 0

log:
  level: "info"                # debug / info / warn / error
  format: "json"               # json / console
  file_path: "./logs"          # 为空则仅输出 stdout
  log_body: false              # 记录请求/响应体（有内存开销，排查时才开）
  add_source: false            # 记录调用位置
  max_backups: 14              # 保留的历史文件数
  max_age_days: 30             # 历史文件保留天数

jwt:
  secret: ""                   # 必填；生产要求 >= 32 位，否则启动失败
  expire_time: 24              # 小时
  issuer: "myproject"

cors:
  allow_origins: ["*"]         # 生产改为显式白名单
  allow_credentials: false     # 与 "*" 互斥（浏览器限制）
  max_age: 12                  # 小时

rate_limit:
  enabled: true                # 单机令牌桶，按客户端 IP
  rps: 50
  burst: 100
  auth_rps: 1                  # 注册/登录单独配额（bcrypt 是 CPU 密集操作）
  auth_burst: 5
```

### 两个容易被忽略的安全配置

**`trusted_proxies`**：gin 默认信任所有代理，`ClientIP()` 会取 `X-Forwarded-For` 首段。留空（默认）表示只信任 `RemoteAddr`；部署在 LB/网关后面时必须填其网段，否则按 IP 的限流可被伪造 header 绕过，日志里的来源 IP 也不可信。

**`max_body_bytes`**：`max_header_bytes` 只约束 header。body 不设限时，一个大 JSON 就能把进程内存打满（绑定会把整个 body 读进内存）。超限返回 413（错误码 10010）。

**`admin.addr`**：`/metrics` 会暴露路由清单与流量特征，`/debug/pprof` 能拉堆和 CPU profile 且可被反复触发当成 DoS。因此默认只监听 `127.0.0.1`。需要被 Prometheus 跨机抓取时改成 `0.0.0.0:9090`，并用安全组限制来源——不要靠在公网端口上加 token 了事。

### 配置优先级

从低到高，后者覆盖前者：

1. `config.go` 中 `setDefaults` 的内置默认值
2. `configs/config.yaml`
3. `configs/config.<env>.yaml`（不存在则跳过）
4. `configs/config.local.yaml`（本机覆盖，不入库）
5. 环境变量 `APP_*`

环境变量命名：配置路径大写、`.` 换 `_`、加 `APP_` 前缀。例如 `database.password` → `APP_DATABASE_PASSWORD`。

> 实现注意：viper 的 `AutomaticEnv` 对「嵌套 key + Unmarshal」不生效，必须对每个 key 显式 `BindEnv`，而 `AllKeys()` 只包含有默认值或出现在配置文件里的 key。因此 `config.go` 里用 `envOnlyKeys` 登记了那些「刻意不写进入库配置文件」的敏感项（数据库/Redis 密码、JWT secret 等）并给了空默认值——不登记的话，环境变量注入会被**静默忽略**。`internal/config/config_test.go` 有用例守着这条。

### 启动校验

`config.Validate()` 在启动时拦截以下情况，直接退出并说明原因：

- `server.mode` 非 debug/release/test
- 数据库 host/dbname/username 缺失
- `jwt.secret` 为空或仍是占位符（如 `your-secret-key-here`）
- `server.trusted_proxies` 含非法的 IP / CIDR
- 生产环境：`jwt.secret` 短于 32 位、数据库密码为空、CORS 同时使用 `*` 与 `allow_credentials`

### 多环境配置

- `config.yaml` - 默认配置
- `config.dev.yaml` - 开发环境
- `config.test.yaml` - 测试环境
- `config.prod.yaml` - 生产环境
- `config.local.yaml` - 本机凭据（不入库）

通过命令行参数或环境变量指定环境：

```bash
# 命令行参数
go run ./cmd/server -env=prod

# 环境变量
APP_ENV=prod go run ./cmd/server

# 指定配置目录
go run ./cmd/server -env=prod -config=/etc/myproject/config
```

## 分层架构

### 架构图

```
┌──────────────────────────────────────────────────────────────┐
│                        HTTP Request                          │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                         Router                               │
│                      (路由分发)                               │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                       Middleware                             │
│   RequestID → Metrics → BodyLimit → Logger → Recovery        │
│              → SecurityHeaders → CORS                       │
│              →（探针路由在此注册，绕开限流与超时）              │
│              → RateLimit → Timeout                          │
│              → Auth（仅受保护路由组）                          │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                       Controller                             │
│          • 参数校验和绑定                                      │
│          • 调用 Service 层                                    │
│          • 统一响应格式                                        │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                        Service                               │
│          • 业务规则校验                                        │
│          • 事务边界（transaction.Do）                          │
│          • 数据层错误 → errcode                                │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                         Data                                 │
│          • 唯一写 SQL 的地方（包级函数，无 interface）           │
│          • connDb(ctx) 自动复用 ctx 里的事务句柄                  │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                    Database / Cache                          │
│                   (MySQL / Redis)                            │
│        由 internal/resource 持有的全局单例统一提供              │
└──────────────────────────────────────────────────────────────┘
```

### 各层职责

| 层级 | 目录 | 职责 |
|------|------|------|
| **Router** | `internal/router/` | 中间件顺序、探针、路由表；新增接口的唯一登记点 |
| **Controller** | `internal/controller/` | 包级函数：参数校验、取 `user_id`、调 Service、统一响应；不持有 DB/Redis |
| **Service** | `internal/service/` | 包级函数：业务规则、事务边界、错误映射、分页上限；不写 SQL |
| **Data** | `internal/data/` | 包级函数：唯一写 SQL 的地方，`connDb(ctx)` 自动感知事务；只返回 gorm 原始错误 |
| **Resource** | `internal/resource/` | 全局资源容器：`DB(ctx)` / `JWT()` / `Redis()` / `Cfg()`，由 bootstrap 一次性 `Set` |
| **Model** | `internal/model/` | 数据模型定义、请求/响应结构体 |
| **Middleware** | `internal/middleware/` | 请求 ID、panic 恢复、指标、日志、安全头、跨域、限流、请求体上限、超时、认证 |

### 装配流程

只有一个装配点，没有构造函数链、没有 Deps 结构体、没有业务 interface：

```go
// internal/bootstrap/bootstrap.go
app.DB, _ = database.NewMySQL(DBOptions(cfg.Database))    // 1. 基础设施
app.Redis, _ = cache.NewRedis(...)

// 2. 一次性把资源交给全局容器（同时初始化 transaction 的 root DB）
resource.Set(cfg, app.DB, app.Redis, auth.NewJWTManager(cfg.JWT.Secret, expire, cfg.JWT.Issuer))

health.RegisterFunc("database", pingDB)                   // 3. 健康检查
engine, err := router.Setup(cfg)                          // 4. 中间件 + 路由表
```

Controller / Service / Data 都是包级函数，`data` 需要连接时自己去 `resource.DB(ctx)` 拿。代价是拿不到 mock 注入点 —— 这也是走真库集成测试的原因（见[测试](#测试)）；收益是新增一个接口不需要碰任何装配代码。分层没有因此消失，只是层与层之间用包级函数调用而不是接口 + 注入，边界由 `test/layering_test.go` 强制。

同理不需要 [google/wire](https://github.com/google/wire) 之类的代码生成：已经没有装配链可生成了。

### 事务用法

判断标准只有一条：**一个业务动作是否对应多次写入**。单条 INSERT/UPDATE 本身就是原子的，GORM 默认还会替它套一层事务，再包一次 `transaction.Do` 只是多一次 BEGIN/COMMIT 往返 —— 所以 `service.CreateOrder` 里没有事务。

真实用例见 `internal/service/order.go` 的 `UpdateOrderStatus`：改 `orders.status` 和往 `order_status_logs` 追加流水必须同生同死，否则要么查不出「谁改的」，要么留下一条与事实不符的假记录。

```go
return transaction.Do(ctx, func(ctx context.Context) error {
	// 带原状态做条件更新，影响 0 行说明状态已被并发请求改掉
	affected, err := data.UpdateOrderStatus(ctx, id, order.Status, req.Status)
	if err != nil {
		return err
	}
	if affected == 0 {
		return errcode.ErrInvalidOrderStatus.WithDetails("订单状态已被其他操作变更，请重新查询后重试")
	}

	return data.CreateOrderStatusLog(ctx, &model.OrderStatusLog{
		OrderID: id, FromStatus: order.Status, ToStatus: req.Status, OperatorID: userID,
	})
})
```

`Do` 把事务句柄放进 ctx，`data` 层的 `connDb(ctx)` 自动认领 —— 所以同一个 data 函数在事务内外都能用，不必写第二套 `XxxWithTx`。闭包返回任何 error 都整体回滚；嵌套调用 `Do` 会复用外层事务（SavePoint 语义）。

一条约束：**闭包收到的 ctx 不能逃出闭包**。`Do` 返回时会把 ctx 里的句柄置为失效，此后 `TxFrom` 一律返回 false —— 因为事务早已 Commit/Rollback，把 ctx 交给后台 goroutine 再拿它写库就是在用一个已结束的 `*sql.Tx`。另外没有导出 `WithTx`：写入口一旦公开，任何代码都能把普通 `*gorm.DB` 冒充成事务句柄塞进 ctx，`resource.DB(ctx)` 会当事务用而实际每条语句自动提交。事务的唯一入口是 `transaction.Do`。

### 日志用法

请求入口已把 `request_id`（以及认证后的 `user_id`）绑定到 ctx，业务层直接用：

```go
logger.C(ctx).Info("order created", "order_no", order.OrderNo, "amount", order.TotalAmount)
```

## 可观测性

日志、指标、探针三者共用同一套 `request_id`，出问题时可以从告警 → 指标 → 日志逐层下钻。

### 内部管理端口

`/metrics`、`/debug/pprof`、`/version` 挂在**独立端口**（默认 `127.0.0.1:9090`），不在业务端口上。原因：前者暴露路由清单与流量特征，后者能拉取堆和 CPU profile，既泄露实现细节，又能被反复触发当成 DoS。

```bash
curl 127.0.0.1:9090/metrics | head -20
curl 127.0.0.1:9090/version
go tool pprof http://127.0.0.1:9090/debug/pprof/heap          # 需 admin.pprof: true
go tool pprof http://127.0.0.1:9090/debug/pprof/profile?seconds=30
```

### 指标清单

只暴露 RED 三要素加少量基础设施指标，不追求大而全：

- `http_requests_total{method,route,status}` —— 请求量与错误率
- `http_request_duration_seconds{method,route}` —— 延迟分布，可算 P95/P99
- `http_requests_in_flight` —— 在途请求数，突增说明下游变慢或出现堆积
- `http_response_size_bytes{route}` —— 发现意外的大响应
- `ratelimit_rejected_total{scope}` —— 限流拒绝数，`scope` 区分 global / auth
- `panics_recovered_total{source}` —— 应长期为 0，一旦不为 0 就该告警
- `db_pool_*` —— 连接池状态。`db_pool_wait_count_total` 持续增长是「服务变慢但看不出原因」最常见的信号
- Go 运行时与进程指标（goroutine 数、GC、内存、FD、CPU）

**`route` 标签用的是路由模板（`/api/v1/users/:id`）而不是真实路径。** 用真实路径会让每个 ID 产生一条独立时间序列，指标基数无上限增长，先撑爆 Prometheus 再撑爆自己的内存。未匹配的路径统一归到 `unmatched`，否则扫描器乱打的路径同样会炸标签。`test/observability_test.go` 有用例守着这条约束。

### Prometheus 抓取配置

```yaml
scrape_configs:
  - job_name: myproject
    metrics_path: /metrics
    static_configs:
      - targets: ["10.0.0.10:9090"] # 需先把 admin.addr 放开到内网地址
```

常用查询：

```promql
sum(rate(http_requests_total[1m]))                                        # QPS
sum(rate(http_requests_total{status=~"5.."}[1m])) / sum(rate(http_requests_total[1m]))  # 错误率
histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket[5m])) by (le, route))  # P99
```

## 优雅退出与摘流

收到 SIGTERM 后的顺序是：**先摘流，再关闭**。

1. `/readyz` 立刻返回 503（`status: "draining"`），负载均衡在下一次探测时把本实例摘掉
2. 等待 `server.drain_delay`（默认 5 秒，生产 10 秒），让在途请求打完、LB 完成摘流
3. `Shutdown` 停止接收新连接，等存量请求处理完（上限 `server.shutdown_timeout`）
4. 逆序释放 Redis、DB 连接

少了第 1、2 步，SIGTERM 之后 LB 仍会在下一次探测前继续转发流量，而服务已经拒绝新连接——表现为每次发布都有一小批 502。`drain_delay` 建议设为探测间隔的两倍以上。

`/readyz` 的响应区分了两种不健康：`draining`（正在退出，属于预期）和 `unavailable`（依赖故障，需要告警），不要混在一起报警。

## API 接口

### 公开接口

业务端口（默认 `:8080`）：

| 方法 | 路径 | 描述 | 请求体 |
|------|------|------|--------|
| GET | `/livez` | 存活探针（不探测依赖，回显版本号） | - |
| GET | `/readyz` | 就绪探针（依赖异常或摘流中返回 503，结果缓存 2 秒） | - |
| GET | `/health` | 同 `/readyz`，兼容旧路径 | - |
| POST | `/api/v1/users/register` | 用户注册（独立限流配额） | `UserRegisterRequest` |
| POST | `/api/v1/users/login` | 用户登录（独立限流配额） | `UserLoginRequest` |

内部端口（默认 `127.0.0.1:9090`，不对外暴露）：

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/metrics` | Prometheus 指标 |
| GET | `/version` | 版本与 Go 版本 |
| GET | `/debug/pprof/*` | 性能剖析（需 `admin.pprof: true`） |

### 需要认证的接口

请求头添加：`Authorization: Bearer <token>`

#### 用户接口

| 方法 | 路径 | 描述 | 请求体/参数 |
|------|------|------|-------------|
| GET | `/api/v1/users/profile` | 获取当前用户信息 | - |
| PUT | `/api/v1/users/profile` | 更新当前用户信息 | `UserUpdateRequest` |
| GET | `/api/v1/users` | 获取用户列表（仅公开字段：id/username/avatar/created_at） | `?page=1&page_size=10`（page 上限 10000，page_size 上限 100） |
| GET | `/api/v1/users/:id` | 获取指定用户的公开信息（不含 email/phone/status） | - |
| DELETE | `/api/v1/users/:id` | 删除用户（**仅限本人**） | - |

#### 订单接口

订单接口均带归属校验，只能访问自己的订单。

| 方法 | 路径 | 描述 | 请求体/参数 |
|------|------|------|-------------|
| POST | `/api/v1/orders` | 创建订单 | `CreateOrderRequest` |
| GET | `/api/v1/orders` | 获取订单列表 | `?page=1&page_size=10&status=0` |
| GET | `/api/v1/orders/:id` | 获取订单详情 | - |
| PUT | `/api/v1/orders/:id/status` | 更新订单状态 | `UpdateOrderStatusRequest` |
| DELETE | `/api/v1/orders/:id` | 删除订单 | - |

### 响应格式

所有响应都带 `request_id`，与日志中的 `request_id` 一致，可直接用于排查。
响应头也会返回 `X-Request-ID`（若上游已带该头则透传）。

#### 成功响应

```json
{
  "code": 0,
  "message": "success",
  "data": { },
  "request_id": "8f1c2d3e4a5b6c7d8e9f0a1b"
}
```

#### 列表响应

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "list": [],
    "total": 100,
    "page": 1,
    "page_size": 10
  },
  "request_id": "8f1c2d3e4a5b6c7d8e9f0a1b"
}
```

#### 错误响应

`details` 的暴露规则：4xx 描述的是调用方自己的输入（哪个字段不合法），生产也会返回；5xx 可能含内部实现细节，仅非生产环境返回。校验失败的信息已翻成中文，字段名用 json tag：

```json
{
  "code": 10002,
  "message": "参数错误",
  "details": "email 必须是合法的邮箱地址",
  "request_id": "8f1c2d3e4a5b6c7d8e9f0a1b"
}
```

## 数据模型

### 用户模型 (User)

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 用户ID |
| username | string | 用户名（唯一） |
| password | string | 密码（加密存储） |
| email | string | 邮箱（唯一） |
| phone | string | 手机号 |
| avatar | string | 头像URL |
| status | int8 | 状态：1-正常，0-禁用 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

### 订单模型 (Order)

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 订单ID |
| order_no | string | 订单号（唯一） |
| user_id | uint64 | 用户ID |
| total_amount_cents | int64 | 订单金额，单位：分 |
| status | int8 | 状态（见下表） |
| remark | string | 备注 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

金额一律用 `int64` 存「分」。`float64` 无法精确表示 0.1 这类十进制小数，一旦出现累加、折扣、对账，误差必然出现且无法追溯。响应里同时给出 `total_amount_cents`（用于计算）和 `total_amount_text`（用于展示，如 `"19.99"`），两边都不碰浮点。

**订单状态流转：**

```
待支付(0) ──→ 已支付(1) ──→ 已发货(2) ──→ 已完成(3)
    │              │
    └──→ 已取消(4) ←┘
```

## 错误码

### 通用错误 (10xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 0 | 200 | 成功 |
| 10001 | 500 | 内部错误 |
| 10002 | 400 | 参数错误 |
| 10003 | 404 | 资源不存在 |
| 10004 | 401 | 未授权 |
| 10005 | 403 | 禁止访问 |
| 10006 | 429 | 请求过于频繁 |
| 10007 | 504 | 请求处理超时（`context.DeadlineExceeded`） |
| 10008 | 405 | 方法不允许 |
| 10009 | 499 | 客户端已断开（`context.Canceled`，仅用于日志/监控归类） |
| 10010 | 413 | 请求体过大 |

`errcode.From` 会把 `context.DeadlineExceeded` / `context.Canceled` 分别映射到 10007 / 10009。不做这层映射的话，超时会被兜成 500「内部错误」，日志刷 error、告警误报，排查方向被带偏。

### 认证错误 (20xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 20001 | 401 | Token 不存在 |
| 20002 | 401 | Token 无效 |
| 20003 | 401 | Token 已过期 |
| 20004 | 500 | Token 生成失败 |

### 用户错误 (30xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 30001 | 404 | 用户不存在 |
| 30002 | 400 | 用户已存在 |
| 30003 | 400 | 邮箱已被注册 |
| 30004 | 400 | 密码错误 |
| 30005 | 403 | 用户已被禁用 |

### 订单错误 (40xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 40001 | 404 | 订单不存在 |
| 40002 | 409 | 无效的订单状态（状态流转非法或并发冲突） |
| 40003 | 400 | 订单无法删除 |

## 测试

框架不留 mock 注入点，所以业务链路一律**打真实数据库**。理由很直接：mock 出来的 DB 只能验证「我调了这个方法」，验不了唯一键冲突、条件更新的 `RowsAffected`、事务回滚这些真正会出问题的地方 —— 而这些恰好是本框架的核心机制。

测试分两类：

- **免 DB 的框架测试** `test/framework_test.go` —— 405/413、限流、探针绕过限流、panic 记成 500、指标 route 标签是模板、安全头。`test/layering_test.go` 扫 import 表守分层边界（service 不许 import gorm、controller 不许 import data、data 不许 import errcode）。这两个任何环境都能跑。
- **真库集成测试** `test/user_api_test.go`、`order_api_test.go`、`tx_test.go` —— 走完整 HTTP 链路（`httptest` + 真 engine + 真库），用例自己 `t.Cleanup` 清数据。连不上库时会 `t.Skip` 并打印起库命令，不会静默通过。

`test/setup_test.go` 的 `TestMain` 负责：加载 `configs/config.dev.yaml` → 关掉限流与 body 日志 → 连库 → `AutoMigrate` 三张表 → `health.Init` → `resource.Set` → `router.Setup` 建出全局 engine。

起本地依赖并跑全量：

```bash
docker compose up -d mysql        # 或 make up（带 Redis）

APP_DATABASE_HOST=127.0.0.1 \
APP_DATABASE_PASSWORD=devpassword \
APP_DATABASE_DBNAME=gin \
go test ./...

docker compose down               # 用完停掉
```

数据库连接参数走 `APP_DATABASE_*` 环境变量覆盖，不需要在仓库里放凭据文件。

## 常用命令

```bash
# 构建
make build              # 编译 server 与 migrate 两个二进制（-trimpath + 注入版本号）
make clean              # 清理构建产物

# 运行
make run                # 运行项目（默认 dev，可用 make run ENV=prod）
make dev                # 热重载运行（需要 air，配置见 .air.toml）

# 本地依赖
make up                 # docker compose 起 MySQL + Redis
make down               # 停掉本地依赖

# 数据库
make migrate            # 执行 AutoMigrate（make migrate ENV=dev）

# 测试
make test               # 运行测试
make test-race          # 竞态检测（并发相关改动必跑）
make test-cover         # 测试覆盖率

# 代码质量
make check              # fmt + vet + test，提交前一键检查
make vet                # go vet
make lint               # 代码检查（需要 golangci-lint，配置见 .golangci.yml）
make vuln               # 依赖漏洞扫描（需要 govulncheck）
make fmt                # 代码格式化

# 依赖管理
make deps               # 下载依赖

# 文档
make swagger            # 生成 Swagger 文档（需要 swag）

# Docker
make docker-build       # 构建 Docker 镜像（多阶段、非 root、注入 VERSION）
make docker-run         # 运行 Docker 容器

# 帮助
make help               # 查看所有命令
```

## 版本信息

版本号注入到 `pkg/buildinfo.Version`，而不是 `main.version`：`-ldflags -X` 对不存在的符号会被**静默忽略**，放在独立包里可以被 `server` / `migrate` 共用，也不会因为改动 main 而失效。

```bash
make build                      # 自动取 git describe --tags --always
./build/myproject               # 启动日志里会打印 version 与 go 版本
curl localhost:8080/livez       # {"status":"ok","version":"v1.2.3",...}
curl 127.0.0.1:9090/version     # {"version":"v1.2.3","go":"go1.25.0"}
```

## CI

`.github/workflows/ci.yml` 分四个并行 job：

- **Build & Test** —— `gofmt` 检查、`go mod tidy` 后无 diff、`go vet`、`go build`、`go test -race` 加覆盖率
- **Lint** —— golangci-lint，配置见 `.golangci.yml`（只开高信噪比的检查，不开风格类 linter 刷屏）
- **Vulnerability scan** —— `govulncheck`，只报实际可达调用路径上的 CVE，误报率低
- **Docker build** —— 验证镜像能构建出来，不推送

`GO_VERSION` 必须与 `go.mod` 的 go 指令、Dockerfile 的基础镜像三者一致，否则 CI 过了但镜像构建会失败。

## 日志

基于标准库 `log/slog`，不引入第三方日志依赖。

### 日志配置

```yaml
log:
  level: "debug"        # debug / info / warn / error
  format: "console"     # 输出格式：console 或 json（生产用 json）
  file_path: "./logs"   # 日志目录，为空则只输出到 stdout
  log_body: false       # 是否记录请求体（含脱敏，仅调试环境开启）
  add_source: false     # 是否记录调用位置 file:line
  max_backups: 14       # 保留的历史文件数
  max_age_days: 30      # 历史文件保留天数
```

### 日志文件

日志按小时轮转，文件名形如 `app_YYYYMMDDHH.log`（例如 `app_2026082319.log`），一小时一个文件。历史文件按 `max_backups` / `max_age_days` 清理。只切分不清理的话，磁盘迟早被写满，而磁盘满会连带拖垮数据库和整机。

```
logs/
├── app_2026082319.log          # 当前小时写入
├── app_2026082318.log          # 上一小时
└── app_2026082309.log          # 更早的历史
```

### 日志格式

**Console 格式（slog TextHandler）：**
```
time=2026-08-20T10:30:00.123+08:00 level=INFO msg="http request" method=GET path=/api/v1/users status=200 latency=3.2ms request_id=8f3c1d2e...
```

**JSON 格式（slog JSONHandler）：**
```json
{"time":"2026-08-20T10:30:00.123+08:00","level":"INFO","msg":"http request","method":"GET","path":"/api/v1/users","status":200,"request_id":"8f3c1d2e..."}
```

每个请求由 RequestID 中间件生成/透传 `X-Request-ID`，并绑定到 ctx logger。业务代码中用 `logger.C(ctx)` 取带 `request_id`（登录后还带 `user_id`）的 logger，日志即可按请求串联：

```go
logger.C(ctx).Info("order created", "order_no", order.OrderNo)
```

## 扩展指南

### 添加新的业务模块

1. **定义模型** - `internal/model/xxx.go`（列表请求内嵌 `model.PageRequest`；金额字段用 `int64` 存分；实体上加 `ToResponse()`）
2. **创建 Data** - `internal/data/xxx.go`，包级函数，用 `connDb(ctx)` 取连接写 gorm 查询；只返回 gorm 原始错误，不 import `errcode`；更新用 `Select(白名单).Updates` 而不是 `Save`（`Save` 是全字段覆盖，并发下会丢更新）
3. **创建 Service** - `internal/service/xxx.go`，包级函数，调 `data.Xxx`，用 `data.IsNotFound` / `data.IsDuplicate` 判定后翻译成 `errcode`，跨表写入用 `transaction.Do(ctx, ...)` 包住
4. **创建 Controller** - `internal/controller/xxx.go`，包级函数，用 `bindJSON` / `bindQuery` 绑定参数（自带校验错误中文化与 413 识别），需要身份时开头调 `middleware.RequireUserID(c)`，只做绑定与响应
5. **登记路由** - 在 `internal/router/router.go` 加一个 `registerXxx(v1, auth)` 并在 `registerAPIRoutes` 里调用一次
6. **登记建表** - 在 `cmd/migrate/main.go` 的 models 列表里加上新实体
7. **错误码** - 在 `pkg/errcode/` 按模块段位加新错误码
8. **健康检查** - 若引入了新的外部依赖，在 `internal/bootstrap/bootstrap.go` 中通过 `health.RegisterFunc("xxx", ...)` 注册，`/readyz` 会自动纳入
9. **后台 goroutine** - 一律用 `safego.Go`，裸 `go func` 里的 panic 不会被 Recovery 中间件捕获，会直接终止进程

合计：**4 个新文件（model / data / service / controller）+ 2 个登记点（router、migrate）**，没有接口、没有构造函数、没有装配文件。分层边界由 `test/layering_test.go` 扫 import 表守着。

### 添加新的中间件

在 `internal/middleware/` 创建新文件，然后在 `internal/router/router.go` 的 `useBaseMiddleware` / `useThrottleMiddleware` 里按顺序注册：

```go
r.Use(middleware.YourMiddleware())
```

注意顺序：RequestID 必须最先；Metrics 放在限流之前（被拒绝的请求也要计入 QPS 与错误率）；BodyLimit 必须早于任何读 Body 的中间件；**Recovery 必须在 Metrics/Logger 的内层** —— 放外层的话 panic 请求在指标里会记成 200（Recovery 还没写响应，`c.Writer.Status()` 是 gin 的默认值），基于 5xx 比例的告警永远不响；Timeout 放最后。

探针路由（`/livez`、`/readyz`、`/health`）刻意注册在 RateLimit 之前：gin 的 `Use` 只作用于之后注册的路由，探针一旦和业务流量共用令牌桶，过载时 kubelet 会拿到 429，liveness 判失败就重启容器 —— 恰好在最需要实例的时候把实例杀掉。

新中间件如果要打指标，标签值必须来自有限集合（路由模板、错误码、固定枚举），不要用路径、用户 ID、UA 这类无界值——指标基数一旦炸掉，Prometheus 和自己的进程会一起倒。

后台 goroutine 一律用 `safego.Go(ctx, name, fn)`：中间件里的 `go func` 不在请求链路上，panic 不会被 Recovery 接住，会直接终止进程。

## License

MIT License