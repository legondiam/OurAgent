package handler

import (
	"net/http"
	"strconv"
	"time"

	"OurAgent/internal/repository"
	"OurAgent/internal/service"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

type MemoryHandler struct {
	service *service.MemoryLifecycleService
}

// NewMemoryHandler 创建长期记忆管理接口
func NewMemoryHandler(memoryService *service.MemoryLifecycleService) *MemoryHandler {
	return &MemoryHandler{service: memoryService}
}

// List 处理长期记忆分页查询
func (h *MemoryHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	filter := repository.MemoryListFilter{UserID: userID, Scope: c.Query("scope"), Type: c.Query("type"), Status: c.Query("status")}
	if raw := c.Query("knowledge_base_id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			response.BusinessError(c, response.CodeInvalidParam, "knowledge_base_id错误")
			return
		}
		filter.KnowledgeBaseID = &id
	}
	filter.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	filter.PageSize, _ = strconv.Atoi(c.DefaultQuery("page_size", "20"))
	items, total, err := h.service.List(filter)
	if err != nil {
		response.ServerError(c, "查询长期记忆失败")
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": filter.Page, "page_size": filter.PageSize})
}

// Confirm 处理候选记忆确认
func (h *MemoryHandler) Confirm(c *gin.Context) {
	userID, id, ok := memoryPathIDs(c)
	if !ok {
		return
	}
	if err := h.service.Confirm(userID, id); err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "确认长期记忆失败")
		return
	}
	response.Success(c, gin.H{"confirmed": true})
}

type updateMemoryRequest struct {
	Content         *string    `json:"content"`
	Value           *string    `json:"value"`
	Scope           *string    `json:"scope"`
	KnowledgeBaseID *uint64    `json:"knowledge_base_id"`
	Durability      *string    `json:"durability"`
	ExpiresAt       *time.Time `json:"expires_at"`
}

// Update 处理长期记忆修改
func (h *MemoryHandler) Update(c *gin.Context) {
	userID, id, ok := memoryPathIDs(c)
	if !ok {
		return
	}
	var req updateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "请求参数错误")
		return
	}
	memory, err := h.service.Update(userID, id, service.UpdateMemoryInput{Content: req.Content, Value: req.Value, Scope: req.Scope, KnowledgeBaseID: req.KnowledgeBaseID, Durability: req.Durability, ExpiresAt: req.ExpiresAt})
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "修改长期记忆失败")
		return
	}
	response.Success(c, memory)
}

// Delete 处理单条长期记忆删除
func (h *MemoryHandler) Delete(c *gin.Context) {
	userID, id, ok := memoryPathIDs(c)
	if !ok {
		return
	}
	if err := h.service.Delete(userID, id); err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "删除长期记忆失败")
		return
	}
	c.JSON(http.StatusAccepted, response.Body{Code: response.CodeOK, Msg: "success", Data: gin.H{"status": "deletion_pending", "chat_logs_deleted": false}})
}

// DeleteByScope 处理按作用域批量删除长期记忆
func (h *MemoryHandler) DeleteByScope(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	if c.Query("confirm") != "true" {
		response.BusinessError(c, response.CodeInvalidParam, "批量删除必须携带confirm=true")
		return
	}
	scope := c.Query("scope")
	var kbID *uint64
	if raw := c.Query("knowledge_base_id"); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			response.BusinessError(c, response.CodeInvalidParam, "knowledge_base_id错误")
			return
		}
		kbID = &id
	}
	count, err := h.service.DeleteByScope(userID, scope, kbID)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "批量删除长期记忆失败")
		return
	}
	c.JSON(http.StatusAccepted, response.Body{Code: response.CodeOK, Msg: "success", Data: gin.H{"status": "deletion_pending", "count": count, "chat_logs_deleted": false}})
}

func memoryPathIDs(c *gin.Context) (uint64, uint64, bool) {
	userID, ok := currentUserID(c)
	if !ok {
		return 0, 0, false
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "memory id错误")
		return 0, 0, false
	}
	return userID, id, true
}
