package response

import "github.com/gin-gonic/gin"

const (
	CodeOK           = 0
	CodeInvalidParam = 1001
	CodeUnauthorized = 1002
	CodeForbidden    = 1003

	CodeUserExisted       = 2001
	CodeAccountOrPassword = 2002

	CodeKnowledgeBaseNotFound = 3001
	CodeDocumentNotFound      = 3002
	CodeMissingFile           = 3003
	CodeUnsupportedFileType   = 3004
	CodeQuestionEmpty         = 3005
	CodeChatLogNotFound       = 3006
	CodeInvalidFeedback       = 3007
	CodeDocumentIndexing      = 3008
	CodeConversationBusy      = 3009
	CodeConversationExpired   = 3010

	CodeInternal = 5000
)

type Body struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

// Success 返回成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(200, Body{Code: CodeOK, Msg: "success", Data: data})
}

// BusinessError 返回业务错误响应
func BusinessError(c *gin.Context, code int, msg string) {
	c.JSON(400, Body{Code: code, Msg: msg})
}

// Conflict返回资源并发冲突
func Conflict(c *gin.Context, code int, msg string) {
	c.JSON(409, Body{Code: code, Msg: msg})
}

// Gone返回已过期资源
func Gone(c *gin.Context, code int, msg string) {
	c.JSON(410, Body{Code: code, Msg: msg})
}

// ServerError 返回服务器错误响应
func ServerError(c *gin.Context, msg string) {
	c.JSON(500, Body{Code: CodeInternal, Msg: msg})
}

// Unauthorized 返回未认证错误响应
func Unauthorized(c *gin.Context, msg string) {
	c.JSON(401, Body{Code: CodeUnauthorized, Msg: msg})
}
