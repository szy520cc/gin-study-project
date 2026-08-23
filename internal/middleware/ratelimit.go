package middleware

import (
	"sync"
	"time"

	"myproject/pkg/errcode"
	"myproject/pkg/metrics"
	"myproject/pkg/response"

	"github.com/gin-gonic/gin"
)

// NoOp 空操作中间件。用于「功能关闭时」占位，避免调用方到处写分支判断。
func NoOp() gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

// RateLimit 按客户端 IP 的令牌桶限流（单机维度）。
//
// scope 用于区分不同配额（global / auth），只作为指标标签，不影响算法。
//
// 说明：
//   - 这里手写令牌桶而非引入 x/time/rate，是为了不增加依赖；
//     算法等价，桶状态惰性计算。
//   - 单机限流适合挡住单 IP 的异常流量。多实例部署下的全局配额
//     需要用 Redis 实现（本项目已有 Redis 客户端，可平滑替换本实现）。
//   - 生效前提是 ClientIP() 可信，即 router 里正确设置了 SetTrustedProxies，
//     否则伪造 X-Forwarded-For 就能绕过。
func RateLimit(scope string, rps float64, burst int) gin.HandlerFunc {
	if rps <= 0 || burst <= 0 {
		return NoOp()
	}

	lim := &ipLimiter{
		rps:      rps,
		burst:    float64(burst),
		buckets:  make(map[string]*bucket),
		lastedGC: time.Now(),
	}

	return func(c *gin.Context) {
		if !lim.allow(c.ClientIP()) {
			metrics.RateLimitRejected.WithLabelValues(scope).Inc()
			response.Error(c, errcode.ErrTooManyReq)
			return
		}
		c.Next()
	}
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

type ipLimiter struct {
	mu      sync.Mutex
	rps     float64
	burst   float64
	buckets map[string]*bucket
	// lastedGC 上次清理时间。清理由请求驱动而不是后台 goroutine：
	// RateLimit 只返回一个 HandlerFunc，调用方拿不到停止句柄，
	// 后台 ticker 就永远不会退出（每次 router.Setup 泄漏一个，测试里会累积）。
	// 惰性清理没有生命周期问题：没有流量时也不需要清理。
	lastedGC time.Time
}

// maxBuckets 桶数量上限。
//
// 只靠 GC 兜不住：GC 是 5 分钟一次、清 10 分钟未活跃的条目，
// 而一台机器每秒能接上千个不同源 IP（IPv6 更是随手换地址），
// 两次 GC 之间就足够把 map 撑到几百万条。到达上限后不再新建桶，
// 直接放行 —— 限流是保护措施，它自己不该成为内存耗尽的原因；
// 真正需要全局精确配额时应换成 Redis 实现。
const maxBuckets = 100000

func (l *ipLimiter) allow(ip string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now)

	b, ok := l.buckets[ip]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			metrics.RateLimitRejected.WithLabelValues("bucket-overflow").Inc()
			return true
		}
		l.buckets[ip] = &bucket{tokens: l.burst - 1, lastSeen: now}
		return true
	}

	// 惰性补充令牌
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * l.rps
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweepLocked 清理长期不活跃的 IP，避免 map 无界增长。调用方必须持锁。
// 每 gcInterval 最多执行一次，摊到请求上的开销可以忽略。
func (l *ipLimiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastedGC) < gcInterval {
		return
	}
	l.lastedGC = now

	cutoff := now.Add(-idleTTL)
	for ip, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, ip)
		}
	}
}

const (
	gcInterval = 5 * time.Minute
	idleTTL    = 10 * time.Minute
)
