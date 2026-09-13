package sqlite

// 并发读写回归测试（数据面与 web 管理面共用同一 *sql.DB）。
//
// 背景：Open 曾以裸 DSN（无 _busy_timeout / _journal_mode）打开库，SQLite 默认
// rollback journal + busy_timeout=0：任一连接持写锁时，其余连接的读写立即返回
// SQLITE_BUSY（"database is locked"）。实测 8 goroutine × 120 轮并发下 960 次操作
// 失败 902 次——表现为管理页请求间歇 500、按钮「点了没反应」、群配置保存不落库
// （改 auto 不生效）、消息管线落库失败导致回复静默丢失。
// 修复：DSN 加 _busy_timeout=5000 + _journal_mode=WAL（见 storage.go Open）。

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

// TestConcurrentReadWriteNoBusy 并发读写不应出现 SQLITE_BUSY：
// 混合模拟消息管线写（SaveMessage）与管理面读写（GetGroupConfig/UpsertGroupConfig/ListGroupConfigs）。
func TestConcurrentReadWriteNoBusy(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	const goroutines, rounds = 8, 60
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		ok    int
		busy  int
		other int
	)
	record := func(err error) {
		if err == nil {
			mu.Lock()
			ok++
			mu.Unlock()
			return
		}
		mu.Lock()
		if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
			busy++
		} else {
			other++
		}
		mu.Unlock()
	}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				record(st.SaveMessage(ctx, entity.Message{
					MessageID:   fmt.Sprintf("m-%d-%d", n, j),
					GroupID:     fmt.Sprintf("g%d", n%3),
					UserID:      "u1",
					Parts:       []entity.ContentPart{{Type: entity.PartTypeText, Text: "hi"}},
					Timestamp:   int64(j),
					MessageType: "group",
				}))
				record(st.UpsertGroupConfig(ctx, entity.GroupConfig{GroupID: "g1", Mode: "auto"}))

				if _, err := st.GetGroupConfig(ctx, "g1"); err != nil && !errors.Is(err, domain.ErrNotFound) {
					record(err)
				}
				if _, err := st.ListGroupConfigs(ctx); err != nil {
					record(err)
				}
			}
		}(i)
	}
	wg.Wait()

	t.Logf("并发读写 ok=%d busy=%d otherErr=%d", ok, busy, other)
	if busy > 0 {
		t.Fatalf("出现 %d 次 SQLITE_BUSY（database is locked）：DSN 缺 busy_timeout/WAL 导致并发锁冲突", busy)
	}
	if other > 0 {
		t.Fatalf("出现 %d 次非锁类错误", other)
	}
}
