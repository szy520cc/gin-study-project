package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	applog "myproject/pkg/logger"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// Options MySQL 连接参数。
//
// 这里刻意不接收 internal/config 的结构体：pkg 是与业务无关的基础设施层，
// 反过来依赖应用配置会让它没法被单独复用，也会让 config 的字段改名波及到这里。
// 由调用方（internal/bootstrap）负责把配置翻译成 Options。
type Options struct {
	Host     string
	Port     int
	Username string
	Password string
	DBName   string

	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration

	// LogLevel 取值 silent / error / warn / info，其余按 warn 处理
	LogLevel string
	// SlowThreshold 慢查询阈值，<=0 时按 200ms
	SlowThreshold time.Duration
	// LogSQLParams 是否把 SQL 参数一起打进日志。
	//
	// 与 LogLevel 解耦：把级别调成 info 排查问题时，不该顺带把
	// 邮箱、手机号、口令哈希这些绑定参数写进日志文件。
	// 默认 false（只打带占位符的语句）。
	LogSQLParams bool
}

// NewMySQL 创建 MySQL 连接。
//
// 相比原实现的三点变化：
//  1. GORM 日志级别、慢查询阈值来自配置 —— 原来硬编码 logger.Info，
//     生产会打印每条 SQL 及其参数（性能损耗 + 用户数据泄露）；
//  2. SQL 日志经 slog 输出，与应用日志同格式、同文件；
//  3. 连接生命周期可配置，并显式开启 TranslateError 以便识别唯一键冲突。
func NewMySQL(opt Options) (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local&timeout=5s&readTimeout=10s&writeTimeout=10s",
		opt.Username, opt.Password, opt.Host, opt.Port, opt.DBName)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: newGormLogger(opt),
		// 让 gorm 把驱动错误翻译成 ErrDuplicatedKey 等哨兵错误，
		// repository 层才能把它转换成领域错误
		TranslateError: true,
		NamingStrategy: schema.NamingStrategy{SingularTable: false},
		// 关闭默认事务可以提升写入性能，但会改变单条写入的语义，这里保持默认
	})
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取数据库实例失败: %w", err)
	}

	sqlDB.SetMaxIdleConns(opt.MaxIdleConns)
	sqlDB.SetMaxOpenConns(opt.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(opt.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("数据库 ping 失败: %w", err)
	}

	return db, nil
}

// gormLogger 把 GORM 日志接到应用 logger。
//
// 不用 gormlogger.New(writer, config)：它把 Info/Warn/Error 三条路径
// 全塞进同一个 Printf，慢查询和 SQL 错误最终都会以 Info 级别落盘 ——
// 生产按 level>=warn 检索日志时一条都看不到。
// 自己实现 Interface 才能保留级别，并且能从 ctx 取到带 request_id 的 logger。
type gormLogger struct {
	level         gormlogger.LogLevel
	slowThreshold time.Duration
	logSQLParams  bool
}

func newGormLogger(opt Options) gormlogger.Interface {
	slowThreshold := opt.SlowThreshold
	if slowThreshold <= 0 {
		slowThreshold = 200 * time.Millisecond
	}
	return gormLogger{
		level:         parseGormLevel(opt.LogLevel),
		slowThreshold: slowThreshold,
		logSQLParams:  opt.LogSQLParams,
	}
}

func (l gormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	l.level = level
	return l
}

func (l gormLogger) Info(ctx context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Info {
		applog.C(ctx).Info("gorm", "detail", fmt.Sprintf(msg, args...))
	}
}

func (l gormLogger) Warn(ctx context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Warn {
		applog.C(ctx).Warn("gorm", "detail", fmt.Sprintf(msg, args...))
	}
}

func (l gormLogger) Error(ctx context.Context, msg string, args ...interface{}) {
	if l.level >= gormlogger.Error {
		applog.C(ctx).Error("gorm", "detail", fmt.Sprintf(msg, args...))
	}
}

// Trace 记录每条 SQL 的执行结果。级别划分：
// 出错 -> Error（「查不到」除外，那是正常业务分支）；超过慢查询阈值 -> Warn；其余 -> Info。
func (l gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()
	attrs := []any{"sql", sql, "rows", rows, "elapsed_ms", elapsed.Milliseconds()}

	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		if l.level >= gormlogger.Error {
			applog.C(ctx).Error("sql failed", append(attrs, "error", err.Error())...)
		}
	case elapsed > l.slowThreshold:
		if l.level >= gormlogger.Warn {
			applog.C(ctx).Warn("slow sql", append(attrs, "threshold_ms", l.slowThreshold.Milliseconds())...)
		}
	case l.level >= gormlogger.Info:
		applog.C(ctx).Info("sql", attrs...)
	}
}

// ParamsFilter 是 GORM 识别的可选接口：返回 nil 参数即让日志只输出带占位符的语句。
// 这是「SQL 参数不落盘」的实现位置。
func (l gormLogger) ParamsFilter(_ context.Context, sql string, params ...interface{}) (string, []interface{}) {
	if l.logSQLParams {
		return sql, params
	}
	return sql, nil
}

func parseGormLevel(level string) gormlogger.LogLevel {
	switch level {
	case "silent":
		return gormlogger.Silent
	case "error":
		return gormlogger.Error
	case "info":
		return gormlogger.Info
	default:
		return gormlogger.Warn
	}
}
