package handler

import (
	"context"
	"strconv"
	"time"

	"OurAgent/internal/rag"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

type ChatHandler struct {
	service *rag.Service
}

func NewChatHandler(service *rag.Service) *ChatHandler {
	return &ChatHandler{service: service}
}

type chatRequest struct {
	Question string `json:"question" binding:"required"`
	TopK     int    `json:"top_k"`
}

// Chat 处理知识库问答请求
func (h *ChatHandler) Chat(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识库 id 错误")
		return
	}
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "请求参数错误")
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	resp, err := h.service.Chat(ctx, rag.ChatRequest{
		UserID:          userID,
		KnowledgeBaseID: kbID,
		Question:        req.Question,
		TopK:            req.TopK,
	})
	if err != nil {
		handleChatError(c, err, "问答失败")
		return
	}
	response.Success(c, resp)
}

// ListLogs 处理查询问答日志请求
func (h *ChatHandler) ListLogs(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	logs, err := h.service.ListLogs(userID)
	if err != nil {
		handleChatError(c, err, "查询问答日志失败")
		return
	}
	response.Success(c, logs)
}
