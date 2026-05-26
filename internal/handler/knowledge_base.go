package handler

import (
	"strconv"

	"OurAgent/internal/service"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

type KnowledgeBaseHandler struct {
	service *service.KnowledgeBaseService
}

func NewKnowledgeBaseHandler(service *service.KnowledgeBaseService) *KnowledgeBaseHandler {
	return &KnowledgeBaseHandler{service: service}
}

type createKnowledgeBaseRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// Create 处理创建知识库请求
func (h *KnowledgeBaseHandler) Create(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	var req createKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "请求参数错误")
		return
	}

	kb, err := h.service.Create(service.CreateKnowledgeBaseInput{
		UserID:      userID,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		handleKnowledgeBaseError(c, err, "创建知识库失败")
		return
	}
	response.Success(c, kb)
}

// List 处理查询知识库列表请求
func (h *KnowledgeBaseHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	items, err := h.service.List(userID)
	if err != nil {
		handleKnowledgeBaseError(c, err, "查询知识库失败")
		return
	}
	response.Success(c, items)
}

// Delete 处理删除知识库请求
func (h *KnowledgeBaseHandler) Delete(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识库 id 错误")
		return
	}

	if err := h.service.Delete(userID, id); err != nil {
		handleKnowledgeBaseError(c, err, "删除知识库失败")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
