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
// 是「取图（远程 URL 直传 provider / 本地取字节）→ 视觉模型单次调用 → 文本描述」的裸调用。
// 拉图来源判定：URL 为 http(s):// → 直传 URL（provider 侧拉图），失败本地 GET 回退；
// part.Base64 直传 → 解码；其余（本地路径，base64 入链闭合产物）→ os.ReadFile。
// P6-001 B-027：按内容哈希缓存成功描述（contentHash→desc，并发安全），重复图片不再调用模型；
// 失败按 contentHash 记冷却（BUG-3），冷却期内直接失败，防重试风暴。
// B-027 增强：内容键优先 FileHash（一图一值，同图多 URL 命中缓存），URL 直传免本地下载。
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

// Describe 生成图片的文本描述。远程 http(s) URL → 直传 URL 给视觉模型（provider 侧拉图，
// 不本地下载，B-027 增强）；直传失败（过期签名 URL / provider 不可达 qpic / 不支持远程图）→
// 本地拉取 → base64 回退重试一次。本地路径/base64 → 取字节 → base64 透传。均复用 ToSchema
// 构造 user 多模态消息（text + image）→ 前置 system → 视觉模型单次调用。
// 命中成功缓存（B-027）直接返回；命中失败冷却（BUG-3）直接返回失败，均不触达模型。
// 日志：缓存命中/冷却命中 Debug、视觉调用记模型调用度量 Info（架构 §17.2 infra/ai）—
// 排障「图片一直是 [图片]」有依据（缓存失效、合并接口不可达、视觉调用失败一目了然）。
func (d *EinoMediaDescriber) Describe(ctx context.Context, part entity.ContentPart) (string, error) {
	l := logger.From(ctx) // 携带 trace_id（会话键）；未注入时回退全局
	key := imageContentKey(part)
	if key != "" {
		d.mu.Lock()
		if desc, ok := d.cache[key]; ok {
			d.mu.Unlock()
			l.Debug("图片描述缓存命中", logger.S("key", key))
			return desc, nil
		}
		if retryAt, ok := d.fails[key]; ok && time.Now().Before(retryAt) {
			d.mu.Unlock()
			l.Debug("图片描述失败冷却命中（BUG-3）",
				logger.S("key", key), logger.S("retry_after", descRetryAfter.String()))
			return "", fmt.Errorf("图片描述在失败冷却中（BUG-3，%s 内重试）: %s", descRetryAfter, key)
		}
		d.mu.Unlock()
	}

	start := time.Now()
	msgs, err := buildDescribeUser(ctx, d.http, part)
	if err != nil {
		d.markFail(key)
		l.Warn("图片描述加载失败",
			logger.S("key", key), logger.I64("latency_ms", time.Since(start).Milliseconds()), logger.Err(err))
		return "", fmt.Errorf("加载图片失败: %w", err)
	}

	out, err := d.cm.Generate(ctx, describeInput(msgs))
	if err != nil && isRemoteURL(part) {
		// URL 直传失败（过期签名 URL / 海外 API 无法访问 qpic / 自建服务不支持远程图）：
		// 本地拉取 → base64 回退重试一次；仅远程 URL 生效，本地/Base64 不进此分支。
		if data, mime, ferr := loadImageBytes(ctx, d.http, part); ferr == nil {
			var fb []*schema.Message
			if fb, err = buildDescribeUserBase64(data, mime); err == nil {
				out, err = d.cm.Generate(ctx, describeInput(fb))
			}
		} else {
			err = ferr // 回退拉图失败：换回退的拉取错误（比原始模型错误更可诊断）
		}
	}

	fields := []zap.Field{
		logger.S("model", d.model),
		logger.I64("latency_ms", time.Since(start).Milliseconds()),
	}
	if err != nil {
		d.markFail(key)
		fields = append(fields, logger.Err(err))
		l.Warn("图片描述模型调用失败", fields...)
		return "", fmt.Errorf("视觉推理失败: %w", err)
	}
	if out.ResponseMeta != nil && out.ResponseMeta.Usage != nil {
		u := out.ResponseMeta.Usage
		fields = append(fields,
			logger.I("prompt_tokens", u.PromptTokens),
			logger.I("completion_tokens", u.CompletionTokens),
			logger.I("total_tokens", u.TotalTokens))
	}
	l.Info("模型调用", fields...)

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

// ---------------------------------------------------------------------------
// 图片描述输入构造（B-027 增强：URL 直传 + 本地回退）
// ---------------------------------------------------------------------------

// isRemoteURL 判断 part 是否为 http(s) 远程 URL。与 loadImageBytes 的拉取判定保持一致。
func isRemoteURL(p entity.ContentPart) bool {
	return strings.HasPrefix(p.URL, "http://") || strings.HasPrefix(p.URL, "https://")
}

// buildDescribeUser 构造图片描述 user 多模态消息（text+image）。
// 远程 URL → 直传 URL part（provider 侧拉图，不本地下载）；本地路径/base64 →
// loadImageBytes 取字节 → base64 透传（MIME 由 DetectContentType 兜底）。
func buildDescribeUser(ctx context.Context, client *http.Client, part entity.ContentPart) ([]*schema.Message, error) {
	if isRemoteURL(part) {
		return ToSchema([]entity.ChatMessage{{
			Role: entity.RoleUser,
			Parts: []entity.ContentPart{
				{Type: entity.PartTypeText, Text: userDescribePrompt},
				{Type: entity.PartTypeImage, URL: part.URL},
			},
		}})
	}
	data, mime, err := loadImageBytes(ctx, client, part)
	if err != nil {
		return nil, err
	}
	return buildDescribeUserBase64(data, mime)
}

// buildDescribeUserBase64 构造 text+image(base64) 的 user 消息（本地路径首走与 URL 回退共用）。
func buildDescribeUserBase64(data []byte, mime string) ([]*schema.Message, error) {
	b64 := base64.StdEncoding.EncodeToString(data)
	return ToSchema([]entity.ChatMessage{{
		Role: entity.RoleUser,
		Parts: []entity.ContentPart{
			{Type: entity.PartTypeText, Text: userDescribePrompt},
			{Type: entity.PartTypeImage, Base64: b64, MIMEType: mime},
		},
	}})
}

// describeInput 前置 system（视觉模型要求 user 多模态消息；system 保持单文本 part）。
func describeInput(user []*schema.Message) []*schema.Message {
	return append([]*schema.Message{schema.SystemMessage(systemDescribePrompt)}, user...)
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

// imageContentKey 图片内容哈希键（B-027 增强）：优先级 FileHash（convert 层从 OneBot
// file/file_md5 提取或 base64 字节推导，一图一值跨 URL 共享）→ Base64（按解码后字节 md5，
// 内容寻址）→ URL 字符串兜底。FileHash 仅当存在可描述来源（URL/Base64 非空）时生效——
// 避免「仅 FileHash 无来源」的占位段记入失败冷却，把同内容另有 URL 的来源一并冷却 60s。
func imageContentKey(p entity.ContentPart) string {
	if p.FileHash != "" && (p.URL != "" || p.Base64 != "") {
		return "md5:" + p.FileHash
	}
	if p.Base64 != "" {
		if data, err := base64util.Decode(p.Base64); err == nil {
			sum := md5.Sum(data)
			return "md5:" + hex.EncodeToString(sum[:])
		}
		// 解码失败 → 落下方 URL 兜底（无则空键不缓存）。
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
