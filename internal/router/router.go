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
	SourceHandler   *handler.SourceHandler
	OAuthHandler    *handler.OAuthHandler
}

func New(deps Dependencies) *gin.Engine {
	r := gin.Default()
	r.MaxMultipartMemory = 32 << 20

	api := r.Group("/api/v1")
	registerAuthRoutes(api, deps.AuthHandler)
	registerOAuthRoutes(api, deps.OAuthHandler)
	registerProtectedRoutes(api, deps)

	return r
}

func registerAuthRoutes(api *gin.RouterGroup, authHandler *handler.AuthHandler) {
	auth := api.Group("/auth")
	auth.POST("/register", authHandler.Register)
	auth.POST("/login", authHandler.Login)
}

func registerOAuthRoutes(api *gin.RouterGroup, oauthHandler *handler.OAuthHandler) {
	oauth := api.Group("/oauth")
	oauth.GET("/notion/callback", oauthHandler.NotionCallback)
}

func registerProtectedRoutes(api *gin.RouterGroup, deps Dependencies) {
	protected := api.Group("")
	protected.Use(middleware.Auth(deps.JWTSecret))
	protected.POST("/knowledge-bases", deps.KBHandler.Create)
	protected.GET("/knowledge-bases", deps.KBHandler.List)
	protected.DELETE("/knowledge-bases/:id", deps.KBHandler.Delete)
	protected.POST("/knowledge-bases/:id/documents", deps.DocumentHandler.Upload)
	protected.GET("/knowledge-bases/:id/documents", deps.DocumentHandler.List)
	protected.POST("/knowledge-bases/:id/sources", deps.SourceHandler.Create)
	protected.GET("/knowledge-bases/:id/sources", deps.SourceHandler.List)
	protected.GET("/documents/:id", deps.DocumentHandler.Get)
	protected.DELETE("/documents/:id", deps.DocumentHandler.Delete)
	protected.POST("/documents/:id/reindex", deps.DocumentHandler.Reindex)
	protected.POST("/knowledge-sources/:id/sync", deps.SourceHandler.Sync)
	protected.GET("/knowledge-sources/:id/documents", deps.SourceHandler.Documents)
	protected.GET("/oauth/notion/authorize", deps.OAuthHandler.NotionAuthorize)
	protected.POST("/knowledge-bases/:id/chat", deps.ChatHandler.Chat)
	protected.POST("/knowledge-bases/:id/chat/stream", deps.ChatHandler.Stream)
	protected.GET("/chat-logs", deps.ChatHandler.ListLogs)
	protected.POST("/chat-logs/:id/feedback", deps.ChatHandler.Feedback)
}
