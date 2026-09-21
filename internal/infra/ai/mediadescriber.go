package ai

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/base64util"
	"plumebot/pkg/config"
	"plumebot/pkg/logger"
)

// 编译期校验：EinoMediaDescriber 实现 domain.MediaDescriber。
var _ domain.MediaDescriber = (*EinoMediaDescriber)(nil)

// 图片描述调用的固定 prompt（阶段 2 简单内置，不配置化）。
const (
	systemDescribePrompt = "你是图片描述助手。请用简体中文简洁描述图片内容，突出关键对象、场景与动作。"
	userDescribePrompt   = "请描述这张图片的内容。"
)

const (
	fetchTimeout  = 30 * time.Second // 拉取图片超时
	maxImageBytes = 10 << 20         // 拉取图片大小上限（防御异常大图/恶意响应）

	// maxDescCacheEntries 描述缓存的软上限（P6-001 B-027，防无界增长；达到后停止写入新条目，
	// 旧条目仍服务。P6-003 B-004 再做 LRU/TTL 统一治理）。
	maxDescCacheEntries = 2000

	// descRetryAfter 描述失败后的冷却时长（P6-001 审查 BUG-3）：失败图片在窗口内直接返回失败，
	// 防止每轮组装对同一批不可达图片（如 NapCat 内网 URL）反复拉取/调模型的失败重试风暴
	// （对应窗口压缩侧的 CompressCooldown=60s）。
	descRetryAfter = 60 * time.Second
)

// EinoMediaDescriber 是基于原始 ChatModel 的 domain.MediaDescriber 实现。
// 与 EinoAgent 刻意分离：无人设 Instruction、无工具、无 ReAct 循环，
// 是「取图 → base64 → 视觉模型单次调用 → 文本描述」的裸调用（图片描述专用）。
// 拉图来源判定：part.Base64 直传 → 解码；URL 为 http(s):// → HTTP GET；
// 其余（本地路径，base64 入链闭合产物）→ os.ReadFile。
// P6-001 B-027：按内容哈希缓存成功描述（contentHash→desc，并发安全），重复图片不再调用模型；
// 失败按 contentHash 记冷却（BUG-3），冷却期内直接失败，防重试风暴。
// model 为视觉模型名（调用度量日志用，NewMediaDescriber 从 vision_entry 取）。
type EinoMediaDescriber struct {
	cm    model.BaseChatModel
	model string
	http  *http.Client
	mu    sync.Mutex
	cache map[string]string    // contentHash → desc；仅缓存成功结果（B-027）
	fails map[string]time.Time // contentHash → 下次重试时刻（BUG-3 失败冷却）
}

// NewMediaDescriber 依据视觉模型条目（cfg.LLM.VisionModel 引用 name）构建描述器。
// vision_model 未配置 / 未指向任何条目 → 报错（描述关闭由调用方在 main 侧判空处理）。
func NewMediaDescriber(ctx context.Context, cfg config.Config) (domain.MediaDescriber, error) {
	entry, ok := cfg.LLM.VisionEntry()
	if !ok {
		return nil, errors.New("llm.vision_model 未指向任何 models 条目（配置视觉模型条目后开启描述能力）")
	}
	cm, err := buildModelFromEntry(ctx, entry, cfg.LLM.TimeoutSeconds)
	if err != nil {
		return nil, err
	}
	return &EinoMediaDescriber{
		cm:    cm,
		model: entry.Model,
		http:  &http.Client{Timeout: fetchTimeout},
		cache: make(map[string]string),
		fails: make(map[string]time.Time),
	}, nil
}

