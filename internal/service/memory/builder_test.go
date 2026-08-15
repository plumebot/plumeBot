package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
)

// ---- 测试构造辅助 ----

// bMsg 构造带 ID/用户/多段的群聊消息。
func bMsg(groupID, userID, id string, parts ...entity.ContentPart) entity.Message {
	return entity.Message{MessageID: id, GroupID: groupID, UserID: userID, MessageType: "group", Parts: parts}
}

// privMsg 构造私聊消息（GroupID 空，会话键 "private:"+UserID）。
func privMsg(userID, id string, parts ...entity.ContentPart) entity.Message {
	return entity.Message{MessageID: id, GroupID: "", UserID: userID, MessageType: "private", Parts: parts}
}

func txt(s string) entity.ContentPart     { return entity.ContentPart{Type: entity.PartTypeText, Text: s} }
func at(s string) entity.ContentPart      { return entity.ContentPart{Type: entity.PartTypeAt, Text: s} }
func imgPart(url string) entity.ContentPart { return entity.ContentPart{Type: entity.PartTypeImage, URL: url} }

// builderStorage 是组装器测试用的假存储：嵌入 domain.Storage，
// 实现 PersistMessage/组装器需要读写的接口，其余调用即 panic（测试不会走到）。
type builderStorage struct {
	domain.Storage
	groupProf map[string]*entity.GroupProfile
	jargon    map[string][]string // groupID -> confirmed 黑话
	facts     map[string][]string // "groupID|userID" -> 事实（私聊 groupID=""）
	persona   map[string]string   // agent -> system_prompt
	archived  []entity.Summary    // ListSummaries 数据

	updatedIDs []string
	updated    [][]entity.ContentPart
}

func (f *builderStorage) SaveMessage(_ context.Context, _ entity.Message) error { return nil }

func (f *builderStorage) GetGroupProfile(_ context.Context, groupID string) (*entity.GroupProfile, error) {
	if p, ok := f.groupProf[groupID]; ok {
		return p, nil
	}
	return nil, domain.ErrNotFound
}

func (f *builderStorage) ListConfirmedJargon(_ context.Context, groupID string) ([]string, error) {
	return f.jargon[groupID], nil
}

func (f *builderStorage) ListMemberFacts(_ context.Context, groupID, userID string) ([]string, error) {
	return f.facts[groupID+"|"+userID], nil
}

func (f *builderStorage) GetPersonaByAgent(_ context.Context, agent string) (*entity.Persona, error) {
	if p, ok := f.persona[agent]; ok {
		return &entity.Persona{Agent: agent, SystemPrompt: p}, nil
	}
	return nil, domain.ErrNotFound
}

