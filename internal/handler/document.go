package handler

import (
	"context"
	"strconv"
	"time"

	"OurAgent/internal/service"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

type DocumentHandler struct {
	service *service.DocumentService
}

func NewDocumentHandler(service *service.DocumentService) *DocumentHandler {
	return &DocumentHandler{service: service}
}

// Upload 处理文档上传请求
func (h *DocumentHandler) Upload(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识库 id 错误")
		return
	}
	file, err := c.FormFile("file")
	if err != nil {
		response.BusinessError(c, response.CodeMissingFile, "文件为空")
		return
	}

	doc, err := h.service.Upload(service.UploadDocumentInput{
		UserID: userID,
		KBID:   kbID,
		File:   file,
		Save:   c.SaveUploadedFile,
	})
	if err != nil {
		handleDocumentError(c, err, "上传文档失败")
		return
	}
	response.Success(c, gin.H{"document_id": doc.ID, "status": doc.Status})
}

// List 处理查询文档列表请求
func (h *DocumentHandler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	kbID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识库 id 错误")
		return
	}
	docs, err := h.service.List(userID, kbID)
	if err != nil {
		handleDocumentError(c, err, "查询文档失败")
		return
	}
	response.Success(c, docs)
}

// Get 处理查询文档详情请求
func (h *DocumentHandler) Get(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	docID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "文档 id 错误")
		return
	}

	doc, err := h.service.Get(userID, docID)
	if err != nil {
		handleDocumentError(c, err, "查询文档失败")
		return
	}
	response.Success(c, doc)
}

// Delete 处理删除文档请求
func (h *DocumentHandler) Delete(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	docID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "文档 id 错误")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if err := h.service.Delete(ctx, userID, docID); err != nil {
		handleDocumentError(c, err, "删除文档失败")
		return
	}
	response.Success(c, nil)
}

// Reindex 处理重新索引文档请求
func (h *DocumentHandler) Reindex(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	docID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "文档 id 错误")
		return
	}
	doc, err := h.service.Reindex(userID, docID)
	if err != nil {
		handleDocumentError(c, err, "重建索引失败")
		return
	}
	response.Success(c, gin.H{"document_id": doc.ID, "status": doc.Status})
}
