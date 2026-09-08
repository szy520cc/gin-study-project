package data

import (
	"context"
	"encoding/json"
	"time"

	"myproject/internal/model"
	"myproject/internal/resource"
	"myproject/pkg/cache"
)

// 缓存 key 前缀与 TTL（对齐资料 RedisEvalKey / RedisCurrentVersionKey）。
// 版本指针（无 TTL）+ 版本化快照/草稿快照（7 天 TTL）。
const (
	redisCurVerPrefix  = "cur_ver"
	redisEvalKeyPrefix = "eval_key"
	snapshotTTL        = 7 * 24 * time.Hour
)

func curVerKey(pack, ext string) string { return redisCurVerPrefix + "_" + pack + "_" + ext }
func evalKey(pack, ext, version string) string {
	return redisEvalKeyPrefix + "_" + pack + "_" + ext + "_" + version
}
func latestKey(pack, ext string) string { return redisEvalKeyPrefix + "_" + pack + "_" + ext + "_latest" }

// redis 返回 Redis 客户端；未启用返回 nil（调用方降级为直查 DB）。
func redis() *cache.RedisClient {
	return resource.Redis()
}

// SetCurVer 写版本指针（无 TTL）。Redis 关闭时静默降级。
func SetCurVer(ctx context.Context, pack, ext, version string) error {
	rc := redis()
	if rc == nil {
		return nil
	}
	return rc.Client().Set(ctx, curVerKey(pack, ext), version, 0).Err()
}

// GetCurVer 读版本指针。miss / Redis 关闭返回空串。
func GetCurVer(ctx context.Context, pack, ext string) string {
	rc := redis()
	if rc == nil {
		return ""
	}
	v, err := rc.Client().Get(ctx, curVerKey(pack, ext)).Result()
	if err != nil {
		return ""
	}
	return v
}

// SetSnapshot 写快照（7 天 TTL）。Redis 关闭时静默降级。
func SetSnapshot(ctx context.Context, pack, ext, version string, snap *model.ConfigSnapshot) error {
	rc := redis()
	if rc == nil {
		return nil
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return rc.Client().Set(ctx, evalKey(pack, ext, version), b, snapshotTTL).Err()
}

// GetSnapshot 读快照。miss / 反序列化失败 / Redis 关闭返回 nil。
func GetSnapshot(ctx context.Context, pack, ext, version string) *model.ConfigSnapshot {
	rc := redis()
	if rc == nil {
		return nil
	}
	b, err := rc.Client().Get(ctx, evalKey(pack, ext, version)).Result()
	if err != nil {
		return nil
	}
	var snap model.ConfigSnapshot
	if err := json.Unmarshal([]byte(b), &snap); err != nil {
		return nil
	}
	if snap.Extension == "" {
		return nil
	}
	return &snap
}

// GetLatestSnapshot 读草稿快照（offline 用）。miss 返回 nil。
func GetLatestSnapshot(ctx context.Context, pack, ext string) *model.ConfigSnapshot {
	rc := redis()
	if rc == nil {
		return nil
	}
	b, err := rc.Client().Get(ctx, latestKey(pack, ext)).Result()
	if err != nil {
		return nil
	}
	var snap model.ConfigSnapshot
	if err := json.Unmarshal([]byte(b), &snap); err != nil {
		return nil
	}
	if snap.Extension == "" {
		return nil
	}
	return &snap
}

// SetLatestSnapshot 写草稿快照（offline 回源回填，7 天 TTL）。
func SetLatestSnapshot(ctx context.Context, pack, ext string, snap *model.ConfigSnapshot) error {
	rc := redis()
	if rc == nil {
		return nil
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return rc.Client().Set(ctx, latestKey(pack, ext), b, snapshotTTL).Err()
}

// DelLatest 删除草稿快照（编辑必删）。Redis 关闭静默降级。
func DelLatest(ctx context.Context, pack, ext string) error {
	rc := redis()
	if rc == nil {
		return nil
	}
	return rc.Client().Del(ctx, latestKey(pack, ext)).Err()
}
