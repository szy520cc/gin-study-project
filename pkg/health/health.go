// Package health 提供依赖健康检查注册表。
//
// 原实现把 *gorm.DB 直接塞进 HealthHandler，每加一个依赖（Redis、MQ、下游服务）
// 就要往 HTTP 层里塞一个客户端。这里改为注册表模式：
// 各基础设施在装配阶段注册自己的探测函数，探针接口只负责遍历。
package health

import (
	"context"
	"myproject/pkg/logger"
	"myproject/pkg/safego"
	"sync"
	"sync/atomic"
	"time"
)

// checker 单个依赖的健康探测。
// 不做成导出 interface：探测就是「一个名字 + 一个函数」，
// 用 RegisterFunc 注册即可，没有第二种实现形态需要抽象。
type checker struct {
	name string
	fn   func(ctx context.Context) error
}

// Registry 健康检查注册表
type Registry struct {
	mu       sync.RWMutex
	checkers []checker
	timeout  time.Duration

	// 探测结果缓存。/readyz 无认证且会真打下游，不缓存的话
	// 外部可以拿它当放大器持续压 DB/Redis；同时 K8s 多副本探针也会叠加压力。
	cacheTTL   time.Duration
	cacheMu    sync.Mutex
	cached     Report
	cachedAt   time.Time
	cacheValid bool

	// probeMu 保证同一时刻只有一批探测在跑（single-flight）。
	// 没有它的话：某个 checker 不理 ctx 而挂死 → 缓存永远写不进去 →
	// 每个新来的探针请求都再起一整批 goroutine 和下游连接，
	// K8s 多副本每几秒一次，故障时会自己把下游打穿。
	probeMu sync.Mutex

	// draining 标记「正在退出」。收到 SIGTERM 后先把它置上，
	// /readyz 立刻返回 503 让 LB 摘流，等存量连接排空再真正关闭。
	draining atomic.Bool

	// exposeErrors 是否把下游的原始错误写进 /readyz 响应。
	// 默认关闭：/readyz 无认证，原始错误里会带 "dial tcp 10.x.x.x:3306" 这类
	// 内网地址与端口。错误详情一律进日志，不进响应体。
	exposeErrors atomic.Bool
}

// NewRegistry 创建注册表。
// timeout 为单个依赖的探测超时，cacheTTL 为探测结果缓存时长（0 表示不缓存）。
func NewRegistry(timeout, cacheTTL time.Duration) *Registry {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Registry{timeout: timeout, cacheTTL: cacheTTL}
}

// RegisterFunc 注册一个依赖的探测函数
func (r *Registry) RegisterFunc(name string, fn func(ctx context.Context) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkers = append(r.checkers, checker{name: name, fn: fn})
}

// Result 单个依赖的探测结果
type Result struct {
	Status string `json:"status"` // ok / error
	Error  string `json:"error,omitempty"`
}

// Report 整体探测报告
type Report struct {
	Healthy  bool              `json:"-"`
	Services map[string]Result `json:"services"`
	// Draining 是否处于退出摘流阶段
	Draining bool `json:"draining,omitempty"`
}

// SetExposeErrors 设置是否把下游原始错误写进探针响应（仅建议在非生产环境开启）。
// 顺带作废缓存：exposeErrors 是在探测时决定要不要把原始错误写进 Report 的，
// 关掉开关而不作废缓存的话，已经含内网地址的那份报告还会继续返回一整个 TTL。
func (r *Registry) SetExposeErrors(v bool) {
	r.exposeErrors.Store(v)
	r.cacheMu.Lock()
	r.cacheValid = false
	r.cacheMu.Unlock()
}

// StartDraining 标记进入摘流阶段。此后 Check 一律返回不健康，
// 使 /readyz 返回 503，负载均衡把本实例从后端列表里摘掉。
func (r *Registry) StartDraining() { r.draining.Store(true) }

// IsDraining 是否处于摘流阶段
func (r *Registry) IsDraining() bool { return r.draining.Load() }

