package handler

import (
	"OurAgent/internal/middleware"
	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
)

func currentUserID(c *gin.Context) (uint64, bool) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok || userID == 0 {
		response.Unauthorized(c, "未登录或 token 无效")
		return 0, false
	}
	return userID, true
}