// Describe 生成图片的文本描述：加载图片字节 → base64 → 复用 ToSchema 构造
// user 多模态消息（text + image，与 spike 冒烟同形状）→ 前置 system → 视觉模型单次调用。
// 命中成功缓存（B-027）直接返回；命中失败冷却（BUG-3）直接返回失败，均不触达模型。
// 日志：缓存命中/冷却命中 Debug、视觉调用记模型调用度量 Info（架构 §17.2 infra/ai）—
// 排障「图片一直是 [图片]」有依据（缓存失效、合并接口不可达、视觉调用失败一目了然）。
func (d *EinoMediaDescriber) Describe(ctx context.Context, part entity.ContentPart) (string, error) {
	key := imageContentKey(part)
	if key != "" {
		d.mu.Lock()
		if desc, ok := d.cache[key]; ok {
			d.mu.Unlock()
			logger.Debug("图片描述缓存命中", logger.S("key", key))
			return desc, nil
		}
		if retryAt, ok := d.fails[key]; ok && time.Now().Before(retryAt) {
			d.mu.Unlock()
			logger.Debug("图片描述失败冷却命中（BUG-3）",
				logger.S("key", key), logger.S("retry_after", descRetryAfter.String()))
			return "", fmt.Errorf("图片描述在失败冷却中（BUG-3，%s 内重试）: %s", descRetryAfter, key)
		}
		d.mu.Unlock()
	}

	start := time.Now()
	data, mime, err := loadImageBytes(ctx, d.http, part)
	if err != nil {
		d.markFail(key)
		logger.Warn("图片描述拉取失败",
			logger.S("key", key), logger.I64("latency_ms", time.Since(start).Milliseconds()), logger.Err(err))
		return "", fmt.Errorf("加载图片失败: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	msgs, err := ToSchema([]entity.ChatMessage{{
		Role: entity.RoleUser,
		Parts: []entity.ContentPart{
			{Type: entity.PartTypeText, Text: userDescribePrompt},
			{Type: entity.PartTypeImage, Base64: b64, MIMEType: mime},
		},
	}})
	if err != nil {
		return "", err
	}
	input := append([]*schema.Message{schema.SystemMessage(systemDescribePrompt)}, msgs...)

	out, err := d.cm.Generate(ctx, input)
	fields := []zap.Field{
		logger.S("model", d.model),
		logger.I64("latency_ms", time.Since(start).Milliseconds()),
	}
	if err != nil {
		d.markFail(key)
		fields = append(fields, logger.Err(err))
		logger.Warn("图片描述模型调用失败", fields...)
		return "", fmt.Errorf("视觉推理失败: %w", err)
	}
	if out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		u := out.ResponseMeta.Usage
		fields = append(fields,
			logger.I("prompt_tokens", u.PromptTokens),
			logger.I("completion_tokens", u.CompletionTokens),
			logger.I("total_tokens", u.TotalTokens))
	}
	logger.Info("模型调用", fields...)

	// 只缓存成功结果（失败走 markFail 冷却）。
	if key != "" && out.Content != "" {
		d.mu.Lock()
		if d.cache == nil {
			d.cache = make(map[string]string)
		}
		if len(d.cache) < maxDescCacheEntries {
			d.cache[key] = out.Content
		}
		d.mu.Unlock()
	}
	return out.Content, nil
}

// markFail 记录描述失败冷却（BUG-3）：失败 contentKey 在 descRetryAfter 内直接失败，防重试风暴。
// 懒初始化：直接构造的实例（测试）cache/fails 可能为 nil。
func (d *EinoMediaDescriber) markFail(key string) {
	if key == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fails == nil {
		d.fails = make(map[string]time.Time)
	}
	if len(d.fails) >= maxDescCacheEntries {
		return // 软上限，超限停止记录（冷却条目会过期，无驱逐压力）
	}
	d.fails[key] = time.Now().Add(descRetryAfter)
}

// imageContentKey 图片内容哈希键（B-027）：Base64 按内容 md5（内容寻址）；
// URL/本地缓存路径按 URL 串（data/image_cache/<md5> 路径本身已是内容寻址）；
// 两者皆空 → ""（不缓存）。
func imageContentKey(p entity.ContentPart) string {
	if p.Base64 != "" {
		sum := md5.Sum([]byte(p.Base64))
		return "b64:" + hex.EncodeToString(sum[:])
	}
	if p.URL != "" {
		return "url:" + p.URL
	}
	return ""
}

// loadImageBytes 按 part 的媒体来源加载图片字节，并返回可用的 MIME 类型。
// 来源优先级：Base64 直传 → http(s) URL → 本地文件路径。
func loadImageBytes(ctx context.Context, client *http.Client, part entity.ContentPart) ([]byte, string, error) {
	var data []byte
	var err error

	switch {
	case part.Base64 != "":
		data, err = base64util.Decode(part.Base64)
		if err != nil {
			return nil, "", fmt.Errorf("解码 Base64 图片失败: %w", err)
		}
	case strings.HasPrefix(part.URL, "http://") || strings.HasPrefix(part.URL, "https://"):
		data, err = fetchImage(ctx, client, part.URL)
		if err != nil {
			return nil, "", err
		}
	case part.URL != "":
		data, err = os.ReadFile(part.URL)
		if err != nil {
			return nil, "", fmt.Errorf("读取本地图片 %s 失败: %w", part.URL, err)
		}
	default:
		return nil, "", errors.New("图片描述输入为空：URL 与 Base64 均为空")
	}

	mime := part.MIMEType
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	return data, mime, nil
}

// fetchImage 经 HTTP GET 拉取图片字节，限制大小与超时。
func fetchImage(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("构造图片请求失败: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取图片 %s 失败: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("拉取图片 %s 失败: HTTP %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取图片响应失败: %w", err)
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("图片 %s 超过大小上限 %d 字节", url, maxImageBytes)
	}
	return data, nil
}
