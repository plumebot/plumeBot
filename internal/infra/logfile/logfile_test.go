package logfile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"plumebot/internal/domain/entity"
)

// base 是测试样本的时间基准（避免依赖真实时钟）。
var base = time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

// writeLog 向 dir 追加一行 zap 形状的 JSON 日志（ts=EpochTimeEncoder 浮点秒）。
func writeLog(t *testing.T, dir, file string, ts time.Time, level, msg string, extra map[string]any) {
	t.Helper()
	line := map[string]any{
		"ts":    float64(ts.UnixNano()) / 1e9,
		"level": level,
		"msg":   msg,
	}
	for k, v := range extra {
		line[k] = v
	}
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("序列化样本失败: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dir, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("打开日志文件失败: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatalf("写入日志失败: %v", err)
	}
}

// writeRaw 追加原始文本行（用于损坏行/非 JSON 场景）。
func writeRaw(t *testing.T, dir, file, line string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, file), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("打开文件失败: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
}

// newReader 造一个含多级别样本的目录：info 3 条（含 trace_id）、warn 1、error 1、fatal 1、debug 1。
func newReader(t *testing.T) *Reader {
	t.Helper()
	dir := t.TempDir()
	writeLog(t, dir, "info.log", base.Add(1*time.Minute), "info", "消息A", map[string]any{"trace_id": "group:100"})
	writeLog(t, dir, "info.log", base.Add(2*time.Minute), "info", "消息B", map[string]any{"trace_id": "group:100"})
	writeLog(t, dir, "info.log", base.Add(3*time.Minute), "info", "消息C", map[string]any{"trace_id": "private:200"})
	writeLog(t, dir, "warn.log", base.Add(4*time.Minute), "warn", "限流", map[string]any{"trace_id": "group:100"})
	writeLog(t, dir, "error.log", base.Add(5*time.Minute), "error", "故障", map[string]any{"caller": "x.go:1"})
	writeLog(t, dir, "fatal.log", base.Add(6*time.Minute), "fatal", "启动失败", nil)
	writeLog(t, dir, "debug.log", base.Add(7*time.Minute), "debug", "细节", nil)
	// gin.log 为文本格式，不应被读取；另放一个无等级前缀的 JSON 文件同样应被忽略。
	writeRaw(t, dir, "gin.log", `[GIN] 2026/09/21 - 200 | 1ms | 127.0.0.1 | GET "/ping"`)
	writeLog(t, dir, "other.log", base.Add(8*time.Minute), "info", "不应出现", nil)
	return New(dir)
}

func query(t *testing.T, r *Reader, q entity.LogQuery) entity.LogPage {
	t.Helper()
	q.Limit = 100
	page, err := r.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query 失败: %v", err)
	}
	return page
}

// TestQueryAllLevelsNewestFirst 验证全部级别合并读取，且按时间倒序（最新在前）。
func TestQueryAllLevelsNewestFirst(t *testing.T) {
	page := query(t, newReader(t), entity.LogQuery{})

	var msgs []string
	for _, e := range page.Items {
		msgs = append(msgs, e.Message)
	}
	want := []string{"细节", "启动失败", "故障", "限流", "消息C", "消息B", "消息A"}
	if len(msgs) != len(want) {
		t.Fatalf("条数 = %d，期望 %d（%v）——gin.log/other.log 不应被读取", len(msgs), len(want), msgs)
	}
	for i := range want {
		if msgs[i] != want[i] {
			t.Fatalf("第 %d 条 = %q，期望 %q（%v）", i, msgs[i], want[i], msgs)
		}
	}
	// 条目自带 level（前端染色），且 ts 被正确解析。
	if page.Items[0].Level != "debug" || page.Items[0].TS.Unix() != base.Add(7*time.Minute).Unix() {
		t.Fatalf("首条 level/ts 不符：%+v", page.Items[0])
	}
}

// TestQueryErrorIncludesFatal 验证 error 语义覆盖 fatal.log。
func TestQueryErrorIncludesFatal(t *testing.T) {
	page := query(t, newReader(t), entity.LogQuery{Levels: []entity.LogLevel{entity.LogLevelError}})

	if len(page.Items) != 2 {
		t.Fatalf("error 语义应含 error+fatal 共 2 条，实际 %d", len(page.Items))
	}
	if page.Items[0].Level != "fatal" || page.Items[1].Level != "error" {
		t.Fatalf("error+fatal 排序不符：%+v", page.Items)
	}
}

