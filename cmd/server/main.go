package main

import (
	"context"
	"fmt"

	"OurAgent/internal/config"
	"OurAgent/internal/database"
	"OurAgent/internal/document"
	"OurAgent/internal/einoapp"
	"OurAgent/internal/handler"
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
	embedder, err := einoapp.NewEmbedding(context.Background(), cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.EmbeddingModel)
	if err != nil {
		logger.Logger.Fatal("初始化 Eino Embedding 失败", zap.Error(err))
	}
	chatModel, err := einoapp.NewChatModel(context.Background(), cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.ChatModel)
	if err != nil {
		logger.Logger.Fatal("初始化 Eino ChatModel 失败", zap.Error(err))
	}
	rewriteChatModel, err := einoapp.NewChatModelWithTemperature(context.Background(), cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.ChatModel, cfg.RAG.QueryRewriteModelTemperature)
	if err != nil {
		logger.Logger.Fatal("初始化查询改写模型失败", zap.Error(err))
	}

	userRepo := repository.NewUserRepository(db)
	kbRepo := repository.NewKnowledgeBaseRepository(db)
	documentRepo := repository.NewDocumentRepository(db)
	chunkRepo := repository.NewChunkRepository(db)
	chatLogRepo := repository.NewChatLogRepository(db)
	ragRetriever := rag.NewQdrantRetriever(documentRepo, chunkRepo, qdrant, embedder)
	queryRewriter := rag.NewLLMQueryRewriter(rewriteChatModel)

	indexer := document.NewIndexer(db, qdrant, embedder, cfg)
	authService := service.NewAuthService(userRepo, cfg.JWT.Secret, cfg.JWT.ExpiresHours)
	kbService := service.NewKnowledgeBaseService(kbRepo)
	documentService := service.NewDocumentService(documentRepo, chunkRepo, kbRepo, indexer, qdrant, cfg)
	chatService, err := service.NewChatService(context.Background(), kbRepo, documentRepo, chatLogRepo, ragRetriever, queryRewriter, chatModel, cfg)
	if err != nil {
		logger.Logger.Fatal("初始化 RAG Chain 失败", zap.Error(err))
	}

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
