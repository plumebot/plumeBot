package ai

import (
	"fmt"

	"github.com/cloudwego/eino/schema"

	"plumebot/internal/domain/entity"
)

// ToSchema 将业务消息列表转换为 eino schema.Message 列表。
// 规则：
//   - 空 Parts 的消息整体跳过；
//   - 单文本 part → 走 Message.Content（兼容任何角色；eino-ext 的
//     UserInputMultiContent 仅允许 user/tool 角色，纯文本走 Content 是标准用法）；
//   - 多 part 或含非文本 part → 走 UserInputMultiContent，且仅允许 user 角色
//     （eino-ext openai 转换器限制，system/assistant 带图片会被模型侧拒绝）；
//   - part 的 URL/Base64 必须二选一（同时为空或同时设置都报错）；
//   - 未知 Role / PartType 报错（错误信息带消息序号与 part 序号定位）。
func ToSchema(msgs []entity.ChatMessage) ([]*schema.Message, error) {
	out := make([]*schema.Message, 0, len(msgs))
	for i, m := range msgs {
		if len(m.Parts) == 0 {
			continue
		}
		role, err := roleToSchema(m.Role)
		if err != nil {
			return nil, fmt.Errorf("消息 %d: %w", i, err)
		}

		// 单文本 part：走 Content（纯文本消息，任何角色皆可）。
		if len(m.Parts) == 1 && m.Parts[0].Type == entity.PartTypeText {
			out = append(out, &schema.Message{Role: role, Content: m.Parts[0].Text})
			continue
		}

		// 多模态：UserInputMultiContent 仅支持 user 角色（eino-ext openai 转换器限制）。
		if m.Role != entity.RoleUser {
			return nil, fmt.Errorf("消息 %d: 非文本 part 仅支持 user 角色，当前 %q", i, m.Role)
		}
		parts := make([]schema.MessageInputPart, 0, len(m.Parts))
		for j, p := range m.Parts {
			part, err := partToSchema(p)
			if err != nil {
				return nil, fmt.Errorf("消息 %d part %d: %w", i, j, err)
			}
			parts = append(parts, part)
		}

		out = append(out, &schema.Message{Role: role, UserInputMultiContent: parts})
	}
	return out, nil
}

// roleToSchema 将业务角色映射为 eino schema.RoleType。
func roleToSchema(r entity.Role) (schema.RoleType, error) {
	switch r {
	case entity.RoleSystem:
		return schema.System, nil
	case entity.RoleUser:
		return schema.User, nil
	case entity.RoleAssistant:
		return schema.Assistant, nil
	default:
		return "", fmt.Errorf("未知角色 %q", r)
	}
}

// partToSchema 将业务内容片段转换为 eino MessageInputPart。
func partToSchema(p entity.ContentPart) (schema.MessageInputPart, error) {
	switch p.Type {
	case entity.PartTypeText:
		return schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: p.Text}, nil

	case entity.PartTypeImage:
		common, err := partCommon(p)
		if err != nil {
			return schema.MessageInputPart{}, err
		}
		return schema.MessageInputPart{
			Type:  schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{MessagePartCommon: common, Detail: schema.ImageURLDetailAuto},
		}, nil

	case entity.PartTypeAudio:
		common, err := partCommon(p)
		if err != nil {
			return schema.MessageInputPart{}, err
		}
		return schema.MessageInputPart{
			Type:  schema.ChatMessagePartTypeAudioURL,
			Audio: &schema.MessageInputAudio{MessagePartCommon: common},
		}, nil

	case entity.PartTypeVideo:
		common, err := partCommon(p)
		if err != nil {
			return schema.MessageInputPart{}, err
		}
		return schema.MessageInputPart{
			Type:  schema.ChatMessagePartTypeVideoURL,
			Video: &schema.MessageInputVideo{MessagePartCommon: common},
		}, nil

	case entity.PartTypeFile:
		common, err := partCommon(p)
		if err != nil {
			return schema.MessageInputPart{}, err
		}
		return schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeFileURL,
			File: &schema.MessageInputFile{MessagePartCommon: common},
		}, nil

	case entity.PartTypeAt:
		// at 为入站专用标记（[@qq]/[@全体]），不直接进 LLM。这里兜底转纯文本，
		// 防含 @ 消息进 LLM 时在 ToSchema 报「未知 part 类型 at」（B-029）；
		// P6-001 service 组装会显式 at→text（B-028），此处仅保证不崩。
		return schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: p.Text}, nil

	default:
		return schema.MessageInputPart{}, fmt.Errorf("未知 part 类型 %q", p.Type)
	}
}

// partCommon 构造二进制片段的公共字段：URL 与 Base64 二选一，必须恰有一个非空。
func partCommon(p entity.ContentPart) (schema.MessagePartCommon, error) {
	if p.URL != "" && p.Base64 != "" {
		return schema.MessagePartCommon{}, fmt.Errorf("URL 与 Base64 不能同时设置")
	}
	if p.URL != "" {
		u := p.URL
		return schema.MessagePartCommon{URL: &u}, nil
	}
	if p.Base64 != "" {
		b := p.Base64
		return schema.MessagePartCommon{Base64Data: &b, MIMEType: p.MIMEType}, nil
	}
	return schema.MessagePartCommon{}, fmt.Errorf("URL 与 Base64 均为空")
}

// FromSchema 取最终消息的文本内容（tool 循环结束后为最终 assistant 消息）。
// 多模态输出等非文本场景返回空串，P6 再议。
func FromSchema(m *schema.Message) string {
	if m == nil {
		return ""
	}
	return m.Content
}
