package handler

import (
	"encoding/json"
	"strconv"

	appsource "OurAgent/internal/source"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

type SourceHandler struct {
	service *appsource.Service
}

func NewSourceHandler(service *appsource.Service) *SourceHandler {
	return &SourceHandler{service: service}
}

type createSourceRequest struct {
	Provider            string          `json:"provider" binding:"required"`
	Name                string          `json:"name" binding:"required"`
	Config              json.RawMessage `json:"config"`
	Credential          json.RawMessage `json:"credential"`
	SyncIntervalSeconds int             `json:"sync_interval_seconds"`
}

// Create 创建外部知识源
func (h *SourceHandler) Create(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识库id错误")
		return
	}
	var req createSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "请求参数错误")
		return
	}
	source, err := h.service.CreateSource(appsource.CreateSourceInput{
		UserID:              userID,
		KnowledgeBaseID:     kbID,
		Provider:            req.Provider,
		Name:                req.Name,
		Config:              req.Config,
		Credential:          req.Credential,
		SyncIntervalSeconds: req.SyncIntervalSeconds,
	})
	if err != nil {
		response.ServerError(c, "创建知识源失败")
		return
	}
	response.Success(c, source)
}

// List 查询知识库下的外部知识源
func (h *SourceHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识库id错误")
		return
	}
	sources, err := h.service.ListSources(userID, kbID)
	if err != nil {
		response.ServerError(c, "查询知识源失败")
		return
	}
	response.Success(c, sources)
}

// Sync 手动投递知识源同步任务
func (h *SourceHandler) Sync(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	sourceID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识源id错误")
		return
	}
	source, err := h.service.TriggerSync(c.Request.Context(), userID, sourceID)
	if err != nil {
		response.ServerError(c, "投递知识源同步任务失败")
		return
	}
	response.Success(c, gin.H{"id": source.ID, "sync_status": source.SyncStatus})
}

// Documents 查询外部文档映射
func (h *SourceHandler) Documents(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	sourceID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识源id错误")
		return
	}
	docs, err := h.service.ListExternalDocuments(userID, sourceID)
	if err != nil {
		response.ServerError(c, "查询外部文档失败")
		return
	}
	response.Success(c, docs)
}
