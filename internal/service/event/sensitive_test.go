package event

import (
	"context"
	"errors"
	"testing"

	"plumebot/internal/domain/entity"
)

// contentMsg 构造带文本内容的消息（groupMsg 的首参是 MessageID）。
func contentMsg(content string) entity.Message {
	m := groupMsg("m1")
	m.Parts = []entity.ContentPart{{Type: entity.PartTypeText, Text: content}}
	return m
}

func TestSensitiveWordHit(t *testing.T) {
	mw := sensitiveWordMiddleware(newSensitiveWordFilter([]string{"赌博"}))

	nextCalled := false
	next := func(_ context.Context, _ entity.Message) error {
		nextCalled = true
		return nil
	}

	err := mw(next)(context.Background(), contentMsg("网络赌博平台"))
	if err == nil {
		t.Fatal("命中敏感词应返回错误")
	}
	if !errors.Is(err, entity.ErrSensitiveWord) {
		t.Fatalf("errors.Is(err, ErrSensitiveWord) 不成立: %v", err)
	}
	var swErr *entity.SensitiveWordError
	if !errors.As(err, &swErr) || swErr.Word != "赌博" {
		t.Fatalf("命中词应可经 errors.As 取出: %v", err)
	}
	if nextCalled {
		t.Fatal("命中后不应继续执行 next")
	}
}

func TestSensitiveWordPass(t *testing.T) {
	mw := sensitiveWordMiddleware(newSensitiveWordFilter([]string{"赌博"}))

	nextCalled := false
	next := func(_ context.Context, _ entity.Message) error {
		nextCalled = true
		return nil
	}

	if err := mw(next)(context.Background(), contentMsg("今天天气不错")); err != nil {
		t.Fatalf("未命中应放行: %v", err)
	}
	if !nextCalled {
		t.Fatal("未命中应继续执行 next")
	}
}

func TestSensitiveWordEmptyWordsAlwaysPass(t *testing.T) {
	mw := sensitiveWordMiddleware(newSensitiveWordFilter(nil))
	nextCalled := false
	next := func(_ context.Context, _ entity.Message) error {
		nextCalled = true
		return nil
	}
	if err := mw(next)(context.Background(), contentMsg("任意内容")); err != nil {
		t.Fatalf("空词表应永远放行: %v", err)
	}
	if !nextCalled {
		t.Fatal("空词表应继续执行 next")
	}
}

func TestSensitiveWordCaseInsensitive(t *testing.T) {
	mw := sensitiveWordMiddleware(newSensitiveWordFilter([]string{"fuck"}))
	err := mw(func(_ context.Context, _ entity.Message) error { return nil })(context.Background(), contentMsg("FUCK OFF"))
	if !errors.Is(err, entity.ErrSensitiveWord) {
		t.Fatalf("大小写变体应命中: %v", err)
	}
}
