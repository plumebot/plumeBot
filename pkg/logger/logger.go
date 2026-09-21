// Package logger 基于 uber/zap 的全局单例日志包。
// 日志按级别分文件写入 ~/.plumebot/logs/（debug/info/warn/error/fatal + gin），
// 单文件超过上限自动切分（lumberjack 统一滚动：10MB/5 备份/30 天）。
// 业务日志仅写文件不写终端；gin 每请求访问日志经 GinAccessWriter 同样落文件。
// 日志规范见 docs/architecture.md §17。
package logger

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// levelAtom 是全局级别门控，Init 时创建，SetLevel 可动态调整（main 在配置加载后应用 log.level）。
// fatal 核心不共用本门控（FatalLevel 恒开，见 Init）。
var levelAtom zap.AtomicLevel

// defaultDirRel 是默认日志目录相对用户主目录的路径（~/.plumebot/logs）。
// 常量字符串（.plumebot/logs，跨平台分隔符）——注意 filepath.Join 非常量不可用于 const。
const defaultDirRel = ".plumebot/logs"

// Init 初始化全局 logger，按级别创建独立日志文件。
// Level 无效时降级为 info。fatal.log 恒开（Fatal 是记账后退出，不应被级别门控吞掉）。
func Init(cfg Config) {
	cfg = withDefaults(cfg)
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return
	}

	globalLevel := parseLevel(cfg.Level)
	levelAtom = zap.NewAtomicLevelAt(globalLevel)
	encoder := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())

	cores := []zapcore.Core{
		newLevelCore(encoder, cfg, "debug", zapcore.DebugLevel, levelAtom),
		newLevelCore(encoder, cfg, "info", zapcore.InfoLevel, levelAtom),
		newLevelCore(encoder, cfg, "warn", zapcore.WarnLevel, levelAtom),
		newLevelCore(encoder, cfg, "error", zapcore.ErrorLevel, levelAtom),
		// fatal 恒开：FatalLevel 始终 enabled（不随 log.level 门控），否则 16 处启动
		// logger.Fatal 的失败原因不会出现在任何日志文件里（架构 §17.1 fatal.log）。
		newLevelCore(encoder, cfg, "fatal", zapcore.FatalLevel, zap.NewAtomicLevelAt(zapcore.FatalLevel)),
	}

	tee := zapcore.NewTee(cores...)
	zl = zap.New(tee, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
}

// SetLevel 动态调整全局日志级别（debug|info|warn|error，无效值降级 info）。
// main 先以默认级别 Init（使配置加载阶段的日志可见），读到 log.level 后再应用。
// Init 之前调用为 no-op。
func SetLevel(level string) {
	if zl == nil {
		return
	}
	levelAtom.SetLevel(parseLevel(level))
}

// withDefaults 填充 Config 的空值字段（目录/滚动参数）。供 Init 与 GinAccessWriter 共用。
func withDefaults(cfg Config) Config {
	if cfg.Dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		cfg.Dir = filepath.Join(home, defaultDirRel)
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
	return cfg
}

// GinAccessWriter 返回 gin 每请求访问日志的写入器（架构 §17.1 gin.log）。
// 与业务日志共用同一个日志目录与滚动策略（lumberjack 10MB/5 备份/30 天），
// 使管理后端 /ping 健康轮询等请求日志落文件而非刷终端。调用方（gin.LoggerWithWriter）
// 负责协议；初始化失败时写入器回退 os.Discard（gin 不 panic）。
func GinAccessWriter() io.Writer {
	cfg := withDefaults(Config{})
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return io.Discard
	}
	return &lumberjack.Logger{
		Filename:   filepath.Join(cfg.Dir, "gin.log"),
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   false,
	}
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

// Dir 返回实际生效的日志目录（Init 未显式指定 Dir 时为 ~/.plumebot/logs）。
// 供日志读取侧（infra/logfile）构造注入使用，避免各处重复解析默认目录。
func Dir() string {
	return withDefaults(Config{}).Dir
}

// ctxKey 是 ctx 注入派生 logger 的私有键（避免与其他包键冲突）。
type ctxKey struct{}

// Context 把派生 logger（附带 fields，如 trace_id）注入 ctx；下游经 From(ctx) 取出使用。
// 未 Init 时注入 no-op logger（From 仍可安全取出并调用）。
func Context(ctx context.Context, fields ...zap.Field) context.Context {
	return context.WithValue(ctx, ctxKey{}, derived(fields...))
}

// From 取出 ctx 中注入的派生 logger；未注入回退全局 logger（行为与包级 Debug/Info/... 一致，
// 便于渐进接入 trace_id 而不必一次改完所有调用）；两者皆无（未 Init）返回 no-op logger，
// 保证返回值非 nil，调用方无需判空。
func From(ctx context.Context) *zap.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok && l != nil {
		return l
	}
	if zl != nil {
		return zl
	}
	return zap.NewNop()
}

// derived 构造附带 fields 的派生 logger；无字段时直接返回基准 logger（省一次派生）。
func derived(fields ...zap.Field) *zap.Logger {
	base := zl
	if base == nil {
		base = zap.NewNop()
	}
	if len(fields) == 0 {
		return base
	}
	return base.With(fields...)
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
// Init 之前（zl == nil，如 main.go 配置加载失败）fallback 打 stderr 保证终端可见——
// 否则启动致命错误完全无痕，只能靠 exit code 判断（架构 §17.1）。
func Fatal(msg string, fields ...zap.Field) {
	if zl == nil {
		var sb strings.Builder
		sb.WriteString("[fatal] ")
		sb.WriteString(msg)
		for _, f := range fields {
			sb.WriteString(" ")
			sb.WriteString(f.Key)
			sb.WriteString("=")
			sb.WriteString(fieldValue(f))
		}
		fmt.Fprintln(os.Stderr, sb.String())
		os.Exit(1)
	}
	zl.Fatal(msg, fields...)
}

// fieldValue 提取 zap.Field 的可打印字符串值。
// zap.Field 把值存在不同私有槽位（String/Integer/Interface），无统一取值 API，
// 这里按类型分发覆盖本仓库用到的构造（S/I/I64/B/Err/Any）；未知类型回退 UnknownType。
func fieldValue(f zap.Field) string {
	switch f.Type {
	case zapcore.StringType:
		return f.String
	case zapcore.Int64Type:
		return strconv.FormatInt(f.Integer, 10)
	case zapcore.Int32Type:
		return strconv.Itoa(int(f.Integer))
	case zapcore.BoolType:
		return strconv.FormatBool(f.Integer != 0)
	case zapcore.ErrorType, zapcore.StringerType, zapcore.ReflectType, zapcore.ObjectMarshalerType, zapcore.NamespaceType:
		if f.Interface != nil {
			return fmt.Sprint(f.Interface)
		}
	}
	return "<unknown>"
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
