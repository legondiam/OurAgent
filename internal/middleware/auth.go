package middleware

import (
	"strconv"
	"strings"

	"OurAgent/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const UserIDKey = "user_id"

// Auth 校验 JWT 并写入当前用户 ID
func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			response.Unauthorized(c, "未登录或 token 为空")
			c.Abort()
			return
		}

		tokenString := strings.TrimPrefix(header, "Bearer ")
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			response.Unauthorized(c, "token 无效")
			c.Abort()
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			response.Unauthorized(c, "token 无效")
			c.Abort()
			return
		}

		sub, err := claims.GetSubject()
		if err != nil || sub == "" {
			response.Unauthorized(c, "token 无效")
			c.Abort()
			return
		}
		userID, err := strconv.ParseUint(sub, 10, 64)
		if err != nil {
			response.Unauthorized(c, "token 无效")
			c.Abort()
			return
		}

		c.Set(UserIDKey, userID)
		c.Next()
	}
}

// CurrentUserID 获取当前用户 ID
func CurrentUserID(c *gin.Context) (uint64, bool) {
	value, exists := c.Get(UserIDKey)
	if !exists {
		return 0, false
	}
	userID, ok := value.(uint64)
	return userID, ok
}
