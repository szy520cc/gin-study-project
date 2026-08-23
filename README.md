# MyProject

基于 Gin 框架的 Go Web 项目模板，采用清晰的分层架构设计，适用于中小型 Web API 项目快速开发。

## 目录

- [项目结构](#项目结构)
  - [依赖方向](#依赖方向)
  - [每个目录的核心职责](#每个目录的核心职责)
  - [一个请求怎么流过这些目录](#一个请求怎么流过这些目录)
  - [新增一个业务模块要改哪些目录](#新增一个业务模块要改哪些目录)
- [技术栈](#技术栈)
- [快速开始](#快速开始)
- [配置说明](#配置说明)
- [分层架构](#分层架构)
- [可观测性](#可观测性)
- [优雅退出与摘流](#优雅退出与摘流)
- [API 接口](#api-接口)
- [数据模型](#数据模型)
- [错误码](#错误码)
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
│   │   └── config.go
│   │
│   ├── bootstrap/                  # 进程装配与生命周期（main 只调它）
│   │   ├── bootstrap.go            # 按序初始化 DB/Redis/两个 server，产出共享依赖
│   │   └── server.go               # 启停、摘流、优雅关闭
│   │
│   ├── module/                     # 业务模块装配与自注册（新增模块的唯一登记点）
│   │   ├── module.go               # Deps 共享依赖 + Register 签名 + All 模块清单
│   │   ├── user.go                 # user 模块：串起三层 + 声明自己的路由
│   │   └── order.go                # order 模块：同上
│   │
│   ├── handler/                    # HTTP 处理器层（Controller）
│   │   ├── bind.go                 # 参数绑定泛型助手 + 校验错误中文化 + 413 识别
│   │   ├── health.go               # 探针处理器（livez / readyz）
│   │   ├── system.go               # 404 / 405 统一 JSON 响应
│   │   ├── user.go                 # 用户相关接口处理器
│   │   └── order.go                # 订单相关接口处理器
│   │
│   ├── service/                    # 业务逻辑层
│   │   ├── service.go              # 分页归一化等本层公共约束
│   │   ├── user.go                 # 用户业务逻辑（注册、登录、CRUD）
│   │   └── order.go                # 订单业务逻辑（创建、状态流转、归属校验）
│   │
│   ├── repository/                 # 数据访问层（DAO）
│   │   ├── repository.go           # 事务感知基类 + 领域错误转换
│   │   ├── user.go                 # 用户数据访问实现
│   │   └── order.go                # 订单数据访问实现
│   │
│   ├── model/                      # 数据模型定义
│   │   ├── common.go               # 通用分页请求
│   │   ├── user.go                 # 用户模型、请求/响应结构体
│   │   └── order.go                # 订单模型、请求/响应结构体
│   │
│   ├── middleware/                 # HTTP 中间件
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
│       └── router.go               # 引擎构建、可信代理、中间件装配、系统路由 + 遍历模块清单
│
├── pkg/                            # 与业务无关的基础设施，不依赖 internal/
│   ├── auth/                       # 认证原语
│   │   ├── jwt.go                  # JWT 生成与解析
│   │   └── password.go             # 密码哈希与校验（bcrypt）
│   ├── database/mysql.go           # MySQL 连接池 + GORM 日志接入
│   ├── cache/redis.go              # Redis 客户端封装
│   ├── logger/logger.go            # 基于 log/slog 的日志，ctx 贯穿 + 保留策略
│   ├── response/response.go        # 统一响应封装
│   ├── errcode/errcode.go          # 错误码体系（支持 Unwrap/Is）
│   ├── health/health.go            # 依赖健康检查注册表（结果缓存 + 摘流状态）
│   ├── metrics/metrics.go          # Prometheus 指标定义 + DB 连接池采集
│   ├── admin/admin.go              # 内部端口：/metrics、/debug/pprof、/version
│   ├── transaction/transaction.go  # 跨 repository 事务管理器
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
├── test/                           # 测试
│   ├── user_service_test.go        # service 层单测（手写 mock）
│   ├── http_test.go                # 路由/中间件端到端测试
│   ├── hardening_test.go           # 405 / 413 / 限流 / 超时映射 / 探针缓存
│   └── observability_test.go       # 指标标签基数 / 安全头 / 摘流 / admin 端点
│                                   # 另有 internal/config/config_test.go、pkg/logger/rotate_test.go
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
                  │                internal/module      按模块装配三层 + 声明路由
                  │                        │
                  │                        ▼
                  │                internal/handler     HTTP 边界：绑定、校验、写响应
                  │                        │
                  │                        ▼
                  │                internal/service     业务规则、事务边界
                  │                        │
                  │                        ▼
                  │                internal/repository  SQL / ORM 边界
                  │                        │
                  │                        ▼
                  │                internal/model       结构体，谁都能依赖
                  ▼
             pkg/*   基础设施，不依赖 internal/ 任何东西
```

两条能自检的规则：

- `pkg/` 里如果出现 `import "myproject/internal/..."`，就是写错了。基础设施一旦反向依赖业务配置，它就没法被单独复用，`config` 字段改名也会波及到它。所以 `database.NewMySQL` 收的是自己的 `Options`，由 `bootstrap` 负责把 `config` 翻译过去。
- 下层不认识上层。`repository` 不知道 HTTP 存在（它返回 `ErrNotFound`，而不是 404）；`service` 不接触 `*gin.Context`；`handler` 不写 SQL。出现跨层调用（比如 handler 直接调 repository）就说明分层破了。

### 每个目录的核心职责

#### 入口与装配

**`cmd/`** — 每个子目录编译出一个独立二进制，目录名就是产物名。这里只做「决定用哪份配置 + 调用装配 + 决定退出码」，不写业务。`cmd/server/main.go` 全文 77 行，一眼能读完启动顺序；`cmd/migrate/main.go` 用 GORM AutoMigrate 同步表结构，复用 `bootstrap.DBOptions` 拿到同一套连接参数。

**`internal/bootstrap/`** — 进程的装配车间与生命周期管理者，是理解这个框架最该先读的目录。

- `bootstrap.go`：`Init(cfg)` 按依赖顺序创建组件 —— JWT → 数据库（强依赖，失败即退出）→ Redis（可配置关闭）→ repository → service → handler → HTTP Server → admin Server；同时把 DB/Redis 注册进健康检查、把连接池指标注册进 Prometheus。`Close()` 逆序释放。
- `server.go`：`Run()` 并发启动业务端口与 admin 端口（admin 起不来只告警不退出），等 SIGINT/SIGTERM，先 `drain()` 摘流再 `Shutdown()`。

把这些从 main 里搬出来的好处是：**新增一个依赖只改这一个文件，main 永远不变**。

#### 配置

**`configs/`** — 只放 yaml，不放 Go 代码。四层覆盖，优先级从低到高：`config.yaml`（入库，不含任何真实凭据）→ `config.<env>.yaml` → `config.local.yaml`（本机凭据，已 gitignore）→ 环境变量 `APP_*`。

两条与安全相关的加载规则：`-env` 取值被白名单限定为 `dev/test/prod`（拼错直接启动失败，而不是静默按默认值跑）；`config.local.yaml` **只在非生产环境加载** —— 它优先级高于环境配置，一旦随 `configs/` 目录同步到生产机会静默替换生产的库地址与 JWT secret。`config.prod.yaml` 在 `-env=prod` 时必须存在。

**`internal/config/`** — 配置的 Go 侧：结构体定义、加载合并、环境变量绑定、以及**启动即校验**。校验会在启动时直接拒绝「JWT secret 用了占位符」「生产环境 secret 短于 32 字节」「CORS 通配符 + allow_credentials」这类问题，而不是等到线上才暴露。放在 `internal/` 是因为配置结构是应用私有的，外部 module 没有理由引用它。

#### HTTP 边界

**`internal/router/`** — 决定「请求进来先经过什么」。构建 gin 引擎、设置可信代理、按固定顺序装配中间件（顺序有讲究，见下文）、注册系统探针路由，最后遍历 `module.All` 让每个业务模块自己挂路由。**本文件不出现任何业务路径，新增模块不改这里**。

**`internal/module/`** — 「一个业务模块长什么样」的唯一答案。每个模块一个文件，在文件里自己把 repository → service → handler 串起来，并声明自己的路由；`module.go` 里的 `Deps` 是共享依赖（DB、事务管理器、JWT、认证/限流中间件），`All` 是模块清单。

这一层是为了消掉重复登记而存在的：原来新增一个模块要在 `repository.New` / `service.New` / `handler.New` / `routes.go` 四个聚合器里各登记一遍，外加 `router.go` 调一次，共 5 处纯机械改动。现在只需在 `All` 里加一行。代价是多了一个包（总代码量基本没变），换来「读一个文件就知道这个模块怎么装、有哪些路由」。

`All` 刻意是**手写清单**，不用 `init` 自注册。自注册并不省事——同样是每个模块写一行，只是从 `module.go` 搬到模块文件里——但会丢掉「一眼看出系统装了哪些模块」和「注释掉一行就关掉某个模块」这两点灵活性。`init` 自注册是给跨包插件（`database/sql` 驱动那种）解耦用的，这里所有模块同包，本来没有解耦需求。`module_test.go` 守着这张清单：漏登记、路径写错、模块间路径冲突都会在那里暴露。

每个模块导出两个函数：`Xxx(g, d)` 从 `Deps` 自装配（线上用），`XxxWith(g, d, svc)` 接受注入的 service（测试用 stub 起完整 HTTP 栈，不连数据库，且路由表与线上完全一致）。

**`internal/middleware/`** — 横切关注点，每个文件一个独立能力，装配顺序即执行顺序：

| 文件 | 作用 | 为什么在这个位置 |
|------|------|-----------------|
| `requestid.go` | 生成/透传 X-Request-ID，绑进 ctx logger | 最先，后续所有日志都要带它 |
| `metrics.go` | RED 指标采集（收尾在 defer 里） | 放在限流**之前**，被拒的请求也要计入 QPS；用 defer 才能让 panic 请求也计数、in_flight 能归零 |
| `bodylimit.go` | 请求体上限，超限 413 | 必须早于任何读 Body 的中间件，否则 MaxBytesReader 包不到真实 Body |
| `logger.go` | 访问日志，query 与 body 都脱敏，按需记 body | 记日志的那份 body 会截断，但交给 handler 的 Body 始终完整；收尾在 defer 里，panic 请求也留日志 |
| `recovery.go` | panic 恢复 + 堆栈 + 指标 | 在 Metrics/Logger 的**内层**：先写好 500，外层才能观测到真实状态码（放外层会记成 200）|
| `secure.go` | nosniff / DENY / CSP / HSTS 响应头 | —— |
| `cors.go` | 跨域，白名单来自配置 | —— |
| `ratelimit.go` | 单机令牌桶，按 IP，分 global/auth 两档配额；桶数量有上限，清理由请求驱动 | 认证接口单独限流：bcrypt 是 CPU 放大器 |
| `timeout.go` | 单请求 ctx 超时 | 最后，包住真正的业务处理 |

`auth.go` 不在全局链上，它是按路由组挂的：`Auth()` 校验 JWT，`SelfOnly()` 做资源归属校验（防止任何登录用户删除任意账号）。

**`internal/handler/`** — HTTP 与业务的翻译层。职责被刻意限制在四件事：绑参、校验、调 service、写响应。**不写业务规则，不碰数据库**。

- `bind.go`：泛型 `bindJSON[T]` / `bindQuery[T]`，把「重复的 ShouldBind 样板 + 校验错误中文化 + 413 识别」收敛成一处。校验失败返回的字段名用 json tag（对齐 API 契约），不会泄露内部结构体名。`InitValidator()` 由 `router.Setup` 调一次
- `health.go`：`/livez`（只看进程活着）与 `/readyz`（真探下游，不健康返 503）
- `system.go`：404 / 405 统一成 JSON，而不是 gin 默认的纯文本
- `user.go` / `order.go`：业务接口

#### 业务核心

**`internal/service/`** — 业务规则所在地，也是**事务边界的划定者**。它拿到的是纯 Go 类型，看不到 `*gin.Context`，所以可以脱离 HTTP 单独测试。

- `service.go`：本层公共约束 —— 分页参数归一化（`maxPageSize=100`，防止 `pageSize=999999` 打穿数据库）
- `user.go`：注册（查重 → bcrypt → 落库）、登录（校验 → 签发 token）、资料更新
- `order.go`：创建、状态流转校验、归属校验

需要「同时写两张表且要么都成功」时，用注入进来的 `transaction.Manager.Do(ctx, fn)` 包住，repository 会通过 ctx 自动感知事务，业务代码无需接触 `*gorm.DB`。

**`internal/repository/`** — ORM 的边界。向上只暴露领域错误，**让 `service` 不必 import gorm**，将来换 ORM 不影响业务代码。

- `repository.go`：`base.conn(ctx)`（ctx 里有事务句柄就复用，否则用默认连接）+ `wrapErr`（把 `gorm.ErrRecordNotFound` 翻成 `ErrNotFound`、唯一键冲突翻成 `ErrConflict`）
- `user.go` / `order.go`：接口定义 + 实现。注意几处刻意的写法：用 `ExistsByUsername` 走 count 而不是把整行捞出来；`Update` 用 `Select` 白名单而不是 `Save` 全字段覆盖（避免并发丢更新）

**`internal/model/`** — 结构体定义，无行为逻辑。每个模型文件包含三类：数据库实体（`User`）、请求体（`UserRegisterRequest`，带 validator tag）、响应体（`UserResponse`，通过 `ToResponse()` 转换）。**请求/响应与实体分离**是为了不把 `password` 这类字段意外序列化给客户端。

响应体还按「谁在看」分了两个：`UserResponse` 含 email/phone/status，只用于本人视角（`/users/profile`）；`UserPublicResponse` 只有 id/username/avatar/created_at，用于列表和查他人 —— 那两个接口只校验登录，用同一个响应体等于让任何注册用户批量导出全库 PII。

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
| `transaction/` | 跨 repository 事务 | 事务句柄放 ctx，支持嵌套复用（SavePoint 语义）；写入口不导出（外部无法用普通 `*gorm.DB` 冒充事务），句柄在 `Do` 返回后失效 |
| `safego/` | 带 panic 保护的 goroutine | 裸 `go func()` 里的 panic 无法被中间件 recover，会直接崩进程 |
| `buildinfo/` | 编译期注入的版本信息 | 由 Makefile 通过 `-ldflags` 写入 |

#### 辅助目录

- **`test/`** — 跨层测试：`http_test.go` 走完整路由链，`hardening_test.go` 覆盖 405/413/限流/超时映射/探针缓存，`observability_test.go` 覆盖指标基数/安全头/摘流/admin 端点。单包内的测试放在各自包里（`internal/config/config_test.go`、`pkg/logger/rotate_test.go`）。
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
4. internal/module/order.go    路由在这里声明，匹配到 h.CreateOrder
5. internal/handler/order.go   bindJSON 绑定 + validator 校验，失败直接 400/413 返回
6. internal/service/order.go   业务规则校验；需要原子性时 transaction.Do 包住
7. internal/repository/order.go 执行 SQL，gorm 错误经 wrapErr 转成 ErrNotFound/ErrConflict
8. internal/model/order.go     实体 → ToResponse() 转成响应体（不含内部字段）
9. pkg/response                统一包装成 {code, message, data}；错误经 errcode 映射 HTTP 状态码
```

排查问题时，用 `X-Request-ID` 在日志里就能串起第 2 步到第 9 步的全部记录。

### 新增一个业务模块要改哪些目录

以加一个 `product` 模块为例，按顺序：

1. `internal/model/product.go` — 实体 + 请求/响应结构体
2. `internal/repository/product.go` — 接口 + 实现 + `NewProduct(db)`
3. `internal/service/product.go` — 接口 + 实现 + `NewProductService(repo, tx)`
4. `internal/handler/product.go` — 接口处理器 + `NewProductHandler(svc)`
5. `internal/module/product.go` — 把上面三层串起来，并声明本模块的路由
6. `internal/module/module.go` — 在 `All` 里加一行 `Product,`（**唯一的登记点**）
7. `cmd/migrate/main.go` — 把 `&model.Product{}` 加进 `models` 列表
8. `pkg/errcode/errcode.go` — 如需新错误码，按 `50001+` 段位追加

前 6 步是固定套路，第 7、8 步按需。全程不需要动 `cmd/server/main.go`、`internal/bootstrap/`、`internal/router/`。

三层的聚合 struct 已经取消（`repository.go` / `service.go` 只剩本层公共约束），需要维护的模块清单只有 `module.All` 一份。

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
│                        Handler                               │
│          • 参数校验和绑定                                      │
│          • 调用 Service 层                                    │
│          • 统一响应格式                                        │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                        Service                               │
│          • 业务逻辑处理                                        │
│          • 事务管理                                           │
│          • 调用 Repository 层                                 │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                      Repository                              │
│          • 数据库 CRUD 操作                                   │
│          • 数据查询和聚合                                      │
└──────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌──────────────────────────────────────────────────────────────┐
│                    Database / Cache                          │
│                   (MySQL / Redis)                            │
└──────────────────────────────────────────────────────────────┘
```

### 各层职责

| 层级 | 目录 | 职责 |
|------|------|------|
| **Handler** | `internal/handler/` | 接收 HTTP 请求、参数校验、调用 Service、返回响应；不持有 DB/Redis |
| **Module** | `internal/module/` | 按业务模块装配三层并声明路由；新增模块的唯一登记点 |
| **Service** | `internal/service/` | 业务逻辑、事务边界、领域错误映射、分页上限 |
| **Repository** | `internal/repository/` | 数据库操作封装；ORM 边界，向上只暴露领域错误 |
| **Model** | `internal/model/` | 数据模型定义、请求/响应结构体 |
| **Middleware** | `internal/middleware/` | 请求 ID、panic 恢复、指标、日志、安全头、跨域、限流、请求体上限、超时、认证 |

### 依赖注入流程

```go
// internal/bootstrap/bootstrap.go 中的装配流程
jwtManager := auth.NewJWTManager(cfg.JWT.Secret, expire, cfg.JWT.Issuer)
app.DB, _ = database.NewMySQL(DBOptions(cfg.Database))   // 1. 基础设施
app.Health.RegisterFunc("database", pingDB)              //    注册健康检查

// 2. 只组装共享依赖，三层由各模块自己串
deps := module.Deps{DB: app.DB, Tx: transaction.NewManager(app.DB), JWT: jwtManager}

// 3. 路由：中间件 + 系统探针 + 遍历 module.All
engine, err := router.Setup(cfg, app.Health, deps, module.All)
```

```go
// internal/module/order.go —— 一个模块的装配与路由都在这里
func Order(g *gin.RouterGroup, d Deps) {
	OrderWith(g, d, service.NewOrderService(repository.NewOrder(d.DB), d.Tx))
}
```

不用 [google/wire](https://github.com/google/wire) 之类的代码生成：装配链只有三层且形状固定，一行 `service.NewOrderService(repository.NewOrder(d.DB), repository.NewOrderStatusLog(d.DB), d.Tx)` 就说完了，引入 codegen 反而多了一个需要维护的构建步骤。

### 事务用法

判断标准只有一条：**一个业务动作是否对应多次写入**。单条 INSERT/UPDATE 本身就是原子的，GORM 默认还会替它套一层事务，再包一次 `tx.Do` 只是多一次 BEGIN/COMMIT 往返 —— 所以 `orderService.Create` 里没有事务。

真实用例见 `internal/service/order.go` 的 `UpdateStatus`：改 `orders.status` 和往 `order_status_logs` 追加流水必须同生同死，否则要么查不出「谁改的」，要么留下一条与事实不符的假记录。

```go
err = s.tx.Do(ctx, func(ctx context.Context) error {
    // 带原状态做条件更新，状态已被别人改掉时返回 ErrConflict
    if err := s.orderRepo.UpdateStatus(ctx, id, order.Status, req.Status); err != nil {
        return err
    }
    return s.logRepo.Create(ctx, &model.OrderStatusLog{
        OrderID: id, FromStatus: order.Status, ToStatus: req.Status, OperatorID: userID,
    })
})
```

`Do` 把事务句柄放进 ctx，repository 的 `base.conn(ctx)` 自动认领，所以 service 全程不碰 `*gorm.DB`，repository 也不必为事务写第二套方法。闭包返回任何 error（包括上面的 `ErrConflict`）都整体回滚；嵌套调用 `Do` 会复用外层事务（SavePoint 语义）。

一条约束：**闭包收到的 ctx 不能逃出闭包**。`Do` 返回时会把 ctx 里的句柄置为失效，此后 `TxFrom` 一律返回 false —— 因为事务早已 Commit/Rollback，把 ctx 交给后台 goroutine 再拿它写库就是在用一个已结束的 `*sql.Tx`。另外没有导出 `WithTx`：写入口一旦公开，任何代码都能把普通 `*gorm.DB` 冒充成事务句柄塞进 ctx，repository 会当事务用而实际每条语句自动提交。事务的唯一入口是 `Manager.Do`。

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
| 40002 | 400 | 无效的订单状态 |
| 40003 | 400 | 订单无法删除 |

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

1. **定义模型** - `internal/model/xxx.go`（列表请求内嵌 `model.PageRequest`；金额字段用 `int64` 存分）
2. **创建 Repository** - `internal/repository/xxx.go`，嵌入 `base` 并提供 `NewXxx(db)`，用 `r.conn(ctx)` 取连接以自动感知事务，错误经 `wrapErr` 转为 `ErrNotFound`/`ErrConflict`；更新用 `Select(白名单).Updates` 而不是 `Save`（`Save` 是全字段覆盖，并发下会丢更新）
3. **创建 Service** - `internal/service/xxx.go`，把仓储层领域错误翻译成 `errcode`，跨表写入用 `s.tx.Do(ctx, ...)`
4. **创建 Handler** - `internal/handler/xxx.go`，用 `bindJSON` / `bindQuery` 绑定参数（自带校验错误中文化与 413 识别），只做绑定与响应
5. **装配模块** - `internal/module/xxx.go`，写 `Xxx(g, d)` 与 `XxxWith(g, d, svc)`：前者从 `Deps` 串起三层，后者留给测试注入 stub；路由在这里声明
6. **登记模块** - 在 `internal/module/module.go` 的 `All` 里加一行。这是唯一的登记点，三层聚合器已经取消
7. **健康检查** - 若引入了新的外部依赖，在 `internal/bootstrap/bootstrap.go` 中通过 `app.Health.RegisterFunc("xxx", ...)` 注册，`/readyz` 会自动纳入
8. **后台 goroutine** - 一律用 `safego.Go`，裸 `go func` 里的 panic 不会被 Recovery 中间件捕获，会直接终止进程

### 添加新的中间件

在 `internal/middleware/` 创建新文件，然后在 `internal/router/router.go` 的中间件链中按顺序注册：

```go
r.Use(middleware.YourMiddleware())
```

注意顺序：RequestID 必须最先；Metrics 放在限流之前（被拒绝的请求也要计入 QPS 与错误率）；BodyLimit 必须早于任何读 Body 的中间件；**Recovery 必须在 Metrics/Logger 的内层** —— 放外层的话 panic 请求在指标里会记成 200（Recovery 还没写响应，`c.Writer.Status()` 是 gin 的默认值），基于 5xx 比例的告警永远不响；Timeout 放最后。

探针路由（`/livez`、`/readyz`、`/health`）刻意注册在 RateLimit 之前：gin 的 `Use` 只作用于之后注册的路由，探针一旦和业务流量共用令牌桶，过载时 kubelet 会拿到 429，liveness 判失败就重启容器 —— 恰好在最需要实例的时候把实例杀掉。

新中间件如果要打指标，标签值必须来自有限集合（路由模板、错误码、固定枚举），不要用路径、用户 ID、UA 这类无界值——指标基数一旦炸掉，Prometheus 和自己的进程会一起倒。

后台 goroutine 一律用 `safego.Go(ctx, name, fn)`：中间件里的 `go func` 不在请求链路上，panic 不会被 Recovery 接住，会直接终止进程。

## License

MIT License