package handler

import (
	"context"
	"strconv"
	"time"

	"OurAgent/internal/service"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

type AgentHandler struct {
	service *service.AgentService
}

type agentChatRequest struct {
	ConversationID string `json:"conversation_id"`
	Question       string `json:"question" binding:"required"`
}

func NewAgentHandler(service *service.AgentService) *AgentHandler {
	return &AgentHandler{service: service}
}

// Chat处理Agent问答请求
func (h *AgentHandler) Chat(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识库id错误")
		return
	}
	var req agentChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "请求参数错误")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	resp, err := h.service.Chat(ctx, service.AgentChatRequest{
		UserID:          userID,
		KnowledgeBaseID: kbID,
		ConversationID:  req.ConversationID,
		Question:        req.Question,
	})
	if err != nil {
		handleChatError(c, err, "Agent问答失败")
		return
	}
	response.Success(c, resp)
}
