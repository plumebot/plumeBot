// Package logfile 实现 domain.LogReader：读取 pkg/logger 产出的 JSON 日志文件
//（~/.plumebot/logs/{debug,info,warn,error,fatal}.log + lumberjack 滚动备份），
// 供管理后端的日志浏览页按「等级 × 时间段 × trace_id」组合筛选（架构 §17.6）。
//
// 零第三方依赖（纯标准库）：日志目录经构造函数注入，本包不解析默认目录；
// 目录内的 gin.log（文本格式）与其它非级别文件天然不被匹配。
package logfile

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"plumebot/internal/domain/entity"
)

const (
	// defaultLimit 是未指定 Limit 时的返回上限（service 侧另有归一，此处兜底直接调用本包的情形）。
	defaultLimit = 200
	// maxLineBytes 是单行扫描上限（含 stacktrace 的长行预留 1MB）。
	maxLineBytes = 1024 * 1024
)

// levelFiles 是等级 → 文件前缀列表。error 语义覆盖 fatal.log（fatal 并入错误展示）。
var levelFiles = map[entity.LogLevel][]string{
	entity.LogLevelDebug: {"debug"},
	entity.LogLevelInfo:  {"info"},
	entity.LogLevelWarn:  {"warn"},
	entity.LogLevelError: {"error", "fatal"},
}

// Reader 读取注入目录下的 JSON 日志文件（无状态，可并发使用）。
type Reader struct {
	dir string
}

// New 创建日志读取器；dir 为日志目录（调用方经 logger.Dir() 取实际生效目录后传入）。
func New(dir string) *Reader {
	return &Reader{dir: dir}
}

// Query 实现 domain.LogReader：按条件返回最新在前的一页日志。
// 策略（架构 §17.6）：按文件 mtime 降序扫描（最近写入的文件先扫，当前文件通常最新）
// → 文件内从末行向前（追加写使文件内 ts 单调递增，遇到早于 Begin 的行即可停止读该文件）
// → 收集够 Offset+Limit+1 条即止 → **按 ts 稳定排序**后切页（消除跨级别文件写入时间
// 交错带来的顺序抖动，不依赖文件系统时间戳精度）。
func (r *Reader) Query(_ context.Context, q entity.LogQuery) (entity.LogPage, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	offset := max(q.Offset, 0)
	need := limit + offset + 1 // 多读一条用于判定 has_more

	files, err := r.matchFiles(q.Levels)
	if err != nil {
		return entity.LogPage{}, err
	}

	var matched []entity.LogEntry
scan:
	for _, f := range files {
		lines, err := readLines(f.path)
		if err != nil {
			continue // 读取失败（被清理/权限）跳过该文件，不阻塞浏览
		}
		for i := len(lines) - 1; i >= 0; i-- { // 文件内从末行向前：最新在前
			e, ok := parseLine(lines[i])
			if !ok {
				continue // 损坏/非 JSON 行静默跳过
			}
			if q.Begin != nil && e.TS.Before(*q.Begin) {
				break // 文件内 ts 递增：更早的行不必再看，换下一个文件
			}
			if !matchTime(e, q) || !matchTrace(e, q) {
				continue
			}
			matched = append(matched, e)
			if len(matched) >= need {
				break scan
			}
		}
	}

	sort.SliceStable(matched, func(i, j int) bool { return matched[i].TS.After(matched[j].TS) })
	hasMore := len(matched) > offset+limit
	end := min(offset+limit, len(matched))
	items := matched[min(offset, end):end]
	if items == nil {
		items = []entity.LogEntry{}
	}
	return entity.LogPage{Items: items, HasMore: hasMore}, nil
}

// logFile 是候选日志文件及其修改时间（用于按新旧排序与时间范围跳过）。
type logFile struct {
	path string
	mod  time.Time
}

// matchFiles 返回符合等级筛选的日志文件，按 mtime 降序（最新文件在前）。
// 无 Levels 时读取全部级别；文件名匹配 "<level>.log"（当前）与 "<level>-<时间戳>.log"（滚动备份）。
func (r *Reader) matchFiles(levels []entity.LogLevel) ([]logFile, error) {
	prefixes := make([]string, 0, len(levels))
	if len(levels) == 0 {
		for lv := range levelFiles {
			prefixes = append(prefixes, levelFiles[lv]...)
		}
	} else {
		for _, lv := range levels {
			prefixes = append(prefixes, levelFiles[lv]...)
		}
	}

	entries, err := os.ReadDir(r.dir)
	if err != nil {
		// 目录不存在（尚未产生日志）视为空结果，不报错阻塞浏览页。
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var files []logFile
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".log") || !hasLevelPrefix(de.Name(), prefixes) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		files = append(files, logFile{path: filepath.Join(r.dir, de.Name()), mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	return files, nil
}

// hasLevelPrefix 判断文件名是否以某个等级前缀开头且其后是 ".log"（当前文件）
// 或 "-"（lumberjack 备份名，如 info-2026-09-21T00-00-00.000.log），
// 避免误匹配（gin.log 不以前缀开头，天然排除）。
func hasLevelPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if !strings.HasPrefix(name, p) {
			continue
		}
		rest := name[len(p):]
		if rest == ".log" || strings.HasPrefix(rest, "-") {
			return true
		}
	}
	return false
}

// readLines 读取文件全部行（单文件 ≤10MB，逐文件读取峰值可控）。
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// parseLine 解析一行 zap JSON 日志；非 JSON/缺 ts 的行返回 ok=false（静默跳过）。
func parseLine(line string) (entity.LogEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return entity.LogEntry{}, false
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return entity.LogEntry{}, false
	}
	ts, ok := parseTS(raw["ts"])
	if !ok {
		return entity.LogEntry{}, false
	}
	e := entity.LogEntry{
		TS:      ts,
		Level:   toString(raw["level"]),
		Message: toString(raw["msg"]),
	}
	if tid, ok := raw["trace_id"].(string); ok {
		e.TraceID = tid
	}
	delete(raw, "ts")
	delete(raw, "level")
	delete(raw, "msg")
	delete(raw, "trace_id")
	if len(raw) > 0 {
		e.Fields = raw
	}
	return e, true
}

// parseTS 解析 zap 的 ts 字段：NewProductionEncoderConfig 默认 EpochTimeEncoder → 浮点秒。
// 兼容字符串形式（自定义 encoder 场景），使日志格式演进时不致整页失效。
func parseTS(v any) (time.Time, bool) {
	switch t := v.(type) {
	case float64:
		sec := int64(t)
		nsec := int64((t - float64(sec)) * 1e9)
		return time.Unix(sec, nsec), true
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// toString 把 JSON 值转字符串（level/msg 正常情况下就是字符串）。
func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// matchTime 判断条目是否落在 [Begin, End] 内。
func matchTime(e entity.LogEntry, q entity.LogQuery) bool {
	if q.Begin != nil && e.TS.Before(*q.Begin) {
		return false
	}
	if q.End != nil && e.TS.After(*q.End) {
		return false
	}
	return true
}

// matchTrace 判断条目是否匹配 trace_id 过滤（空 = 不过滤）。
func matchTrace(e entity.LogEntry, q entity.LogQuery) bool {
	return q.TraceID == "" || e.TraceID == q.TraceID
}
