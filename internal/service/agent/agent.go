package agent

import (
	"context"

	"plumebot/internal/domain"
	"plumebot/internal/domain/entity"
)

// AgentService 负责 Agent 推理编排。
// 五段消息组装（①persona + ②会话画像 + ③摘要 → system，④窗口，⑤当前消息）在
// service/memory BuildMessages 完成（P6-001）；本层只透传组装好的完整消息列表给 domain.Agent。
type AgentService struct {
	agent domain.Agent
}

// NewAgentService 创建 AgentService，注入 domain.Agent 实现。
func NewAgentService(agent domain.Agent) *AgentService {
	return &AgentService{agent: agent}
}

// GenerateReply 透传消息列表调用 Agent 生成回复。
func (s *AgentService) GenerateReply(ctx context.Context, msgs []entity.ChatMessage) (string, error) {
	return s.agent.Generate(ctx, msgs)
}