// Check 探测所有依赖。结果在 cacheTTL 内复用，避免探针被当成放大器。
func (r *Registry) Check(ctx context.Context) Report {
	// 摘流期间不必再探测下游：结论已经确定，且此时下游可能已被关闭
	if r.draining.Load() {
		return Report{Healthy: false, Draining: true, Services: map[string]Result{}}
	}

	// 探测不继承调用方 ctx 的取消：/readyz 是公开路由且套了超时中间件，
	// 客户端断连或请求超时会让 Ping 立刻返回 canceled，
	// 若把这个结果写进缓存，TTL 内所有探针都会读到「不健康」——
	// 一次 curl 中途 Ctrl-C 就能让健康实例被 LB 摘掉。
	//
	// 但断开取消链之后必须自己补一个兜底 deadline：r.timeout 只对「尊重 ctx」的
	// checker 有效，Checker 是导出接口，塞一个同步 net.Dial 进来就能让 check 永不返回。
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.overallTimeout())
	defer cancel()

	if r.cacheTTL <= 0 {
		return r.check(probeCtx)
	}

	if cached, ok := r.cachedReport(); ok {
		return cached
	}

	// 不持 cacheMu 做网络 IO：探测实现若不理 ctx，持锁会让所有 /readyz 一起阻塞。
	// 换成独立的 probeMu 做 single-flight，抢到锁的那个请求负责探测，
	// 其余请求在这里排队，醒来后先看一眼缓存 —— 通常已经有新结果了。
	r.probeMu.Lock()
	defer r.probeMu.Unlock()
	if cached, ok := r.cachedReport(); ok {
		return cached
	}

	startedAt := time.Now()
	report := r.check(probeCtx)

	r.cacheMu.Lock()
	r.cached = report
	// 记探测「开始」时刻而不是结束时刻：慢探测拿到的是较旧的事实，
	// 用结束时刻会让这份旧结论多活一个探测耗时那么久。
	r.cachedAt = startedAt
	r.cacheValid = true
	r.cacheMu.Unlock()

	return report
}

// overallTimeout 一批探测的整体上限：单个 checker 超时的两倍，
// 给并发调度和结果收集留出余量。
func (r *Registry) overallTimeout() time.Duration {
	return 2 * r.timeout
}

// cachedReport 返回仍在 TTL 内的缓存结果
func (r *Registry) cachedReport() (Report, bool) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	if r.cacheValid && time.Since(r.cachedAt) < r.cacheTTL {
		return r.cached, true
	}
	return Report{}, false
}

// check 并发探测所有依赖
func (r *Registry) check(ctx context.Context) Report {
	r.mu.RLock()
	checkers := make([]checker, len(r.checkers))
	copy(checkers, r.checkers)
	r.mu.RUnlock()

	report := Report{Healthy: true, Services: make(map[string]Result, len(checkers))}
	if len(checkers) == 0 {
		return report
	}

	type outcome struct {
		name string
		err  error
	}
	results := make(chan outcome, len(checkers))

	for _, c := range checkers {
		go func(c checker) {
			// 探测函数来自各基础设施，panic 不会被 gin 的 Recovery 接住，
			// 这里兜住并转成 error：一个探测实现的 bug 不该杀进程，
			// 但也必须体现为「不健康」而不是被静默忽略。
			err := safego.RunE(ctx, "health-check-"+c.name, func() error {
				cctx, cancel := context.WithTimeout(ctx, r.timeout)
				defer cancel()
				return c.fn(cctx)
			})
			results <- outcome{name: c.name, err: err}
		}(c)
	}

	// 收集结果时不能死等：不理 ctx 的 checker 会让这里永久阻塞，
	// 连带 /readyz 一起挂住。ctx 到期后把还没回来的依赖记为不健康，
	// 残留 goroutine 之后往带缓冲的 channel 里写，不会泄漏。
	pending := make(map[string]struct{}, len(checkers))
	for _, c := range checkers {
		pending[c.name] = struct{}{}
	}

	for i := 0; i < len(checkers); i++ {
		var o outcome
		select {
		case o = <-results:
		case <-ctx.Done():
			for name := range pending {
				report.Healthy = false
				logger.C(ctx).Error("health check timeout", "dependency", name, "error", ctx.Err().Error())
				res := Result{Status: "error"}
				if r.exposeErrors.Load() {
					res.Error = "probe timeout"
				}
				report.Services[name] = res
			}
			return report
		}
		delete(pending, o.name)

		if o.err != nil {
			report.Healthy = false
			// 详情一律进日志；是否进响应体由 exposeErrors 决定
			logger.C(ctx).Error("health check failed", "dependency", o.name, "error", o.err.Error())
			res := Result{Status: "error"}
			if r.exposeErrors.Load() {
				res.Error = o.err.Error()
			}
			report.Services[o.name] = res
			continue
		}
		report.Services[o.name] = Result{Status: "ok"}
	}

	return report
}
