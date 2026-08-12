package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
	"plumebot/pkg/config"
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
)

// EinoMediaDescriber 是基于原始 ChatModel 的 domain.MediaDescriber 实现。
// 与 EinoAgent 刻意分离：无人设 Instruction、无工具、无 ReAct 循环，
// 是「取图 → base64 → 视觉模型单次调用 → 文本描述」的裸调用（图片描述专用）。
// 拉图来源判定：part.Base64 直传 → 解码；URL 为 http(s):// → HTTP GET；
// 其余（本地路径，base64 入链闭合产物）→ os.ReadFile。
type EinoMediaDescriber struct {
	cm   model.BaseChatModel
	http *http.Client
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
	return &EinoMediaDescriber{cm: cm, http: &http.Client{Timeout: fetchTimeout}}, nil
}

// Describe 生成图片的文本描述：加载图片字节 → base64 → 复用 ToSchema 构造
// user 多模态消息（text + image，与 spike 冒烟同形状）→ 前置 system → 视觉模型单次调用。
func (d *EinoMediaDescriber) Describe(ctx context.Context, part entity.ContentPart) (string, error) {
	data, mime, err := loadImageBytes(ctx, d.http, part)
	if err != nil {
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
	if err != nil {
		return "", fmt.Errorf("视觉推理失败: %w", err)
	}
	return out.Content, nil
}

// loadImageBytes 按 part 的媒体来源加载图片字节，并返回可用的 MIME 类型。
// 来源优先级：Base64 直传 → http(s) URL → 本地文件路径。
func loadImageBytes(ctx context.Context, client *http.Client, part entity.ContentPart) ([]byte, string, error) {
	var data []byte
	var err error

	switch {
	case part.Base64 != "":
		data, err = base64.StdEncoding.DecodeString(part.Base64)
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
