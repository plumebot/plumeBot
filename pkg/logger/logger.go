// Package logger 基于 uber/zap 的全局单例日志包。
// 日志按级别分文件写入 ~/.plumebot/logs/，单文件超过上限自动切分。
package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Config 日志初始化配置。零值字段使用默认值。
type Config struct {
	Level      string // debug | info | warn | error，默认 "info"
	Dir        string // 日志目录，默认 "~/.plumebot/logs"
	MaxSize    int    // 单文件最大 MB，默认 10
	MaxBackups int    // 最大备份数，默认 5
	MaxAge     int    // 最大保留天数，默认 30
}

var zl *zap.Logger

// Init 初始化全局 logger，按级别创建独立日志文件。
// Level 无效时降级为 info。
func Init(cfg Config) {
	if cfg.Dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		cfg.Dir = filepath.Join(home, ".plumebot", "logs")
	}
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 10
	}
	if cfg.MaxBackups <= 0 {
		cfg.MaxBackups = 5
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 30
	}

	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return
	}

	globalLevel := parseLevel(cfg.Level)
	atomLevel := zap.NewAtomicLevelAt(globalLevel)
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())

	cores := []zapcore.Core{
		newLevelCore(encoder, cfg, "debug", zapcore.DebugLevel, atomLevel),
		newLevelCore(encoder, cfg, "info", zapcore.InfoLevel, atomLevel),
		newLevelCore(encoder, cfg, "warn", zapcore.WarnLevel, atomLevel),
		newLevelCore(encoder, cfg, "error", zapcore.ErrorLevel, atomLevel),
	}

	tee := zapcore.NewTee(cores...)
	zl = zap.New(tee, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
}

func newLevelCore(encoder zapcore.Encoder, cfg Config, name string, lvl zapcore.Level, atom zap.AtomicLevel) zapcore.Core {
	writer := zapcore.AddSync(&lumberjack.Logger{
		Filename:   filepath.Join(cfg.Dir, name+".log"),
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   false,
	})
	return zapcore.NewCore(encoder, writer, &levelGate{level: lvl, atom: atom})
}

type levelGate struct {
	level zapcore.Level
	atom  zap.AtomicLevel
}

func (g *levelGate) Enabled(lvl zapcore.Level) bool {
	return lvl == g.level && g.atom.Enabled(lvl)
}

func parseLevel(s string) zapcore.Level {
	switch s {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// Sync 刷新所有日志缓冲区，应在 main 中 defer 调用。
func Sync() {
	if zl != nil {
		zl.Sync()
	}
}

// L 返回底层 *zap.Logger，供需要直接使用 zap API 的场景。
func L() *zap.Logger {
	return zl
}

// 各级别日志方法在全局 logger 未初始化（zl == nil，如组件单测）时安全丢弃，
// 不 panic；生产路径 main 后 Init 行为不变。Fatal 兜底直接退出保证语义。

// Debug 输出 debug 级别日志。
func Debug(msg string, fields ...zap.Field) {
	if zl == nil {
		return
	}
	zl.Debug(msg, fields...)
}

// Info 输出 info 级别日志。
func Info(msg string, fields ...zap.Field) {
	if zl == nil {
		return
	}
	zl.Info(msg, fields...)
}

// Warn 输出 warn 级别日志。
func Warn(msg string, fields ...zap.Field) {
	if zl == nil {
		return
	}
	zl.Warn(msg, fields...)
}

// Error 输出 error 级别日志。
func Error(msg string, fields ...zap.Field) {
	if zl == nil {
		return
	}
	zl.Error(msg, fields...)
}

// Fatal 输出 fatal 级别日志后调用 os.Exit(1)。
func Fatal(msg string, fields ...zap.Field) {
	if zl == nil {
		os.Exit(1)
	}
	zl.Fatal(msg, fields...)
}

// --- Field 便捷构造函数 ---

// S 创建 string Field。
func S(k, v string) zap.Field { return zap.String(k, v) }

// I 创建 int Field。
func I(k string, v int) zap.Field { return zap.Int(k, v) }

// I64 创建 int64 Field。
func I64(k string, v int64) zap.Field { return zap.Int64(k, v) }

// F64 创建 float64 Field。
func F64(k string, v float64) zap.Field { return zap.Float64(k, v) }

// B 创建 bool Field。
func B(k string, v bool) zap.Field { return zap.Bool(k, v) }

// Err 创建 error Field。
func Err(v error) zap.Field { return zap.Error(v) }

// Any 创建任意类型 Field。
func Any(k string, v interface{}) zap.Field { return zap.Any(k, v) }