func (f *builderStorage) ListSummaries(_ context.Context, chatID string, limit int) ([]entity.Summary, error) {
	// 与生产一致：按 seq 取最新 limit 条，再反转回正序（旧→新）。
	var out []entity.Summary
	for _, s := range f.archived {
		if s.ChatID == chatID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq > out[j].Seq })
	if len(out) > limit {
		out = out[:limit]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (f *builderStorage) UpdateMessageParts(_ context.Context, messageID string, parts []entity.ContentPart) error {
	f.updatedIDs = append(f.updatedIDs, messageID)
	f.updated = append(f.updated, parts)
	return nil
}

// fakeDescriber 记录调用次数，可注入错误/预设描述。
type fakeDescriber struct {
	calls  int
	err    error
	preset string
}

func (d *fakeDescriber) Describe(_ context.Context, _ entity.ContentPart) (string, error) {
	d.calls++
	if d.err != nil {
		return "", d.err
	}
	if d.preset != "" {
		return d.preset, nil
	}
	return "图片描述", nil
}

// newBuilderService 构造组装测试服务：默认 persona「你是测试机器人」、默认兜底人设。
func newBuilderService(store *builderStorage, d domain.MediaDescriber) *MemoryService {
	if store.persona == nil {
		store.persona = map[string]string{"PlumeBot": "你是测试机器人"}
	}
	return NewMemoryService(NewWindow(), store, &fakeSummarizer{}, BuilderConfig{
		Prompt:         config.PromptConfig{},
		AgentName:      "PlumeBot",
		DefaultPersona: "兜底人设",
	}, d)
}

// persist 依次持久化多条消息（暖窗口 + 画像缓存，与生产时序一致）。
func persist(t *testing.T, svc *MemoryService, msgs ...entity.Message) {
	t.Helper()
	ctx := context.Background()
	for _, m := range msgs {
		if _, err := svc.PersistMessage(ctx, m); err != nil {
			t.Fatalf("持久化失败: %v", err)
		}
	}
}

// ---- 组装测试 ----

func TestBuildMessagesFiveSectionsOrder(t *testing.T) {
	store := &builderStorage{
		groupProf: map[string]*entity.GroupProfile{"g1": {
			GroupID: "g1", Culture: "技术群", Topics: []string{"Go", "AI"},
			ActiveHours: "晚上", Rules: []string{"不发广告"}, Atmosphere: []string{"轻松"},
		}},
		jargon: map[string][]string{"g1": {"破防", "yyds"}},
		facts:  map[string][]string{"g1|u1": {"喜欢猫", "是程序员"}, "g1|u2": {"在备考"}},
	}
	svc := newBuilderService(store, nil)
	ctx := context.Background()

	w1 := bMsg("g1", "u1", "w1", txt("你好"))
	w2 := bMsg("g1", "u2", "w2", txt("在吗"))
	cur := bMsg("g1", "u3", "cur", txt("看看你"))
	persist(t, svc, w1, w2, cur)

	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	// system + w1 + w2 + cur = 4 条，当前消息不重复（去重）。
	if len(msgs) != 4 {
		t.Fatalf("消息条数 = %d, want 4", len(msgs))
	}
	if msgs[0].Role != entity.RoleSystem || len(msgs[0].Parts) != 1 || msgs[0].Parts[0].Type != entity.PartTypeText {
		t.Fatalf("首条应为 system 单文本 part: %+v", msgs[0])
	}
	sys := msgs[0].Parts[0].Text
	// 五段顺序：persona → 会话画像 → 历史摘要 → 回复指令。
	idxPersona := strings.Index(sys, "你是测试机器人")
	idxProfile := strings.Index(sys, "## 会话画像")
	idxSummary := strings.Index(sys, "## 历史摘要")
	idxInst := strings.Index(sys, replyInstruction)
	if idxPersona < 0 || idxProfile < 0 || idxSummary < 0 || idxInst < 0 ||
		!(idxPersona < idxProfile && idxProfile < idxSummary && idxSummary < idxInst) {
		t.Errorf("system 五段顺序错误: persona=%d 画像=%d 摘要=%d 指令=%d\n%s",
			idxPersona, idxProfile, idxSummary, idxInst, sys)
	}
	// ② 会话画像：群信息 + 黑话 + 成员事实。
	for _, want := range []string{
		"群 ID：g1", "群文化：技术群", "主要话题：Go、AI", "群规：不发广告", "氛围：轻松",
		"破防；yyds", "u1：喜欢猫；是程序员", "u2：在备考",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("system 缺少画像内容 %q:\n%s", want, sys)
		}
	}
	// ③ 摘要空占位。
	if !strings.Contains(sys, "（暂无历史摘要）") {
		t.Errorf("无摘要应占位: %s", sys)
	}
	// ④ 窗口按时间序、⑤ 当前消息最后。
	if msgs[1].Role != entity.RoleUser || msgs[1].Parts[0].Text != "你好" {
		t.Errorf("④ 首条应为 w1: %+v", msgs[1])
	}
	if msgs[2].Parts[0].Text != "在吗" {
		t.Errorf("④ 第二条应为 w2: %+v", msgs[2])
	}
	if msgs[3].Role != entity.RoleUser || msgs[3].Parts[0].Text != "看看你" {
		t.Errorf("⑤ 应为当前消息: %+v", msgs[3])
	}
}

