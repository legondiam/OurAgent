package handler

import (
	"strconv"

	appoauth "OurAgent/internal/oauth"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

type OAuthHandler struct {
	notion *appoauth.NotionService
}

func NewOAuthHandler(notion *appoauth.NotionService) *OAuthHandler {
	return &OAuthHandler{notion: notion}
}

// NotionAuthorize生成Notion授权地址
func (h *OAuthHandler) NotionAuthorize(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}
	sourceID, err := parseUintQuery(c, "source_id")
	if err != nil {
		response.BusinessError(c, response.CodeInvalidParam, "知识源id错误")
		return
	}
	url, err := h.notion.AuthorizeURL(c.Request.Context(), userID, sourceID)
	if err != nil {
		response.ServerError(c, "生成Notion授权地址失败")
		return
	}
	response.Success(c, gin.H{"authorize_url": url})
}

// NotionCallback处理Notion授权回调
func (h *OAuthHandler) NotionCallback(c *gin.Context) {
	result, err := h.notion.HandleCallback(c.Request.Context(), c.Query("code"), c.Query("state"))
	if err != nil {
		response.ServerError(c, "Notion授权失败")
		return
	}
	response.Success(c, result)
}

func parseUintQuery(c *gin.Context, key string) (uint64, error) {
	return strconv.ParseUint(c.Query(key), 10, 64)
}
