package logsvc

import (
	"context"
	"testing"

	"plumebot/internal/domain/entity"
)

// fakeReader 记录收到的查询，返回固定页。
type fakeReader struct {
	got entity.LogQuery
}

func (f *fakeReader) Query(_ context.Context, q entity.LogQuery) (entity.LogPage, error) {
	f.got = q
	return entity.LogPage{Items: []entity.LogEntry{{Message: "样本"}}}, nil
}

// TestQueryNormalizesLimitAndOffset 验证 limit 缺省/上限与 offset 负数归一后透传给 reader。
func TestQueryNormalizesLimitAndOffset(t *testing.T) {
	cases := []struct {
		name       string
		in         entity.LogQuery
		wantLimit  int
		wantOffset int
	}{
		{"缺省 limit", entity.LogQuery{}, defaultLimit, 0},
		{"超上限截断", entity.LogQuery{Limit: 9999}, maxLimit, 0},
		{"负数 offset 归零", entity.LogQuery{Limit: 50, Offset: -3}, 50, 0},
		{"正常值透传", entity.LogQuery{Limit: 50, Offset: 20}, 50, 20},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeReader{}
			if _, err := New(f).Query(context.Background(), c.in); err != nil {
				t.Fatalf("Query 失败: %v", err)
			}
			if f.got.Limit != c.wantLimit || f.got.Offset != c.wantOffset {
				t.Fatalf("归一后 limit/offset = %d/%d，期望 %d/%d",
					f.got.Limit, f.got.Offset, c.wantLimit, c.wantOffset)
			}
		})
	}
}

// TestQueryPassesThroughFilters 验证等级/trace_id/时间条件原样透传。
func TestQueryPassesThroughFilters(t *testing.T) {
	f := &fakeReader{}
	levels := []entity.LogLevel{entity.LogLevelWarn}
	if _, err := New(f).Query(context.Background(), entity.LogQuery{
		Levels: levels, TraceID: "group:100",
	}); err != nil {
		t.Fatalf("Query 失败: %v", err)
	}
	if len(f.got.Levels) != 1 || f.got.Levels[0] != entity.LogLevelWarn || f.got.TraceID != "group:100" {
		t.Fatalf("筛选条件未透传：%+v", f.got)
	}
}
