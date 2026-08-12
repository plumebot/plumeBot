package memory

import (
	"context"
	"os"
	"testing"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/logger"
)

// TestMain 初始化全局 logger（error 级，避免画像加载失败日志触发 nil 指针）。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "plumebot-test-logs")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	logger.Init(logger.Config{Level: "error", Dir: dir})
	os.Exit(m.Run())
}

// fakeStorage 嵌入 domain.Storage 接口，实现 PersistMessage / ProfileCache 用到的几个方法。
// 其余方法嵌入 nil 接口，调用即 panic（但本测试不会调用）。
type fakeStorage struct {
	domain.Storage
	saved     []entity.Message
	groupProf map[string]*entity.GroupProfile
	groupGets int // 群画像查询次数

	archived []entity.Summary // SaveSummary 落库的归档摘要
}

func (f *fakeStorage) SaveMessage(_ context.Context, msg entity.Message) error {
	f.saved = append(f.saved, msg)
	return nil
}

func (f *fakeStorage) GetGroupProfile(_ context.Context, groupID string) (*entity.GroupProfile, error) {
	f.groupGets++
	if p, ok := f.groupProf[groupID]; ok {
		return p, nil
	}
	return nil, domain.ErrNotFound
}

func (f *fakeStorage) SaveSummary(_ context.Context, sum entity.Summary) error {
	f.archived = append(f.archived, sum)
	return nil
}

func (f *fakeStorage) ListSummaries(_ context.Context, chatID string, limit int) ([]entity.Summary, error) {
	var out []entity.Summary
	for i := len(f.archived) - 1; i >= 0; i-- {
		if f.archived[i].ChatID == chatID {
			out = append(out, f.archived[i])
			if len(out) == limit {
				break
			}
		}
	}
	// 反转回正序（旧→新），与 sqlite 实现一致。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// fakeSummarizer 返回预设输出的假摘要器；results 耗尽后返回默认 JSON。
// errAt 指定在第 N 次调用时返回 err；errAt=0 时若设置了 err，则每次调用都返回 err。
type fakeSummarizer struct {
	results []string // 依次返回的模型输出
	calls   int
	err     error
	errAt   int // 第 N 次调用时返回 err（0 = 不按次数触发）
}

func (f *fakeSummarizer) Summarize(_ context.Context, _ string, _ string) (string, error) {
	f.calls++
	if f.errAt > 0 {
		if f.calls == f.errAt {
			return "", f.err
		}
	} else if f.err != nil {
		return "", f.err
	}
	if len(f.results) > 0 {
		r := f.results[0]
		f.results = f.results[1:]
		return r, nil
	}
	return `{"summary":"s","keywords":["k"],"decisions":["d"]}`, nil
}

func TestPersistMessageWritesWindowAndStorage(t *testing.T) {
	store := &fakeStorage{}
	svc := NewMemoryService(NewWindow(), store, &fakeSummarizer{})
	msg := groupMsg("g1", "hi")

	full, err := svc.PersistMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("持久化失败: %v", err)
	}
	if full {
		t.Error("首条消息不应触发压缩")
	}
	if len(store.saved) != 1 || store.saved[0].PlainText() != "hi" {
		t.Errorf("SQLite 未收到消息: %+v", store.saved)
	}
	got, _ := svc.GetWindow(context.Background(), "g1")
	if len(got) != 1 {
		t.Errorf("窗口应包含消息: %+v", got)
	}
	// 持久化同时触达画像缓存：群画像被缓存（无画像也缓存为已查询）。
	if _, ok := svc.GetGroupProfile("g1"); !ok {
		t.Error("持久化应触达群画像缓存")
	}
}

func TestPersistMessageSignalsCompressionAtCap(t *testing.T) {
	store := &fakeStorage{}
	svc := NewMemoryService(NewWindow(), store, &fakeSummarizer{})
	for i := 0; i < WindowCap; i++ {
		full, err := svc.PersistMessage(context.Background(), groupMsg("g1", "m"))
		if err != nil {
			t.Fatalf("持久化失败: %v", err)
		}
		if i == WindowCap-1 && !full {
			t.Error("达到上限时应触发压缩信号")
		}
	}
	if len(store.saved) != WindowCap {
		t.Errorf("SQLite 应持久化 %d 条, 实际 %d", WindowCap, len(store.saved))
	}
}
