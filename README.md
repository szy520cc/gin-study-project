# MyProject · Starlark 规则执行引擎配置系统

> 基于 Gin 分层框架构建的**规则配置平台**。运营在后台编写含 `if` 判断的 Starlark 规则，**保存时即完成编译校验**（不通过不允许入库），通过后灰度切流、全量发布；业务侧通过一个可被程序调用的 `/engine/eval` 接口，按「项目标识 + 配置标识」实时求值。

四大核心能力：**配置编写（保存即校验）** → **试跑验证** → **线下测试** → **发布管理**。

---

## 目录

- [这是什么](#这是什么)
- [核心概念与业务模型](#核心概念与业务模型)
- [四大核心能力](#四大核心能力)
- [关键机制](#关键机制)
- [项目结构](#项目结构)
- [技术栈](#技术栈)
- [快速开始](#快速开始)
- [配置说明](#配置说明)
- [分层架构](#分层架构)
- [后台管理界面](#后台管理界面)
- [可观测性](#可观测性)
- [优雅退出与摘流](#优雅退出与摘流)
- [API 接口](#api-接口)
- [数据模型](#数据模型)
- [错误码](#错误码)
- [测试](#测试)
- [常用命令](#常用命令)
- [版本信息](#版本信息)
- [CI](#ci)
- [日志](#日志)
- [扩展指南](#扩展指南)
- [License](#license)

## 这是什么

一句话：**把「业务判断逻辑」从代码里搬到配置平台上**。

传统做法是运营提需求、研发改代码、发版上线，一个判断条件的调整要走完整条发布链路。本系统把这类逻辑（例如「素材垂直类型属于 0/1/5 且命中敏感词 → 转人工复核」）抽象成**规则配置**：

- 运营在后台用 Starlark（Python 子集）语法编写规则，语法运营可读；
- 规则里通过占位符 `##指标ID**解析路径##` 引用「指标」（如 `material.vertical_type`）；
- 保存时后端把占位符**编译**成 Starlark 取值表达式，落库；
- 业务侧调用 `/engine/eval` 传入目标参数，引擎提取指标、执行规则、返回结果。

规则支持灰度：先切 10% 流量验证，确认无误再全量发布；线上版本永不原地修改，任何编辑都会 fork 出一个新版本草稿——回滚因此变成「把指针切回去」这种零成本操作。

工程上它是一个标准的 Go 分层项目：Gin + GORM + MySQL + Redis，`data` 层是唯一写 SQL 的地方，分层边界由测试强制。**框架层面的通用能力（配置加载与校验、中间件链、可观测性、优雅退出、事务、日志轮转）都在，业务层面则完全围绕规则引擎组织。**

## 核心概念与业务模型

### 业务层级

```
project（项目）              顶层容器，一个业务系统，主键是字符串
└── config_pack（配置包）     项目内的分组
    └── config（配置）        一条可独立发布的配置；type=rule 表示规则类
        └── rule（规则）      规则内容：占位符原文 + 编译成品 + 引用指标

field（字段 / 指标）          独立于层级之外，被规则按 ID 引用
```

关键点：

- **`config` 是版本化的主体**，`version`（时间戳）、`is_latest`、`status`、`cut_num` 都挂在它上面；
- **`rule` 是 `config` 的从表**（`config_id` 关联），规则内容较大（脚本 + 占位符原文），单独成表符合「按版本取子表」的查询模式；
- **`field`（指标）不属于任何项目**，是全局共享的取值定义：`parse_path` 是从输入 JSON 里提取值的点路径，`default_value` 是取不到值时的兜底。

### 与上游原型设计资料的映射

`资料/` 目录保留了本系统所借鉴的上游原型设计（表结构、切流原理、性能分析等）。表映射关系如下：

| 上游原型表 | 本项目表 | 说明 |
|---|---|---|
| `uc_metric`（指标） | `field`（字段） | 复用。`name`/`type`/`default_value`/`parse_path` 一一对应 |
| `uc_extension_pack`（扩展包） | `project`（项目） | 复用（顶层容器） |
| ——（原型无） | `config_pack`（配置包） | 本项目特有的中间分组层 |
| `uc_extension`（扩展） | `config`（配置） | 复用。原型已具备 `version`/`is_latest`/`status`/`cut_num`/`cut_version`，本项目补了 `cut_by` |
| `uc_ext_rule`（规则） | `rule`（规则） | **本项目新建** |
| `uc_ext_value`（简单值） | `config` 的非 rule 类型 | 复用（值本身就是一条配置） |

上游原型中若干**已知缺陷**在实现时被直接规避，详见 `docs/planning/ARCHITECTURE.md` §9.2（如切流比例越界静默失效、灰度回源查错 ID、事务内写 Redis 的窄窗口等）。

## 四大核心能力

### 1. 配置编写

后台「配置管理」页提供 Starlark 规则编辑器：

- 键入 `control` 快捷插入字段占位符；Tab 缩进；末尾必须给 `result` 赋值；
- 占位符写法 `##指标ID**解析路径##`，例如 `##209**material.vertical_type##`；
- 编辑器按项目过滤可用指标（字段），避免跨项目引用。

**编辑态与执行态是分离的**：前端保存的是占位符原文，落库时编译成可执行脚本，两个形态各存一列。

### 2. 配置验证（保存闸门 + TestRun 试跑）

**校验是保存的闸门，不是一个独立步骤。** 新增/编辑保存时 `engine.ValidateRule` 做两层校验，任一层不过就整单拒绝、不落库：

1. **占位符格式** —— 所有 `##...##` 片段必须形如 `##指标ID**解析路径##`，避免 `##abc**x##` 这类明显写错的占位符被静默留在脚本里；
2. **Starlark 编译** —— 编译成执行态脚本后做解析 + 名字解析 + 编译（**不执行**），拦住语法错误与未定义名字，报错带 `第 N 行：中文原因` 的行号定位。

页面层还提供「验证规则」按钮：必须填写非空「试跑入参（JSON）」并真实跑通一次才解锁保存；规则或 JSON 改动后必须重新验证。即使绕过页面直接调用保存接口，服务端也会再次执行同一闸门：用该 JSON 取字段并执行规则，运行错误或结果类型不匹配时拒绝保存。

> 校验语义与执行态**完全一致**：`go.starlark.net` 的 `TopLevelControl` 选项在本版本实际不生效，所以**函数外的 `return` 会被拒绝**。规则一律写成 `def rule(): ...` + 末尾 `result = rule()`。

同一套校验也会在 `publish` / `cutprogress` 之前对**库里已存的规则**再跑一遍（`ensureRuleConfigured`），防止历史脏数据、或规则行被绕过保存接口直接改坏之后仍被上线。

此外保留 `POST /api/v1/configs/testrun` 作为**可编程的试跑接口**：输入规则原文 + 结果类型 + 目标参数 JSON，立即返回执行结果，响应里的 `bind_var_info` 列出每个指标的实际提取值（`hit` 标记是否真的从输入里取到、取不到时用的默认值是什么），用于区分「规则写错了」还是「取值取错了」。纯计算，不落库不碰缓存。

### 3. 线下测试（`/engine/eval`）

`POST /api/v1/engine/eval` —— **供程序调用，不挂登录态**。

按「项目标识（pack）+ 配置标识（key）」求值，正是业务程序在真实链路里调用的那个接口。支持三种模式：

| 场景 | 传参 | 行为 |
|---|---|---|
| 锁定版本 | `version: "20260912175706987"` | 直接执行指定版本，跳过灰度抽样 |
| 草稿优先 | `offline_flag: true` | 走 `_latest` 草稿快照（未发布的最新版本） |
| 默认（线上） | 都不传 | 读版本指针，若处于灰度期则按比例抽样 |

> 该接口刻意不挂鉴权，面向「配置平台被程序调用」的场景。**生产环境必须靠部署网络隔离（内网 / 安全组）或 IP 白名单中间件保护**，不要直接暴露到公网。

### 4. 发布管理（灰度切流 + 全量发布）

| 动作 | 接口 | 说明 |
|---|---|---|
| 灰度切流 | `POST /api/v1/configs/cutprogress` | 把指定比例的流量切到待审核版本 |
| 全量发布 | `POST /api/v1/configs/publish` | 待审核版本转为生效、旧版本下线、灰度标记清零，发布即生效 |

**两个动作都有前置条件**（后台按钮据此决定是否显示，服务端另有兜底校验，见 `ensureRuleConfigured`）：

- 目标必须是**待审核**版本（`status=0`）——生效版本要改，只能先编辑 fork 出新草稿；
- 规则必须**已保存且通过校验**（列表接口回传 `rule_ready`）；
- 切流还要求**同标识已有线上版本**（列表接口回传 `has_active_version`）——灰度状态是写在线上版本行上的，没有线上版本就无处可挂。

**切流比例在后台只提供 10%~90% 共 9 档**（下拉选择，不支持手填；100% 请走「发布」转全量），接口层校验 `cut_num ∈ (0,1)` 开区间。这个限制正是为了规避上游原型「比例越界静默失效」的缺陷。

**切流与全量发布没有必然先后顺序**——可以先切流验证再全量，也可以在测试充分的前提下直接全量发布。

## 关键机制

这一节是理解本系统的核心，也是与普通 CRUD 项目最大的区别。

### 不可变版本链

**已生效版本（`status=1`）只读。** 编辑一个线上版本，系统不会原地修改它，而是 fork 出一行新记录：

```
编辑前：  v20260912120000  status=1  is_latest=2   ← 线上生效中
编辑后：  v20260912120000  status=1  is_latest=2   ← 仍在生效，内容未变
         v20260912175706  status=0  is_latest=1   ← 新草稿，承载本次编辑
```

- 草稿（`status=0`）**原地迭代**，不产生新行；
- 版本号 = 时间戳，单调递增（实现上加了 3 位随机后缀，避免同秒 fork 撞唯一索引）；
- `is_latest` 标记当前最新草稿（1=是，2=否）。

收益：灰度发布天然有前提（新旧两版同时在库）、回滚零成本、审计可追溯、发布失败零风险。

### 版本化缓存 key + 指针切换

执行面（`eval`）是热路径，不能每次都查库组装。缓存设计如下：

```
指针:  cur_ver_{pack}_{ext}               = version      （无 TTL）
快照:  eval_key_{pack}_{ext}_{version}    = 快照 JSON     （7 天 TTL）
草稿:  eval_key_{pack}_{ext}_latest       = 快照 JSON     （7 天 TTL，惰性回填）
```

其中 `pack` = `project.logo`，`ext` = `config.logo`。

**发布不删缓存，只切指针。** 旧版本的 key 靠 TTL 自然淘汰——缓存失效问题由此被转化成了 key 命名问题，这是整套设计里最值得借鉴的一点。

Redis 关闭时（`redis.enabled: false`）所有缓存读写静默降级为直查 DB，不影响功能正确性。

### 灰度切流

灰度状态（`cut_num` / `cut_version` / `cut_by` / `cut_at`）写在**老版本行**上，而不是新版本上。这样做的好处是：执行面一次 GET 就能拿到全量信息（老版本 + 新版本 + 切流比例），不需要两次查询。

执行侧按比例抽样：

```go
if rand.Float64() < cutNum {
    // 命中新版本
}
```

抽样用 Go 1.20+ 的全局 `rand.Float64()`（并发安全），命中则执行 `NewVersion` 快照，否则执行当前指针指向的版本。

灰度收尾 = `publish`：下线老版本时顺带清空灰度字段。

### 编辑态 / 执行态分离

| 形态 | 存储字段 | 内容 |
|---|---|---|
| 编辑态 | `rule.condition_config` | 占位符原文（含 `bind_var`），供前端回显 |
| 执行态 | `rule.rule` | 编译后的 Starlark 成品脚本 |
| 引用指标 | `rule.bind_var` | 去重后的指标 ID 列表（逗号分隔） |

前端永远面对占位符原文（人可读、可编辑），引擎永远面对编译成品（可直接执行），两边互不干扰。

### 占位符编译规则

```
##209**material.vertical_type##   →   context["material$vertical_type${209}"]
```

- 正则：`##(\d+)\*\*([\w.-]+)##`；
- `normalize(路径)` = 把 `.` 替换成 `$`；
- 环境 key 契约：`{normalize(parse_path)}${field.id}`；
- 编译的同时收集指标 ID 去重写入 `bind_var`。

执行前按 `bind_var` 批量查指标元信息，取不到值时用 `field.default_value` 解析后的默认值兜底。

### eval 决策树

`/engine/eval` 按以下优先级决定执行哪个版本：

```
1. 显式指定 version            → 直接执行该版本快照（跳过灰度）
2. offline_flag = true         → 读 _latest 草稿快照（缺失时回源 DB 并惰性回填）
3. 默认路径                    → 读 cur_ver 指针 + 对应快照
   └── 指针缺失 / 快照缺失      → 回源 DB 重建快照并写回（自愈）
4. 灰度期                      → rand.Float64() < cut_num 时执行 NewVersion
```

### Starlark 执行

- 引擎：`go.starlark.net`，`syntax.LegacyFileOptions()` + `TopLevelControl` + `GlobalReassign`；
- 沙箱无 IO；注入 `context` 全局变量；从 `globals["result"]` 取结果；
- 结果类型三种：`pass_reject_review` / `hit_result` / `json`；
- **设了 `SetMaxExecutionSteps`**：死循环脚本会被强制中断，而不是打满 CPU。

## 项目结构

```
myproject/
├── cmd/
│   ├── server/main.go            # 服务入口（-env / -config）
│   └── migrate/main.go           # AutoMigrate 建表入口
├── configs/
│   ├── config.yaml               # 入库默认配置（不含任何凭据）
│   ├── config.dev.yaml
│   ├── config.test.yaml
│   ├── config.prod.yaml
│   └── config.local.yaml         # 本机凭据（已被 .gitignore 忽略）
├── docs/
│   ├── planning/
│   │   ├── ARCHITECTURE.md                  # ★ 规则引擎架构设计（需求源头，必读）
│   │   ├── RULE_EDITOR_PLAN.md
│   │   └── frontend-code-review-2026-09-06.md
│   └── swagger/
├── internal/
│   ├── bootstrap/bootstrap.go    # 唯一装配点
│   ├── config/                   # 配置加载、优先级合并、启动校验
│   ├── controller/               # 参数绑定 → 调 service → 统一响应
│   ├── data/                     # 唯一写 SQL 的层（含 Redis 缓存读写）
│   ├── engine/                   # ★ Starlark 编译器 + 执行器（纯计算包，可独立单测）
│   │   ├── compiler.go           #   占位符 → Starlark 表达式 + bind_var 收集
│   │   ├── extract.go            #   jsoniter 点路径取值 + 默认值兜底
│   │   └── executor.go           #   Starlark 执行 + 结果转换
│   ├── middleware/               # 请求 ID / 恢复 / 指标 / 日志 / 安全头 / CORS / 限流 / 超时 / 认证
│   ├── model/                    # 数据模型与请求/响应结构（含快照结构）
│   ├── resource/resource.go      # 全局资源容器（DB / Redis / JWT / Cfg）
│   ├── router/router.go          # 中间件顺序 + 全部路由表
│   ├── service/                  # 业务规则、事务边界、错误映射
│   │   ├── rule.go               #   规则保存（含 fork 语义）
│   │   ├── publish.go            #   发布与灰度切流
│   │   ├── eval.go               #   决策树 + 灰度抽样 + 回源自愈
│   │   └── enrich.go             #   列表页可读名称批量回填
│   └── web/                      # ★ 后台管理界面（go:embed 打进二进制）
│       ├── web.go
│       └── assets/
│           ├── index.html        # amis 容器
│           ├── login.html        # 独立登录页（不加载 amis）
│           ├── pages/*.json      # amis 页面 schema（按菜单一项一个文件）
│           ├── pages/_frags/     # 可复用的选项片段
│           └── static/           # amis SDK / Bootstrap / 公共 js+css
├── pkg/
│   ├── admin/                    # 内部管理端口（/metrics、/debug/pprof、/version）
│   ├── auth/                     # JWT 签发校验 + bcrypt 密码哈希
│   ├── buildinfo/                # 版本号注入点（-ldflags -X）
│   ├── cache/                    # Redis 客户端
│   ├── database/                 # MySQL 客户端（连接池 + 慢查询日志）
│   ├── errcode/                  # 错误码定义
│   ├── health/                   # 健康检查函数注册表
│   ├── logger/                   # slog 封装 + 按小时轮转
│   ├── metrics/                  # Prometheus 指标
│   ├── response/                 # 统一响应格式
│   ├── safego/                   # 带 panic 恢复的 goroutine
│   └── transaction/              # 事务唯一入口 transaction.Do
├── test/                         # 集成测试 + 分层边界测试
├── 资料/                          # ★ 上游原型设计资料（表结构 / 切流 / 性能 / 接口分析）
├── scripts/                      # build.sh / deploy.sh
├── Makefile
├── Dockerfile / docker-compose.yml
└── README.md
```

带 ★ 的目录是理解本项目业务的关键：`internal/engine`（引擎）、`internal/web`（后台）、`docs/planning/ARCHITECTURE.md`（设计文档）、`资料/`（原型资料）。

## 技术栈

| 类别 | 技术 | 版本 |
|------|------|------|
| 语言 | Go | 1.25 |
| Web 框架 | [Gin](https://github.com/gin-gonic/gin) | 1.9.1 |
| ORM | [GORM](https://gorm.io/) | 1.25.5 |
| 规则引擎 | [go.starlark.net](https://github.com/google/starlark-go) | 2026-09-04 |
| JSON 提取 | [json-iterator](https://github.com/json-iterator/go) | 1.1.12 |
| 配置管理 | [Viper](https://github.com/spf13/viper) | 1.18.2 |
| JWT | [golang-jwt](https://github.com/golang-jwt/jwt) | 5.3.1 |
| 缓存 | [go-redis](https://github.com/redis/go-redis) | 9.3.0 |
| 指标 | [prometheus/client_golang](https://github.com/prometheus/client_golang) | 1.24.1 |
| 数据库 | MySQL | 8.0+ |
| 缓存 | Redis | 6.0+（可选，关闭后降级直查 DB） |
| 后台 UI | [amis](https://aisuda.bce.baidu.com/amis/) | 6.13（本地静态资源，不走 CDN） |

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

`cmd/migrate/main.go` 的 models 列表就是全部建表清单：`users` / `project` / `field` / `config_pack` / `config` / `rule`。

### 5. 运行项目

```bash
# 开发模式
make run

# 或直接运行
go run cmd/server/main.go

# 指定环境
go run cmd/server/main.go -env=prod
```

启动日志里会打印路由表与监听地址，便于确认挂载是否符合预期。

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

拿到 token 后即可调用后台管理界面：浏览器打开 **http://localhost:8080/admin**。

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
  enabled: true                # 关闭后不建连、健康检查不含 Redis，缓存静默降级为直查 DB
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

### 三个容易被忽略的安全配置

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
              ┌───────────────┴───────────────┐
              ▼                               ▼
┌───────────────────────────┐   ┌───────────────────────────┐
│          Engine           │   │           Data            │
│   • Starlark 编译 / 执行   │   │  • 唯一写 SQL 的地方        │
│   • 纯计算包，无外部依赖    │   │  • 缓存读写（cache.go）     │
│     （可独立单测）          │   │  • connDb(ctx) 自动复用事务 │
└───────────────────────────┘   └───────────────────────────┘
                                              │
                                              ▼
                              ┌───────────────────────────┐
                              │      Database / Cache      │
                              │        (MySQL / Redis)     │
                              │  由 internal/resource 持有  │
                              └───────────────────────────┘
```

### 各层职责

| 层级 | 目录 | 职责 |
|------|------|------|
| **Router** | `internal/router/` | 中间件顺序、探针、路由表；新增接口的唯一登记点 |
| **Controller** | `internal/controller/` | 包级函数：参数校验、取 `user_id`、调 Service、统一响应；不持有 DB/Redis |
| **Service** | `internal/service/` | 包级函数：业务规则、事务边界、错误映射、分页上限；不写 SQL |
| **Data** | `internal/data/` | 包级函数：唯一写 SQL 的地方，`connDb(ctx)` 自动感知事务；只返回 gorm 原始错误 |
| **Engine** | `internal/engine/` | 纯计算包：占位符编译、点路径提取、Starlark 执行；不依赖 controller/service/data/resource |
| **Resource** | `internal/resource/` | 全局资源容器：`DB(ctx)` / `JWT()` / `Redis()` / `Cfg()`，由 bootstrap 一次性 `Set` |
| **Model** | `internal/model/` | 数据模型定义、请求/响应结构体 |
| **Middleware** | `internal/middleware/` | 请求 ID、panic 恢复、指标、日志、安全头、跨域、限流、请求体上限、超时、认证 |
| **Web** | `internal/web/` | 后台管理 UI 的静态资源与 amis schema（`go:embed` 打进二进制） |

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

判断标准只有一条：**一个业务动作是否对应多次写入**。单条 INSERT/UPDATE 本身就是原子的，GORM 默认还会替它套一层事务，再包一次 `transaction.Do` 只是多一次 BEGIN/COMMIT 往返 —— 所以单次写操作不需要显式包事务。

本项目的真实用例是**发布**：下线其他生效版本、激活目标版本、写 Redis 指针，前两步必须同生同死。

```go
return transaction.Do(ctx, func(ctx context.Context) error {
	// 同一事务里多次写入，任一失败整体回滚
	if err := connDb(ctx).Model(&model.Config{}).Where(...).Updates(...).Error; err != nil {
		return err
	}
	return connDb(ctx).Model(&model.Config{}).Where(...).Updates(...).Error
})
// 指针 SET 刻意放在事务 commit 之后 —— 事务内写 Redis 存在「DB 回滚但缓存已改」的窄窗口
```

`Do` 把事务句柄放进 ctx，`data` 层的 `connDb(ctx)` 自动认领 —— 所以同一个 data 函数在事务内外都能用，不必写第二套 `XxxWithTx`。闭包返回任何 error 都整体回滚；嵌套调用 `Do` 会复用外层事务（SavePoint 语义）。

一条约束：**闭包收到的 ctx 不能逃出闭包**。`Do` 返回时会把 ctx 里的句柄置为失效，此后 `TxFrom` 一律返回 false —— 因为事务早已 Commit/Rollback，把 ctx 交给后台 goroutine 再拿它写库就是在用一个已结束的 `*sql.Tx`。另外没有导出 `WithTx`：写入口一旦公开，任何代码都能把普通 `*gorm.DB` 冒充成事务句柄塞进 ctx，`resource.DB(ctx)` 会当事务用而实际每条语句自动提交。事务的唯一入口是 `transaction.Do`。

### 日志用法

请求入口已把 `request_id`（以及认证后的 `user_id`）绑定到 ctx，业务层直接用：

```go
logger.C(ctx).Info("config published", "config_id", id, "version", version)
```

## 后台管理界面

后台是一个 **amis 单页应用**，挂在 `/admin`（与业务 API 共享 8080 端口），全部静态资源通过 `go:embed` 打进二进制，部署时不需要额外挂载文件。

### 路径规划

| 路径 | 内容 |
|---|---|
| `/admin/login` | 独立登录页（不加载 amis SDK，避免「登录前就要跑 amis 初始化」的鸡生蛋问题） |
| `/admin/` | amis 容器页（顶栏 + 侧边菜单 + 多标签页） |
| `/admin/pages/*.json` | amis 页面 schema，一项菜单一个文件 |
| `/admin/static/*` | amis SDK / Bootstrap / 公共 js+css（带 1 小时 `Cache-Control`） |

> schema JSON 走独立 handler 而不是 `StaticFS`：后者会把整个目录树挂上去，未来误丢一个文件也会被无脑暴露。

### 菜单与页面

菜单配置在 `internal/web/assets/static/common/app_menu.js`（`window.APP_MENU`），改菜单只动这一个文件：

| 分组 | 页面 | schema |
|---|---|---|
| 概览 | 首页 | `pages/home.json` |
| 项目管理 | 项目管理 | `pages/project.json` |
| 项目管理 | 字段管理 | `pages/field.json` |
| 配置管理 | 配置包管理 | `pages/config-pack.json` |
| 配置管理 | 配置管理 | `pages/config.json` |
| 系统管理 | 用户管理 | `pages/users.json` / `pages/user-add.json` |
| 系统管理 | 系统监控 | `pages/monitor.json` |

### 几个实现约定

- **响应适配**：后端列表统一返回 `{list, total}`，而 amis 期望 `{items, total, count}`。`app.js` 里的 `authFetcher` 对**所有**响应（含 select 的 `source` 请求）统一做这层转换，所以 schema 适配器里写 `payload.data.items`。
- **鉴权**：`authFetcher` 是传给 `amis.embed` 的 fetcher，自动带上 `localStorage` 里的 token，并按 amis 6.x 契约把后端 `{code, message, data}` 规整成带 `status` 的响应体。
- **规则编辑器**：`rule-editor.js` 注册为自定义表单项（`className: "rule-editor"`），负责 `control` 快捷插入占位符与 Tab 缩进。（`json-editor.js` 原用于「目标参数」JSON 编辑区，随独立验证弹窗一并下线，现已无页面引用。）
- **下拉联动**：amis 6.x 会在 `source.url` 里 `${}` 变量变化时自动重发请求，配置管理页的「所属配置包」筛选即按此跟随「所属项目」。
- **`api.data` 是「替换」而不是「合并」**——这是本项目踩过坑的两处约定：
  - 给**表单**配 `api.data` 会**覆盖整个提交体**（表单字段全丢，后端报「xx 不能为空」）；
  - 给 **crud** 配 `api.data` 会**覆盖整个 query string**（`project_id`/`page`/`page_size` 等筛选与分页参数全丢，表现为「筛选不生效」）。
  - 因此约定：**新增/编辑表单与 crud 一律不写 `api.data`**，靠表单数据域提交；固定查询条件用隐藏表单项表达（`{ "type": "hidden", "name": "type", "value": "rule" }`）；`api.data` 只用于「删除 / 发布」这类**没有表单字段**的确认框。
  - 另一个推论：`initApi` 回填的字段会进入表单数据域，提交时**整个数据域**都会被发出（即使没有对应表单项），所以主键无需再手工映射进 `data`。
- **crud 必须显式声明分页字段名**：`"pageField": "page"`、`"perPageField": "page_size"`。缺省时 amis 发的是 `perPage`，而后端只认 `page_size`，分页大小会静默失效。

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

**`route` 标签用的是路由模板（`/api/v1/users/:id`）而不是真实路径。** 用真实路径会让每个 ID 产生一条独立时间序列，指标基数无上限增长，先撑爆 Prometheus 再撑爆自己的内存。未匹配的路径统一归到 `unmatched`，否则扫描器乱打的路径同样会炸标签。`test/framework_test.go` 有用例守着这条约束。

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

统一前缀 `/api/v1`。业务模块（项目 / 字段 / 配置包 / 配置）采用**动作式路由**，不按 REST 资源法区分 method：

```
POST  /xxx/add     添加
POST  /xxx/update  修改（body 带 id）
POST  /xxx/delete  删除（body 带 id）
GET   /xxx/list    列表
GET   /xxx/detail  详情（query 带 id，供编辑回填）
```

好处是「这个服务对外提供什么」有唯一答案，且新增接口不用纠结 method 语义。

### 公开接口

业务端口（默认 `:8080`）：

| 方法 | 路径 | 描述 | 请求体 |
|------|------|------|--------|
| GET | `/livez` | 存活探针（不探测依赖，回显版本号） | - |
| GET | `/readyz` | 就绪探针（依赖异常或摘流中返回 503，结果缓存 2 秒） | - |
| GET | `/health` | 同 `/readyz`，兼容旧路径 | - |
| POST | `/api/v1/users/register` | 用户注册（独立限流配额） | `UserRegisterRequest` |
| POST | `/api/v1/users/login` | 用户登录（独立限流配额） | `UserLoginRequest` |
| POST | `/api/v1/engine/eval` | **规则求值（线下测试，不挂鉴权）** | `EvalRequest` |

内部端口（默认 `127.0.0.1:9090`，不对外暴露）：

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/metrics` | Prometheus 指标 |
| GET | `/version` | 版本与 Go 版本 |
| GET | `/debug/pprof/*` | 性能剖析（需 `admin.pprof: true`） |

### 需要认证的接口

请求头添加：`Authorization: Bearer <token>`

#### 用户接口

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | `/api/v1/users/profile` | 获取当前用户信息 |
| PUT | `/api/v1/users/profile` | 更新当前用户信息 |
| GET | `/api/v1/users` | 用户列表（仅公开字段；`?page=1&page_size=10`） |
| GET | `/api/v1/users/:id` | 指定用户的公开信息（不含 email/phone/status） |

#### 项目管理 `/api/v1/projects/*`

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | `/add` | 新建项目（`logo` 为全局唯一标识，是 eval 的 `pack`） |
| POST | `/update` | 修改 |
| POST | `/delete` | 删除 |
| GET | `/list` | 列表 |
| GET | `/detail` | 详情（`?id=`） |

#### 字段管理（指标）`/api/v1/fields/*`

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | `/add` | 新建指标（`parse_path` 为点路径，如 `material.vertical_type`） |
| POST | `/update` | 修改 |
| POST | `/delete` | 删除 |
| GET | `/list` | 列表 |
| GET | `/detail` | 详情（`?id=`） |

#### 配置包管理 `/api/v1/config-packs/*`

同上五个动作（`add` / `update` / `delete` / `list` / `detail`）。

#### 配置管理 `/api/v1/configs/*`

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | `/add` | 新建配置 |
| POST | `/update` | 修改 |
| POST | `/delete` | 删除 |
| GET | `/list` | 列表（支持 `project_id` / `config_pack_id` / `name` / `type` / `status` / `is_latest` 筛选） |
| GET | `/detail` | 详情（`?id=`） |

#### 规则与发布 `/api/v1/configs/*`

| 方法 | 路径 | 描述 | 请求体 |
|------|------|------|--------|
| POST | `/rule/add` | 一步创建「type=rule 配置 + 规则内容」，版本号自动生成，`logo` 查重 | `CreateRuleConfigRequest` |
| POST | `/rule/update` | 更新规则（编辑生效版本会自动 fork 新版本草稿） | `UpdateRuleConfigRequest` |
| POST | `/rule/save` | 保存规则（编译落库，含 fork 语义） | `SaveRuleRequest` |
| GET | `/rule/detail` | 规则详情（含占位符原文供回显 + 引用指标详情） | `?id=` |
| POST | `/testrun` | **试跑验证**（编译执行，不落库不碰缓存） | `TestRunRequest` |
| POST | `/publish` | 全量发布 | `PublishRequest` |
| POST | `/cutprogress` | 灰度切流 | `CutProgressRequest` |

### 规则求值契约（`/api/v1/engine/eval`）

```jsonc
// 请求
{
  "pack": "ec_project",          // 项目标识（project.logo），必填
  "key": "ec_manual_rule",       // 配置标识（config.logo），必填
  "version": "",                 // 指定版本则跳过灰度；留空走默认决策树
  "offline_flag": false,         // true 时读草稿快照（未发布的最新版本）
  "data": { "material": { "vertical_type": 1 } }   // 目标参数，可为空对象
}

// 响应
{
  "code": 0,
  "message": "success",
  "data": {
    "pack": "ec_project",
    "key": "ec_manual_rule",
    "version": "20260912175706987",   // 实际执行的版本号
    "value": 1,                        // 执行结果
    "result_type": "pass_reject_review"
  },
  "request_id": "8f1c2d3e4a5b6c7d8e9f0a1b"
}
```

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

时间戳统一用 `int64` 存 Unix 秒（不依赖 GORM 的自动时间戳），避免时区与精度问题。

### `users` 用户

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 用户 ID |
| username | string | 用户名（唯一） |
| password | string | 密码（bcrypt 哈希，json 序列化时隐藏） |
| email | string | 邮箱（唯一） |
| phone | string | 手机号 |
| avatar | string | 头像 URL |
| status | int8 | 状态：1-正常，0-禁用 |
| created_at / updated_at | int64 | Unix 秒 |

### `project` 项目

顶层容器，`id` 是**字符串主键**，`logo` 是全局唯一标识（即 eval 的 `pack`）。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | string | 主键 |
| name | string | 项目名称 |
| logo | string | 项目标识（唯一，eval 的 pack） |
| status | uint8 | 状态 |
| remark | string | 备注 |

### `config_pack` 配置包

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 主键 |
| project_id | string | 所属项目 |
| name | string | 配置包名称 |
| logo | string | 配置包标识 |
| status | uint8 | 状态 |
| remark | string | 备注 |

### `field` 字段（指标）

全局共享，不属于任何项目。`parse_path` 决定从输入 JSON 里怎么取值，`default_value` 决定取不到时用什么。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 主键（即占位符里的「指标 ID」） |
| name | string | 指标名称 |
| type | string | 类型（int / string / json 等） |
| parse_path | string | 解析路径（点路径，如 `material.vertical_type`） |
| default_value | string | 默认值（JSON，取不到值时兜底） |
| status | uint8 | 状态 |
| remark | string | 备注 |

### `config` 配置

版本化主体。`version` / `is_latest` / `status` / 灰度字段都在这一行上。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 主键 |
| project_id | string | 所属项目 |
| config_pack_id | uint64 | 所属配置包 |
| name | string | 配置名称 |
| logo | string | 配置标识（即 eval 的 `key`，与 `version` 联合唯一） |
| type | string | 类型，`rule` 表示规则类 |
| version | string | 版本号（时间戳 + 3 位随机后缀） |
| status | uint8 | 0-待审核 / 1-生效 / 2-下线 |
| is_latest | uint8 | 1-是最新草稿 / 2-否 |
| cut_num | float64 | 灰度比例 (0,1)，写在**老版本行**上 |
| cut_version | string | 灰度目标版本 |
| cut_by | string | 切流操作人 |
| cut_at | int64 | 切流时间（Unix 秒） |
| remark | string | 备注 |

### `rule` 规则

`config` 的从表，按 `config_id` 关联。规则内容较大，与主行分离符合「按版本取子表」的查询模式。

| 字段 | 类型 | 说明 |
|------|------|------|
| id | uint64 | 主键 |
| config_id | uint64 | 关联 `config.id` |
| rule | string | **编译后**的 Starlark 成品脚本（执行用） |
| condition_config | string | **占位符原文** JSON（编辑回显用） |
| bind_var | string | 逗号分隔的指标 ID（去重，执行前批量取元信息用） |
| result_type | string | `pass_reject_review` / `hit_result` / `json` |
| engine | string | 写死 `starlark` |
| pack | string | 冗余：所属项目 `logo` |
| extension | string | 冗余：所属配置 `logo` |
| version | string | 冗余：`config.version` |

> `pack` / `extension` / `version` 是刻意冗余的：执行面可以只靠这三个字段直接定位规则，不必再回查 `config` 表。

### Redis 缓存结构

| Key | 值 | TTL |
|---|---|---|
| `cur_ver_{pack}_{ext}` | 当前生效版本号 | 无 |
| `eval_key_{pack}_{ext}_{version}` | 版本快照（`ConfigSnapshot` JSON） | 7 天 |
| `eval_key_{pack}_{ext}_latest` | 草稿快照（惰性回填） | 7 天 |

`ConfigSnapshot` 一次装全量：主表信息 + 规则 + 引用指标元信息，灰度期还会嵌一个 `new_version`（同结构）装待上线版本——一个 key 装两份。

## 错误码

### 通用错误 (10xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 0 | 200 | 成功 |
| 10001 | 500 | 内部错误 |
| 10002 | 400 | 参数错误 |
| 10003 | 404 | 资源不存在 |
| 10004 | 401 | 未授权 |
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

### 项目错误 (50xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 50001 | 404 | 项目不存在 |
| 50002 | 400 | 项目标识已存在 |

### 字段错误 (60xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 60001 | 404 | 字段不存在 |
| 60002 | 400 | 字段解析路径已存在 |
| 60003 | 400 | 字段类型不合法 |
| 60004 | 400 | 字段默认值不合法 |
| 60005 | 400 | 字段默认值类型与字段类型不一致 |

### 配置包错误 (70xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 70001 | 404 | 配置包不存在 |
| 70002 | 400 | 配置包标识已存在 |

### 配置错误 (80xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 80001 | 404 | 配置不存在 |
| 80002 | 400 | 配置版本已存在 |

### 规则与发布错误 (90xxx)

| 错误码 | HTTP 状态码 | 描述 |
|--------|------------|------|
| 90001 | 404 | 规则不存在 |
| 90002 | 400 | 规则内容不合法（占位符格式或 Starlark 编译未通过，`details` 带 `第 N 行` 定位） |
| 90003 | 500 | 规则执行失败 |
| 90010 | 400 | 配置已生效，不能重复发布 |
| 90011 | 400 | 配置状态不合法 |
| 90012 | 400 | 切流比例必须在 (0,1) 之间 |
| 90013 | 400 | 无生效版本，无法切流 |

## 测试

框架不留 mock 注入点，所以业务链路一律**打真实数据库**。理由很直接：mock 出来的 DB 只能验证「我调了这个方法」，验不了唯一键冲突、条件更新的 `RowsAffected`、事务回滚这些真正会出问题的地方 —— 而这些恰好是本框架的核心机制。

测试分三类：

- **免 DB 的框架测试** `test/framework_test.go` —— 405/413、限流、探针绕过限流、panic 记成 500、指标 route 标签是模板、安全头。任何环境都能跑。
- **分层边界测试** `test/layering_test.go` —— 扫 import 表守分层约束：`service` 不许 import `gorm` / `resource`，`controller` 不许 import `data`，`data` 不许 import `errcode` / `gin`。任何环境都能跑。
- **真库集成测试** `test/user_api_test.go`、`test/tx_test.go`、`test/rule_test.go` —— 走完整 HTTP 链路（`httptest` + 真 engine + 真库），用例自己 `t.Cleanup` 清数据。连不上库时会 `t.Skip` 并打印起库命令，不会静默通过。

其中 `test/rule_test.go` 的 `TestRuleLifecycle` 覆盖了完整的业务生命周期：建指标 → 保存规则（占位符编译）→ 试跑验证 → 发布 → 编辑触发 fork → 灰度切流 → 求值 → 全量收尾 → 越界校验。

引擎侧另有独立单测 `internal/engine/engine_test.go`（编译 / 提取 / 执行 / JSON 结果全链路），因为 `internal/engine` 是纯计算包，不依赖任何基础设施，可以脱离 DB 跑。

`test/setup_test.go` 的 `TestMain` 负责：加载 `configs/config.dev.yaml` → 关掉限流与 body 日志 → 连库 → `AutoMigrate` 所需表 → `health.Init` → `resource.Set` → `router.Setup` 建出全局 engine。

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
logger.C(ctx).Info("config published", "config_id", id)
```

> 日志中间件对 `password` / `token` / `secret` / `authorization` / `credential` / `id_card` / `phone` 等敏感字段做了脱敏（含 `access_token`、`user_password` 这类子串变体），且查询串会整体处理——否则 `?username=x&password=y` 会原样落盘。

## 扩展指南

### 添加新的业务模块

以本项目的 `config-pack` 为例，一个完整模块是：

1. **定义模型** - `internal/model/xxx.go`（列表请求内嵌 `model.PageRequest`；时间字段用 `int64` 存 Unix 秒；实体上加 `ToResponse()`）
2. **创建 Data** - `internal/data/xxx.go`，包级函数，用 `connDb(ctx)` 取连接写 gorm 查询；只返回 gorm 原始错误，不 import `errcode`；更新用 `Select(白名单).Updates` 而不是 `Save`（`Save` 是全字段覆盖，并发下会丢更新）
3. **创建 Service** - `internal/service/xxx.go`，包级函数，调 `data.Xxx`，用 `data.IsNotFound` / `data.IsDuplicate` 判定后翻译成 `errcode`，跨表写入用 `transaction.Do(ctx, ...)` 包住
4. **创建 Controller** - `internal/controller/xxx.go`，包级函数，用 `bindJSON` / `bindQuery` 绑定参数（自带校验错误中文化与 413 识别），需要身份时开头调 `middleware.RequireUserID(c)`，只做绑定与响应
5. **登记路由** - 在 `internal/router/router.go` 加一个 `registerXxx(v1, auth)` 并在 `registerAPIRoutes` 里调用一次
6. **登记建表** - 在 `cmd/migrate/main.go` 的 models 列表里加上新实体
7. **错误码** - 在 `pkg/errcode/errcode.go` 按模块段位加新错误码（当前段位：通用 10xxx、认证 20xxx、用户 30xxx、项目 50xxx、字段 60xxx、配置包 70xxx、配置 80xxx、规则与发布 90xxx）
8. **健康检查** - 若引入了新的外部依赖，在 `internal/bootstrap/bootstrap.go` 中通过 `health.RegisterFunc("xxx", ...)` 注册，`/readyz` 会自动纳入
9. **后台 goroutine** - 一律用 `safego.Go`，裸 `go func` 里的 panic 不会被 Recovery 中间件捕获，会直接终止进程

合计：**4 个新文件（model / data / service / controller）+ 2 个登记点（router、migrate）**，没有接口、没有构造函数、没有装配文件。分层边界由 `test/layering_test.go` 扫 import 表守着。

若还要在后台加页面：新建 `internal/web/assets/pages/xxx.json`，并在 `app_menu.js` 的 `window.APP_MENU` 里挂一个叶子节点。

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
