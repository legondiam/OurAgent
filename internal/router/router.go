package router

import (
	"OurAgent/internal/handler"
	"OurAgent/internal/middleware"

	"github.com/gin-gonic/gin"
)

type Dependencies struct {
	JWTSecret       string
	AuthHandler     *handler.AuthHandler
	KBHandler       *handler.KnowledgeBaseHandler
	DocumentHandler *handler.DocumentHandler
	ChatHandler     *handler.ChatHandler
}

func New(deps Dependencies) *gin.Engine {
	r := gin.Default()
	r.MaxMultipartMemory = 32 << 20

	api := r.Group("/api/v1")
	registerAuthRoutes(api, deps.AuthHandler)
	registerProtectedRoutes(api, deps)

	return r
}

func registerAuthRoutes(api *gin.RouterGroup, authHandler *handler.AuthHandler) {
	auth := api.Group("/auth")
	auth.POST("/register", authHandler.Register)
	auth.POST("/login", authHandler.Login)
}

func registerProtectedRoutes(api *gin.RouterGroup, deps Dependencies) {
	protected := api.Group("")
	protected.Use(middleware.Auth(deps.JWTSecret))
	protected.POST("/knowledge-bases", deps.KBHandler.Create)
	protected.GET("/knowledge-bases", deps.KBHandler.List)
	protected.DELETE("/knowledge-bases/:id", deps.KBHandler.Delete)
	protected.POST("/knowledge-bases/:id/documents", deps.DocumentHandler.Upload)
	protected.GET("/knowledge-bases/:id/documents", deps.DocumentHandler.List)
	protected.GET("/documents/:id", deps.DocumentHandler.Get)
	protected.POST("/knowledge-bases/:id/chat", deps.ChatHandler.Chat)
	protected.GET("/chat-logs", deps.ChatHandler.ListLogs)
}
