package main

import (
	"fmt"

	"OurAgent/internal/config"
	"OurAgent/internal/database"
	"OurAgent/internal/document"
	"OurAgent/internal/handler"
	"OurAgent/internal/llm"
	"OurAgent/internal/rag"
	"OurAgent/internal/repository"
	"OurAgent/internal/router"
	"OurAgent/internal/service"
	"OurAgent/internal/vectorstore"
	"OurAgent/pkg/logger"

	"go.uber.org/zap"
)

func main() {
	logger.Init()
	defer logger.Sync()

	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Logger.Fatal("加载配置失败", zap.Error(err))
	}

	db, err := database.Connect(cfg.MySQL.DSN)
	if err != nil {
		logger.Logger.Fatal("连接 MySQL 失败", zap.Error(err))
	}
	if err := database.AutoMigrate(db); err != nil {
		logger.Logger.Fatal("数据库自动迁移失败", zap.Error(err))
	}

	qdrant := vectorstore.NewQdrantClient(cfg.Qdrant.URL, cfg.Qdrant.Collection)
	embedder := llm.NewOpenAICompatibleEmbedding(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.EmbeddingModel)
	chatModel := llm.NewOpenAICompatibleChat(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.ChatModel)

	userRepo := repository.NewUserRepository(db)
	kbRepo := repository.NewKnowledgeBaseRepository(db)
	documentRepo := repository.NewDocumentRepository(db)
	chunkRepo := repository.NewChunkRepository(db)
	chatLogRepo := repository.NewChatLogRepository(db)

	indexer := document.NewIndexer(db, qdrant, embedder, cfg)
	authService := service.NewAuthService(userRepo, cfg.JWT.Secret, cfg.JWT.ExpiresHours)
	kbService := service.NewKnowledgeBaseService(kbRepo)
	documentService := service.NewDocumentService(documentRepo, kbRepo, indexer, cfg)
	chatService := rag.NewService(kbRepo, documentRepo, chunkRepo, chatLogRepo, qdrant, embedder, chatModel, cfg)

	authHandler := handler.NewAuthHandler(authService)
	kbHandler := handler.NewKnowledgeBaseHandler(kbService)
	documentHandler := handler.NewDocumentHandler(documentService)
	chatHandler := handler.NewChatHandler(chatService)

	r := router.New(router.Dependencies{
		JWTSecret:       cfg.JWT.Secret,
		AuthHandler:     authHandler,
		KBHandler:       kbHandler,
		DocumentHandler: documentHandler,
		ChatHandler:     chatHandler,
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Logger.Info("OurAgent 服务已启动", zap.String("addr", addr))
	if err := r.Run(addr); err != nil {
		logger.Logger.Fatal("启动服务失败", zap.Error(err))
	}
}
