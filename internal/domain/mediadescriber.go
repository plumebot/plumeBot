package domain

import (
	"context"

	"plumebot/internal/domain/entity"
)

// MediaDescriber 为多模态内容生成 LLM 文本描述（多模态描述机制，阶段 2）。
// 实现应自取媒体内容（HTTP GET URL / 读本地缓存文件 / 解码 Base64）→ 转 base64 →
// 视觉模型单次调用 → 返回文本描述；失败返回 error，由调用方回退占位标记（如 [图片]）不断链。
type MediaDescriber interface {
	Describe(ctx context.Context, part entity.ContentPart) (string, error)
}
