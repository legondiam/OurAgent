package handler

import (
	"context"
	"strconv"
	"time"

	"OurAgent/internal/service"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

type ChatHandler struct {
	service *service.ChatService
}

func NewChatHandler(service *service.ChatService) *ChatHandler {
	return &ChatHandler{service: service}
}

type chatRequest struct {
	Question         string   `json:"question" binding:"required"`
	TopK             int      `json:"top_k"`
	ScoreThreshold   *float64 `json:"score_threshold"`
	MaxContextTokens int      `json:"max_context_tokens"`
	StrictMode       *bool    `json:"strict_mode"`
}

type feedbackRequest struct {
	Rating string `json:"rating" binding:"required"`
	Reason string `json:"reason"`
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

	resp, err := h.service.Chat(ctx, service.ChatRequest{
		UserID:           userID,
		KnowledgeBaseID:  kbID,
		Question:         req.Question,
		TopK:             req.TopK,
		ScoreThreshold:   req.ScoreThreshold,
		MaxContextTokens: req.MaxContextTokens,
		StrictMode:       req.StrictMode,
	})
	if err != nil {
		handleChatError(c, err, "问答失败")
		return
	}
	response.Success(c, resp)
}

// Stream 处理知识库流式问答请求
func (h *ChatHandler) Stream(c *gin.Context) {
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	events, err := h.service.Stream(ctx, service.ChatRequest{
		UserID:           userID,
		KnowledgeBaseID:  kbID,
		Question:         req.Question,
		TopK:             req.TopK,
		ScoreThreshold:   req.ScoreThreshold,
		MaxContextTokens: req.MaxContextTokens,
		StrictMode:       req.StrictMode,
	})
	if err != nil {
		handleChatError(c, err, "问答失败")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	for event := range events {
		if event.Err != nil {
			c.SSEvent("error", gin.H{"message": event.Err.Error()})
			c.Writer.Flush()
			return
		}
		switch event.Type {
		case "message":
			c.SSEvent("message", gin.H{"content": event.Content})
		case "sources":
			c.SSEvent("sources", gin.H{"sources": event.Sources})
		case "done":
			c.SSEvent("done", gin.H{"chat_log_id": event.ChatLogID})
		}
		c.Writer.Flush()
	}
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

// Feedback 处理问答反馈请求
func (h *ChatHandler) Feedback(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	logID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "问答日志 id 错误")
		return
	}
	var req feedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "请求参数错误")
		return
	}
	feedback, err := h.service.SubmitFeedback(service.FeedbackInput{
		UserID:    userID,
		ChatLogID: logID,
		Rating:    req.Rating,
		Reason:    req.Reason,
	})
	if err != nil {
		handleChatError(c, err, "提交反馈失败")
		return
	}
	response.Success(c, feedback)
}