func TestBuildMessagesCurrentNotInWindow(t *testing.T) {
	svc := newBuilderService(&builderStorage{}, nil)
	ctx := context.Background()
	persist(t, svc, bMsg("g1", "u1", "w1", txt("你好")))

	cur := bMsg("g1", "u2", "cur", txt("新消息")) // 未持久化
	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if len(msgs) != 3 { // system + w1 + cur
		t.Fatalf("消息条数 = %d, want 3", len(msgs))
	}
	if msgs[2].Parts[0].Text != "新消息" {
		t.Errorf("⑤ 应追加当前消息: %+v", msgs[2])
	}
}

func TestBuildMessagesAtToText(t *testing.T) {
	// B-028：at 段组装转文本，不保留 at part。
	svc := newBuilderService(&builderStorage{}, nil)
	cur := bMsg("g1", "u1", "cur", txt("你好"), at("[@123]"))
	msgs, err := svc.BuildMessages(context.Background(), cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	last := msgs[len(msgs)-1]
	if len(last.Parts) != 1 || last.Parts[0].Type != entity.PartTypeText || last.Parts[0].Text != "你好[@123]" {
		t.Errorf("at 应转文本合并: %+v", last)
	}
}

func TestBuildMessagesBotRoleMapping(t *testing.T) {
	svc := newBuilderService(&builderStorage{}, nil)
	ctx := context.Background()
	bot := bMsg("g1", "botID1", "b1", txt("bot 说"))
	human := bMsg("g1", "u1", "h1", txt("人说"))
	cur := bMsg("g1", "u2", "cur", txt("现在"))
	persist(t, svc, bot, human, cur)

	msgs, err := svc.BuildMessages(ctx, cur, "botID1")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	// msgs = [system, b1, h1, cur]
	if msgs[1].Role != entity.RoleAssistant || msgs[2].Role != entity.RoleUser || msgs[3].Role != entity.RoleUser {
		t.Errorf("bot 消息应为 assistant，其余 user: %+v", msgs[1:])
	}

	// botID 空 → 全 RoleUser。
	msgs2, _ := svc.BuildMessages(ctx, cur, "")
	if msgs2[1].Role != entity.RoleUser || msgs2[2].Role != entity.RoleUser {
		t.Errorf("botID 空应全 user: %+v", msgs2[1:])
	}
}

func TestBuildMessagesPrivateBranch(t *testing.T) {
	store := &builderStorage{facts: map[string][]string{"|u1": {"喜欢猫"}}}
	svc := newBuilderService(store, nil)
	cur := privMsg("u1", "cur", txt("私聊"))
	msgs, err := svc.BuildMessages(context.Background(), cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	sys := msgs[0].Parts[0].Text
	if !strings.Contains(sys, "用户 ID：u1") || !strings.Contains(sys, "用户事实：喜欢猫") {
		t.Errorf("私聊应注入用户 ID 与事实: %s", sys)
	}
	if strings.Contains(sys, "【群信息】") || strings.Contains(sys, "【群内黑话】") {
		t.Errorf("私聊不应有群画像/黑话: %s", sys)
	}
}

func TestBuildMessagesFactsCap(t *testing.T) {
	facts := make([]string, 5)
	for i := range facts {
		facts[i] = fmt.Sprintf("事实%d", i+1)
	}
	store := &builderStorage{facts: map[string][]string{"g1|u1": facts}}
	svc := NewMemoryService(NewWindow(), store, &fakeSummarizer{}, BuilderConfig{
		Prompt: config.PromptConfig{FactsPerMember: 3}, AgentName: "PlumeBot", DefaultPersona: "兜底",
	}, nil)
	ctx := context.Background()
	cur := bMsg("g1", "u1", "cur", txt("hi"))
	persist(t, svc, cur) // 窗口有成员 u1

	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	sys := msgs[0].Parts[0].Text
	if !strings.Contains(sys, "事实1；事实2；事实3") {
		t.Errorf("应注入前 3 条事实: %s", sys)
	}
	if strings.Contains(sys, "事实4") || strings.Contains(sys, "事实5") {
		t.Errorf("超过 facts_per_member 上限不应注入: %s", sys)
	}
}

func TestBuildMessagesJargonCap(t *testing.T) {
	jargon := make([]string, 25)
	for i := range jargon {
		jargon[i] = fmt.Sprintf("黑话%d", i+1)
	}
	store := &builderStorage{jargon: map[string][]string{"g1": jargon}}
	svc := NewMemoryService(NewWindow(), store, &fakeSummarizer{}, BuilderConfig{
		Prompt: config.PromptConfig{JargonCap: 20}, AgentName: "PlumeBot", DefaultPersona: "兜底",
	}, nil)
	cur := bMsg("g1", "u1", "cur", txt("hi"))
	persist(t, svc, cur)

	msgs, err := svc.BuildMessages(context.Background(), cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	sys := msgs[0].Parts[0].Text
	if !strings.Contains(sys, "黑话20") {
		t.Errorf("应注入前 20 条黑话: %s", sys)
	}
	if strings.Contains(sys, "黑话21") {
		t.Errorf("超过 jargon_cap 上限不应注入: %s", sys)
	}
}

func TestBuildMessagesPersonaFallback(t *testing.T) {
	// 无 persona 行 → defaultPersona。
	store := &builderStorage{}
	svc := NewMemoryService(NewWindow(), store, &fakeSummarizer{}, BuilderConfig{
		AgentName: "PlumeBot", DefaultPersona: "兜底人设",
	}, nil)
	msgs, err := svc.BuildMessages(context.Background(), bMsg("g1", "u1", "cur", txt("hi")), "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if !strings.HasPrefix(msgs[0].Parts[0].Text, "兜底人设") {
		t.Errorf("persona 未命中应兜底 defaultPersona: %q", msgs[0].Parts[0].Text)
	}

	// defaultPersona 也空 → config.DefaultSystemPrompt。
	svc2 := NewMemoryService(NewWindow(), store, &fakeSummarizer{}, BuilderConfig{AgentName: "PlumeBot"}, nil)
	msgs2, _ := svc2.BuildMessages(context.Background(), bMsg("g1", "u1", "cur", txt("hi")), "")
	if !strings.HasPrefix(msgs2[0].Parts[0].Text, config.DefaultSystemPrompt) {
		t.Errorf("defaultPersona 空应兜底 DefaultSystemPrompt: %q", msgs2[0].Parts[0].Text)
	}
}

func TestBuildMessagesSummaries(t *testing.T) {
	store := &builderStorage{archived: []entity.Summary{
		{ChatID: "g1", Seq: 1, Text: "早先话题"},
		{ChatID: "g1", Seq: 2, Text: "近期话题"},
	}}
	svc := newBuilderService(store, nil)
	msgs, err := svc.BuildMessages(context.Background(), bMsg("g1", "u1", "cur", txt("hi")), "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	sys := msgs[0].Parts[0].Text
	if !strings.Contains(sys, "早先话题") || !strings.Contains(sys, "近期话题") {
		t.Errorf("应拼接全部历史摘要: %s", sys)
	}
	if strings.Contains(sys, "（暂无历史摘要）") {
		t.Errorf("有摘要不应占位: %s", sys)
	}
}

func TestBuildMessagesDescribeRecentRoundsBudget(t *testing.T) {
	d := &fakeDescriber{preset: "一只猫"}
	store := &builderStorage{}
	svc := NewMemoryService(NewWindow(), store, &fakeSummarizer{}, BuilderConfig{
		Prompt: config.PromptConfig{DescribeRecentRounds: 1}, AgentName: "PlumeBot", DefaultPersona: "兜底",
	}, d)
	ctx := context.Background()
	m1 := bMsg("g1", "u1", "m1", imgPart("http://x/1.png"))
	m2 := bMsg("g1", "u2", "m2", imgPart("http://x/2.png"))
	m3 := bMsg("g1", "u3", "m3", imgPart("http://x/3.png"))
	persist(t, svc, m1, m2, m3)

	msgs, err := svc.BuildMessages(ctx, m3, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if d.calls != 1 {
		t.Errorf("recent_rounds=1 应只描述最近 1 轮（即当前消息）, 实际 %d 次调用", d.calls)
	}
	// ④ 窗口（去重当前后为 m1,m2）恒 [图片]；⑤ 当前消息 m3 有描述。
	if !strings.Contains(msgs[1].Parts[0].Text, "[图片]") || !strings.Contains(msgs[2].Parts[0].Text, "[图片]") {
		t.Errorf("最近 1 轮之外的消息图片不应被描述: %q / %q",
			msgs[1].Parts[0].Text, msgs[2].Parts[0].Text)
	}
	if !strings.Contains(msgs[3].Parts[0].Text, "（图片：一只猫）") {
		t.Errorf("当前消息图片应被描述: %q", msgs[3].Parts[0].Text)
	}
}

func TestBuildMessagesDescribePerTurnCap(t *testing.T) {
	d := &fakeDescriber{preset: "图"}
	store := &builderStorage{}
	svc := NewMemoryService(NewWindow(), store, &fakeSummarizer{}, BuilderConfig{
		Prompt: config.PromptConfig{DescribePerTurnCap: 1}, AgentName: "PlumeBot", DefaultPersona: "兜底",
	}, d)
	ctx := context.Background()
	cur := bMsg("g1", "u1", "cur", imgPart("http://x/1.png"), imgPart("http://x/2.png"))
	persist(t, svc, cur)

	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if d.calls != 1 {
		t.Errorf("per_turn_cap=1 应只描述 1 张, 实际 %d", d.calls)
	}
	last := msgs[len(msgs)-1].Parts[0].Text
	if !strings.Contains(last, "（图片：图）") || !strings.Contains(last, "[图片]") {
		t.Errorf("第一张描述、第二张占位: %q", last)
	}
}

func TestBuildMessagesDescribePersistAndReuse(t *testing.T) {
	// B-025/B-027：描述成功后写回 SQLite；窗口回填后二次组装不重复调用 describer。
	d := &fakeDescriber{preset: "一只猫"}
	store := &builderStorage{}
	svc := newBuilderService(store, d)
	ctx := context.Background()
	cur := bMsg("g1", "u1", "cur", imgPart("http://x/1.png"))
	persist(t, svc, cur)

	if _, err := svc.BuildMessages(ctx, cur, ""); err != nil {
		t.Fatalf("首次组装失败: %v", err)
	}
	if d.calls != 1 {
		t.Errorf("首次应描述 1 次, 实际 %d", d.calls)
	}
	if len(store.updatedIDs) != 1 || store.updatedIDs[0] != "cur" {
		t.Fatalf("首次应持久化描述: updatedIDs=%v", store.updatedIDs)
	}
	found := false
	for _, p := range store.updated[0] {
		if p.Type == entity.PartTypeImage && p.Description == "一只猫" {
			found = true
		}
	}
	if !found {
		t.Errorf("写回 parts 应含描述: %+v", store.updated[0])
	}

	// 二次组装：窗口已回填描述 → 不再调用 describer、不再持久化。
	if _, err := svc.BuildMessages(ctx, cur, ""); err != nil {
		t.Fatalf("二次组装失败: %v", err)
	}
	if d.calls != 1 {
		t.Errorf("二次组装不应重复描述, 实际 %d", d.calls)
	}
	if len(store.updatedIDs) != 1 {
		t.Errorf("二次组装不应重复持久化, 实际 %d", len(store.updatedIDs))
	}
}

func TestBuildMessagesDescribeErrorFallback(t *testing.T) {
	d := &fakeDescriber{err: errors.New("描述失败")}
	store := &builderStorage{}
	svc := newBuilderService(store, d)
	ctx := context.Background()
	cur := bMsg("g1", "u1", "cur", imgPart("http://x/1.png"))
	persist(t, svc, cur)

	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("描述失败不应阻断组装: %v", err)
	}
	last := msgs[len(msgs)-1].Parts[0].Text
	if !strings.Contains(last, "[图片]") {
		t.Errorf("描述失败应回退 [图片]: %q", last)
	}
	if len(store.updatedIDs) != 0 {
		t.Errorf("描述失败不应持久化, 实际 %d 次更新", len(store.updatedIDs))
	}
}

func TestBuildMessagesNoDescriber(t *testing.T) {
	// 无 vision_model（describer nil）→ 图片恒 [图片]。
	svc := newBuilderService(&builderStorage{}, nil)
	ctx := context.Background()
	cur := bMsg("g1", "u1", "cur", imgPart("http://x/1.png"))
	persist(t, svc, cur)

	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if !strings.Contains(msgs[len(msgs)-1].Parts[0].Text, "[图片]") {
		t.Errorf("无 describer 应恒 [图片]: %q", msgs[len(msgs)-1].Parts[0].Text)
	}
}

func TestBuildMessagesSystemSingleTextPart(t *testing.T) {
	svc := newBuilderService(&builderStorage{}, nil)
	msgs, err := svc.BuildMessages(context.Background(), bMsg("g1", "u1", "cur", txt("hi")), "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if len(msgs[0].Parts) != 1 || msgs[0].Parts[0].Type != entity.PartTypeText {
		t.Errorf("system 应单文本 part: %+v", msgs[0])
	}
}

// TestBuildMessagesEmptyIDDedupesCurrent 空 MessageID（OneBot 边角，审查 BUG-2）：
// 当前消息按「窗口末尾 + 同发送者同时刻」去重，不重复进 ④ 与 ⑤。
func TestBuildMessagesEmptyIDDedupesCurrent(t *testing.T) {
	svc := newBuilderService(&builderStorage{}, nil)
	ctx := context.Background()
	w1 := bMsg("g1", "u1", "w1", txt("早"))
	w1.Timestamp = 1
	cur := bMsg("g1", "u1", "", txt("现在"))
	cur.Timestamp = 2
	persist(t, svc, w1, cur)

	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if len(msgs) != 3 { // system + w1 + cur
		t.Fatalf("消息条数 = %d, want 3", len(msgs))
	}
	if msgs[1].Parts[0].Text != "早" || msgs[2].Parts[0].Text != "现在" {
		t.Errorf("④ 应含 w1、⑤ 应含当前: %+v", msgs[1:])
	}
}

// TestBuildMessagesEmptyIDNoPersistDescribe 空 MessageID 描述不持久化（BUG-2 防 UPDATE 写错行），
// 仅内存回填用于本次组装。
func TestBuildMessagesEmptyIDNoPersistDescribe(t *testing.T) {
	d := &fakeDescriber{preset: "一只猫"}
	store := &builderStorage{}
	svc := newBuilderService(store, d)
	ctx := context.Background()
	cur := bMsg("g1", "u1", "", imgPart("http://x/1.png"))
	cur.Timestamp = 1
	persist(t, svc, cur)

	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if !strings.Contains(msgs[len(msgs)-1].Parts[0].Text, "（图片：一只猫）") {
		t.Errorf("空 ID 消息图片应描述并用于本次组装: %q", msgs[len(msgs)-1].Parts[0].Text)
	}
	if len(store.updatedIDs) != 0 {
		t.Errorf("空 MessageID 不应持久化描述（防写错行）, 实际 %v", store.updatedIDs)
	}
}

// TestBuildMessagesEmptyIDBudgetNotDoubled 空 MessageID 当前消息描述预算不翻倍（BUG-2）：
// per_turn_cap=1 时只描述 1 张，不会因 covered 判定失效对同一消息再跑一轮。
func TestBuildMessagesEmptyIDBudgetNotDoubled(t *testing.T) {
	d := &fakeDescriber{preset: "图"}
	store := &builderStorage{}
	svc := NewMemoryService(NewWindow(), store, &fakeSummarizer{}, BuilderConfig{
		Prompt: config.PromptConfig{DescribePerTurnCap: 1}, AgentName: "PlumeBot", DefaultPersona: "兜底",
	}, d)
	ctx := context.Background()
	cur := bMsg("g1", "u1", "", imgPart("http://x/1.png"), imgPart("http://x/2.png"))
	cur.Timestamp = 1
	persist(t, svc, cur)

	msgs, err := svc.BuildMessages(ctx, cur, "")
	if err != nil {
		t.Fatalf("BuildMessages 失败: %v", err)
	}
	if d.calls != 1 {
		t.Errorf("空 ID 当前消息描述预算不应翻倍, 实际 %d 次调用", d.calls)
	}
	last := msgs[len(msgs)-1].Parts[0].Text
	if !strings.Contains(last, "（图片：图）") || !strings.Contains(last, "[图片]") {
		t.Errorf("应描述 1 张、另一张占位: %q", last)
	}
}
