package main

import (
	"context"
	"fmt"
	"time"

	"OurAgent/internal/config"
	"OurAgent/internal/database"
	"OurAgent/internal/document"
	"OurAgent/internal/einoapp"
	"OurAgent/internal/handler"
	"OurAgent/internal/queue"
	"OurAgent/internal/rag"
	"OurAgent/internal/repository"
	apprerank "OurAgent/internal/rerank"
	"OurAgent/internal/router"
	appsearch "OurAgent/internal/search"
	"OurAgent/internal/service"
	"OurAgent/internal/storage"
	"OurAgent/internal/tasks"
	"OurAgent/internal/vectorstore"
	"OurAgent/internal/websearch"
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
	minioClient, err := storage.NewMinIOClient(context.Background(), cfg.MinIO)
	if err != nil {
		logger.Logger.Fatal("初始化MinIO失败", zap.Error(err))
	}
	keywordStore, err := appsearch.NewBlugeStore(cfg.Search.BlugeDir)
	if err != nil {
		logger.Logger.Fatal("初始化Bluge关键词索引失败", zap.Error(err))
	}
	defer keywordStore.Close()

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
	vectorRetriever := rag.NewQdrantRetriever(documentRepo, chunkRepo, qdrant, embedder)
	bm25Retriever := rag.NewBM25Retriever(documentRepo, chunkRepo, keywordStore)
	ragRetriever := rag.NewHybridRetriever(vectorRetriever, bm25Retriever)
	queryRewriter := rag.NewLLMQueryRewriter(rewriteChatModel)
	reranker := apprerank.NewDashScopeReranker(
		cfg.Rerank.BaseURL,
		cfg.Rerank.APIKey,
		cfg.Rerank.Model,
		time.Duration(cfg.Rerank.TimeoutSeconds)*time.Second,
	)

	indexer := document.NewIndexer(db, qdrant, keywordStore, minioClient, embedder, cfg)
	if !cfg.Rabbit.Enabled {
		logger.Logger.Fatal("RabbitMQ 未启用，无法启动文档异步任务")
	}
	rabbitClient, err := queue.NewRabbitMQClient(cfg.Rabbit)
	if err != nil {
		logger.Logger.Fatal("初始化 RabbitMQ 失败", zap.Error(err))
	}
	defer rabbitClient.Close()
	taskProducer := tasks.NewProducer(rabbitClient)
	indexConsumer := tasks.NewIndexConsumer(rabbitClient, documentRepo, indexer, cfg.Rabbit)
	if err := indexConsumer.Start(context.Background(), cfg.Rabbit.IndexQueue); err != nil {
		logger.Logger.Fatal("启动文档索引消费者失败", zap.Error(err))
	}
	deleteConsumer := tasks.NewDeleteConsumer(rabbitClient, documentRepo, chunkRepo, qdrant, keywordStore, minioClient, cfg.Rabbit)
	if err := deleteConsumer.Start(context.Background(), cfg.Rabbit.DeleteQueue); err != nil {
		logger.Logger.Fatal("启动文档删除清理消费者失败", zap.Error(err))
	}

	authService := service.NewAuthService(userRepo, cfg.JWT.Secret, cfg.JWT.ExpiresHours)
	kbService := service.NewKnowledgeBaseService(kbRepo)
	documentService := service.NewDocumentService(documentRepo, kbRepo, taskProducer, minioClient)
	var webFallback websearch.Answerer
	if cfg.Web.Enabled {
		webFallback = websearch.NewDashScopeAnswerer(cfg.Web)
	}
	chatService, err := service.NewChatService(context.Background(), kbRepo, documentRepo, chatLogRepo, ragRetriever, queryRewriter, reranker, chatModel, webFallback, cfg)
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