// TestQueryTraceID 验证 trace_id 精确过滤（跨级别）。
func TestQueryTraceID(t *testing.T) {
	page := query(t, newReader(t), entity.LogQuery{TraceID: "group:100"})

	if len(page.Items) != 3 {
		t.Fatalf("trace_id=group:100 应命中 3 条，实际 %d", len(page.Items))
	}
	for _, e := range page.Items {
		if e.TraceID != "group:100" {
			t.Fatalf("命中非目标 trace_id：%+v", e)
		}
	}
}

// TestQueryTimeRange 验证时间段边界（含端点）。
func TestQueryTimeRange(t *testing.T) {
	r := newReader(t)
	begin := base.Add(2 * time.Minute)
	end := base.Add(4 * time.Minute)
	page := query(t, r, entity.LogQuery{Begin: &begin, End: &end})

	var msgs []string
	for _, e := range page.Items {
		msgs = append(msgs, e.Message)
	}
	want := []string{"限流", "消息C", "消息B"}
	if len(msgs) != len(want) {
		t.Fatalf("时间范围命中 %v，期望 %v", msgs, want)
	}
	for i := range want {
		if msgs[i] != want[i] {
			t.Fatalf("时间范围第 %d 条 = %q，期望 %q", i, msgs[i], want[i])
		}
	}
}

// TestQueryPaging 验证 limit/offset/has_more 语义。
func TestQueryPaging(t *testing.T) {
	r := newReader(t)

	first, err := r.Query(context.Background(), entity.LogQuery{Limit: 3})
	if err != nil {
		t.Fatalf("Query 失败: %v", err)
	}
	if len(first.Items) != 3 || !first.HasMore {
		t.Fatalf("首页应为 3 条且 has_more=true，实际 %d/%v", len(first.Items), first.HasMore)
	}
	if first.Items[0].Message != "细节" {
		t.Fatalf("首页首条应为最新「细节」，实际 %q", first.Items[0].Message)
	}

	second, err := r.Query(context.Background(), entity.LogQuery{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("Query 失败: %v", err)
	}
	if len(second.Items) != 3 || second.Items[0].Message != "限流" {
		t.Fatalf("第二页首条应为「限流」，实际 %+v", second.Items)
	}

	last, err := r.Query(context.Background(), entity.LogQuery{Limit: 3, Offset: 6})
	if err != nil {
		t.Fatalf("Query 失败: %v", err)
	}
	if len(last.Items) != 1 || last.Items[0].Message != "消息A" || last.HasMore {
		t.Fatalf("末页应为 1 条且 has_more=false，实际 %+v has_more=%v", last.Items, last.HasMore)
	}
}

// TestQuerySkipsBrokenLines 验证损坏行/非 JSON 行被静默跳过，不影响其余条目。
func TestQuerySkipsBrokenLines(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "info.log", base, "info", "正常", nil)
	writeRaw(t, dir, "info.log", "{ 这不是合法 JSON")
	writeRaw(t, dir, "info.log", "[GIN] 文本行")
	writeLog(t, dir, "info.log", base.Add(time.Minute), "info", "仍然可读", nil)

	page := query(t, New(dir), entity.LogQuery{})
	if len(page.Items) != 2 || page.Items[0].Message != "仍然可读" {
		t.Fatalf("损坏行应被跳过且不影响其余条目，实际 %+v", page.Items)
	}
}

// TestQueryMissingDir 验证日志目录不存在时返回空页而非错误（首次运行场景）。
func TestQueryMissingDir(t *testing.T) {
	page := query(t, New(filepath.Join(t.TempDir(), "not-exist")), entity.LogQuery{})
	if len(page.Items) != 0 || page.HasMore {
		t.Fatalf("目录缺失应返回空页，实际 %+v", page)
	}
}

// TestHasLevelPrefix 验证文件名匹配边界（gin.log 与无关文件不被误匹配）。
func TestHasLevelPrefix(t *testing.T) {
	prefixes := levelFiles[entity.LogLevelInfo]
	cases := map[string]bool{
		"info.log":                         true,
		"info-2026-09-21T10-00-00.000.log": true,
		"gin.log":                          false,
		"information.log":                  false,
		"info.log.1":                       false,
	}
	for name, want := range cases {
		if got := hasLevelPrefix(name, prefixes); got != want {
			t.Fatalf("hasLevelPrefix(%q) = %v，期望 %v", name, got, want)
		}
	}
}
