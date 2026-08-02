package main

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"OurAgent/internal/agent"
	"OurAgent/internal/config"
	"OurAgent/internal/database"
	"OurAgent/internal/document"
	"OurAgent/internal/einoapp"
	"OurAgent/internal/handler"
	appoauth "OurAgent/internal/oauth"
	"OurAgent/internal/queue"
	"OurAgent/internal/rag"
	"OurAgent/internal/repository"
	apprerank "OurAgent/internal/rerank"
	"OurAgent/internal/router"
	appsearch "OurAgent/internal/search"
	"OurAgent/internal/service"
	appsource "OurAgent/internal/source"
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
	sourceRepo := repository.NewSourceRepository(db)
	chunkRepo := repository.NewChunkRepository(db)
	chatLogRepo := repository.NewChatLogRepository(db)
	conversationRepo := repository.NewConversationRepository(db)
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
	indexConsumer := tasks.NewIndexConsumer(rabbitClient, documentRepo, sourceRepo, indexer, cfg.Rabbit)
	if err := indexConsumer.Start(ctx, cfg.Rabbit.IndexQueue); err != nil {
		logger.Logger.Fatal("启动文档索引消费者失败", zap.Error(err))
	}
	deleteConsumer := tasks.NewDeleteConsumer(rabbitClient, documentRepo, sourceRepo, chunkRepo, qdrant, keywordStore, minioClient, cfg.Rabbit)
	if err := deleteConsumer.Start(ctx, cfg.Rabbit.DeleteQueue); err != nil {
		logger.Logger.Fatal("启动文档删除清理消费者失败", zap.Error(err))
	}

	authService := service.NewAuthService(userRepo, cfg.JWT.Secret, cfg.JWT.ExpiresHours)
	kbService := service.NewKnowledgeBaseService(kbRepo)
	documentService := service.NewDocumentService(documentRepo, kbRepo, taskProducer, minioClient)
	sourceService := appsource.NewService(sourceRepo, kbRepo, documentRepo, minioClient, taskProducer, cfg.Source)
	sourceConsumer := appsource.NewConsumer(rabbitClient, sourceRepo, sourceService, cfg.Rabbit, cfg.Source)
	if err := sourceConsumer.Start(ctx, cfg.Rabbit.SourceSyncQueue); err != nil {
		logger.Logger.Fatal("启动知识源同步消费者失败", zap.Error(err))
	}
	sourceScheduler := appsource.NewScheduler(sourceRepo, taskProducer, cfg.Source)
	sourceScheduler.Start(ctx)
	var webFallback websearch.Answerer
	if cfg.Web.Enabled {
		webFallback = websearch.NewDashScopeAnswerer(cfg.Web)
	}
	chatService, err := service.NewChatService(context.Background(), kbRepo, documentRepo, chatLogRepo, ragRetriever, queryRewriter, reranker, chatModel, webFallback, cfg)
	if err != nil {
		logger.Logger.Fatal("初始化 RAG Chain 失败", zap.Error(err))
	}
	chatService.ConfigureShortTermMemory(conversationRepo, taskProducer)
	conversationCompactor := service.NewConversationCompactor(conversationRepo, chatLogRepo, chatModel, cfg.Memory)
	conversationCompactConsumer := tasks.NewConversationCompactConsumer(rabbitClient, conversationRepo, conversationCompactor, cfg)
	if err := conversationCompactConsumer.Start(ctx, cfg.Rabbit.ConversationCompactQueue); err != nil {
		logger.Logger.Fatal("启动会话摘要消费者失败", zap.Error(err))
	}

	authHandler := handler.NewAuthHandler(authService)
	kbHandler := handler.NewKnowledgeBaseHandler(kbService)
	documentHandler := handler.NewDocumentHandler(documentService)
	sourceHandler := handler.NewSourceHandler(sourceService)
	oauthHandler := handler.NewOAuthHandler(appoauth.NewNotionService(cfg.OAuth.Notion, cfg.JWT.Secret, sourceRepo))
	chatHandler := handler.NewChatHandler(chatService)
	agentPlanner := agent.NewLLMPlanner(rewriteChatModel)
	agentService := service.NewAgentService(chatService, agentPlanner, chatModel)
	agentService.ConfigureShortTermMemory(conversationRepo, service.NewConversationContextAssembler(conversationRepo, chatLogRepo, cfg.Memory), cfg.Memory)
	agentHandler := handler.NewAgentHandler(agentService)

	r := router.New(router.Dependencies{
		JWTSecret:       cfg.JWT.Secret,
		AuthHandler:     authHandler,
		KBHandler:       kbHandler,
		DocumentHandler: documentHandler,
		ChatHandler:     chatHandler,
		AgentHandler:    agentHandler,
		SourceHandler:   sourceHandler,
		OAuthHandler:    oauthHandler,
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	logger.Logger.Info("OurAgent 服务已启动", zap.String("addr", addr))
	server := &http.Server{Addr: addr, Handler: r}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErr:
		if err != nil && !stderrors.Is(err, http.ErrServerClosed) {
			logger.Logger.Fatal("启动服务失败", zap.Error(err))
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Logger.Error("关闭HTTP服务失败", zap.Error(err))
		}
	}
}
