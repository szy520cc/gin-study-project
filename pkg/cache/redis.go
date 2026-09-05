package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient Redis 客户端封装
type RedisClient struct {
	client *redis.Client
}

// Options Redis 连接参数。
// 与 pkg/database 同理：不依赖 internal/config，由调用方翻译。
type Options struct {
	Host     string
	Port     int
	Password string
	DB       int
}

// NewRedis 创建 Redis 连接
func NewRedis(opt Options) (*RedisClient, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", opt.Host, opt.Port),
		Password: opt.Password,
		DB:       opt.DB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect redis: %w", err)
	}

	return &RedisClient{client: client}, nil
}

// Close 关闭连接
func (r *RedisClient) Close() error {
	return r.client.Close()
}

// Ping 探活，供健康检查注册表使用
func (r *RedisClient) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// Client 返回底层 go-redis 客户端。
//
// 本包只保留连接管理（NewRedis/Close）与探活（Ping），不再包装任何命令：
// 一层只改了签名的透传，既没加行为也没加约束，却要求每次用新命令都先来这里补一个方法。
// 业务需要用 Redis 时直接拿这个客户端调 go-redis（命令齐全、文档现成），
// 真出现「多处重复的复合操作」再往上抽。
func (r *RedisClient) Client() *redis.Client {
	return r.client
}
