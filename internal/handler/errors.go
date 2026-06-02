package handler

import (
	stderrors "errors"

	"OurAgent/internal/service"
	"OurAgent/pkg/logger"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

func handleAuthError(c *gin.Context, err error, fallback string) {
	switch {
	case stderrors.Is(err, service.ErrUserExisted):
		response.BusinessError(c, response.CodeUserExisted, "用户已存在")
		logger.S.Infof("用户已存在：%+v", err)
	case stderrors.Is(err, service.ErrAccountOrPassword):
		response.BusinessError(c, response.CodeAccountOrPassword, "用户名或密码错误")
		logger.S.Infof("用户名或密码错误：%+v", err)
	default:
		response.ServerError(c, fallback)
		logger.S.Errorf("服务器错误：%+v", err)
	}
}

func handleKnowledgeBaseError(c *gin.Context, err error, fallback string) {
	switch {
	case stderrors.Is(err, service.ErrKnowledgeBaseNotFound):
		response.BusinessError(c, response.CodeKnowledgeBaseNotFound, "知识库不存在")
		logger.S.Infof("知识库不存在：%+v", err)
	default:
		response.ServerError(c, fallback)
		logger.S.Errorf("服务器错误：%+v", err)
	}
}

func handleDocumentError(c *gin.Context, err error, fallback string) {
	switch {
	case stderrors.Is(err, service.ErrKnowledgeBaseNotFound):
		response.BusinessError(c, response.CodeKnowledgeBaseNotFound, "知识库不存在")
		logger.S.Infof("知识库不存在：%+v", err)
	case stderrors.Is(err, service.ErrDocumentNotFound):
		response.BusinessError(c, response.CodeDocumentNotFound, "文档不存在")
		logger.S.Infof("文档不存在：%+v", err)
	case stderrors.Is(err, service.ErrMissingFile):
		response.BusinessError(c, response.CodeMissingFile, "文件为空")
		logger.S.Infof("文件为空：%+v", err)
	case stderrors.Is(err, service.ErrUnsupportedFileType):
		response.BusinessError(c, response.CodeUnsupportedFileType, "文件类型不支持")
		logger.S.Infof("文件类型不支持：%+v", err)
	case stderrors.Is(err, service.ErrDocumentIndexing):
		response.BusinessError(c, response.CodeDocumentIndexing, "文档正在索引中，请稍后再试")
		logger.S.Infof("文档正在索引中：%+v", err)
	default:
		response.ServerError(c, fallback)
		logger.S.Errorf("服务器错误：%+v", err)
	}
}

func handleChatError(c *gin.Context, err error, fallback string) {
	switch {
	case stderrors.Is(err, service.ErrQuestionEmpty):
		response.BusinessError(c, response.CodeQuestionEmpty, "问题不能为空")
		logger.S.Infof("问题为空：%+v", err)
	case stderrors.Is(err, service.ErrKnowledgeBaseNotFound):
		response.BusinessError(c, response.CodeKnowledgeBaseNotFound, "知识库不存在")
		logger.S.Infof("知识库不存在：%+v", err)
	case stderrors.Is(err, service.ErrChatLogNotFound):
		response.BusinessError(c, response.CodeChatLogNotFound, "问答日志不存在")
		logger.S.Infof("问答日志不存在：%+v", err)
	case stderrors.Is(err, service.ErrInvalidFeedback):
		response.BusinessError(c, response.CodeInvalidFeedback, "反馈参数错误")
		logger.S.Infof("反馈参数错误：%+v", err)
	default:
		response.ServerError(c, fallback)
		logger.S.Errorf("服务器错误：%+v", err)
	}
}
