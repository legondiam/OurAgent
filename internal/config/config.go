package config

import (
	"errors"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	Server ServerConfig    `yaml:"server" mapstructure:"server"`
	MySQL  MySQLConfig     `yaml:"mysql" mapstructure:"mysql"`
	Qdrant QdrantConfig    `yaml:"qdrant" mapstructure:"qdrant"`
	MinIO  MinIOConfig     `yaml:"minio" mapstructure:"minio"`
	LLM    LLMConfig       `yaml:"llm" mapstructure:"llm"`
	RAG    RAGConfig       `yaml:"rag" mapstructure:"rag"`
	Rerank RerankConfig    `yaml:"rerank" mapstructure:"rerank"`
	JWT    JWTConfig       `yaml:"jwt" mapstructure:"jwt"`
	Search SearchConfig    `yaml:"search" mapstructure:"search"`
	Rabbit RabbitMQConfig  `yaml:"rabbitmq" mapstructure:"rabbitmq"`
	Web    WebSearchConfig `yaml:"web_search" mapstructure:"web_search"`
	OAuth  OAuthConfig     `yaml:"oauth" mapstructure:"oauth"`
}

type ServerConfig struct {
	Port int `yaml:"port" mapstructure:"port"`
}

type MySQLConfig struct {
	DSN string `yaml:"dsn" mapstructure:"dsn"`
}

type QdrantConfig struct {
	URL        string `yaml:"url" mapstructure:"url"`
	Collection string `yaml:"collection" mapstructure:"collection"`
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint" mapstructure:"endpoint"`
	AccessKey string `yaml:"access_key" mapstructure:"access_key"`
	SecretKey string `yaml:"secret_key" mapstructure:"secret_key"`
	Bucket    string `yaml:"bucket" mapstructure:"bucket"`
	UseSSL    bool   `yaml:"use_ssl" mapstructure:"use_ssl"`
}

type LLMConfig struct {
	BaseURL        string `yaml:"base_url" mapstructure:"base_url"`
	APIKey         string `yaml:"api_key" mapstructure:"api_key"`
	ChatModel      string `yaml:"chat_model" mapstructure:"chat_model"`
	EmbeddingModel string `yaml:"embedding_model" mapstructure:"embedding_model"`
}

type RAGConfig struct {
	ChunkSize                    int     `yaml:"chunk_size" mapstructure:"chunk_size"`
	ChunkOverlap                 int     `yaml:"chunk_overlap" mapstructure:"chunk_overlap"`
	TopK                         int     `yaml:"top_k" mapstructure:"top_k"`
	ScoreThreshold               float64 `yaml:"score_threshold" mapstructure:"score_threshold"`
	MaxContextTokens             int     `yaml:"max_context_tokens" mapstructure:"max_context_tokens"`
	StrictMode                   bool    `yaml:"strict_mode" mapstructure:"strict_mode"`
	QueryRewriteEnabled          bool    `yaml:"query_rewrite_enabled" mapstructure:"query_rewrite_enabled"`
	QueryRewriteMaxQueries       int     `yaml:"query_rewrite_max_queries" mapstructure:"query_rewrite_max_queries"`
	QueryRewriteIncludeOriginal  bool    `yaml:"query_rewrite_include_original" mapstructure:"query_rewrite_include_original"`
	QueryRewriteModelTemperature float64 `yaml:"query_rewrite_model_temperature" mapstructure:"query_rewrite_model_temperature"`
	QueryRewriteHydeEnabled      bool    `yaml:"query_rewrite_hyde_enabled" mapstructure:"query_rewrite_hyde_enabled"`
	QueryRewriteStepBackEnabled  bool    `yaml:"query_rewrite_step_back_enabled" mapstructure:"query_rewrite_step_back_enabled"`
	HybridEnabled                bool    `yaml:"hybrid_enabled" mapstructure:"hybrid_enabled"`
	BM25Enabled                  bool    `yaml:"bm25_enabled" mapstructure:"bm25_enabled"`
	BM25TopK                     int     `yaml:"bm25_top_k" mapstructure:"bm25_top_k"`
	RRFK                         int     `yaml:"rrf_k" mapstructure:"rrf_k"`
}

type RerankConfig struct {
	Enabled        bool   `yaml:"enabled" mapstructure:"enabled"`
	Provider       string `yaml:"provider" mapstructure:"provider"`
	BaseURL        string `yaml:"base_url" mapstructure:"base_url"`
	APIKey         string `yaml:"api_key" mapstructure:"api_key"`
	Model          string `yaml:"model" mapstructure:"model"`
	CandidateLimit int    `yaml:"candidate_limit" mapstructure:"candidate_limit"`
	TopN           int    `yaml:"top_n" mapstructure:"top_n"`
	TimeoutSeconds int    `yaml:"timeout_seconds" mapstructure:"timeout_seconds"`
}

type JWTConfig struct {
	Secret       string `yaml:"secret" mapstructure:"secret"`
	ExpiresHours int    `yaml:"expires_hours" mapstructure:"expires_hours"`
}

type SearchConfig struct {
	BlugeDir string `yaml:"bluge_dir" mapstructure:"bluge_dir"`
}

type RabbitMQConfig struct {
	Enabled           bool   `yaml:"enabled" mapstructure:"enabled"`
	URL               string `yaml:"url" mapstructure:"url"`
	Exchange          string `yaml:"exchange" mapstructure:"exchange"`
	IndexQueue        string `yaml:"index_queue" mapstructure:"index_queue"`
	DeleteQueue       string `yaml:"delete_queue" mapstructure:"delete_queue"`
	SourceSyncQueue   string `yaml:"source_sync_queue" mapstructure:"source_sync_queue"`
	RetryDelaySeconds int    `yaml:"retry_delay_seconds" mapstructure:"retry_delay_seconds"`
	MaxRetries        int    `yaml:"max_retries" mapstructure:"max_retries"`
	IndexWorkers      int    `yaml:"index_workers" mapstructure:"index_workers"`
	DeleteWorkers     int    `yaml:"delete_workers" mapstructure:"delete_workers"`
	SourceSyncWorkers int    `yaml:"source_sync_workers" mapstructure:"source_sync_workers"`
	PrefetchCount     int    `yaml:"prefetch_count" mapstructure:"prefetch_count"`
}

type WebSearchConfig struct {
	Enabled        bool   `yaml:"enabled" mapstructure:"enabled"`
	FallbackOnly   bool   `yaml:"fallback_only" mapstructure:"fallback_only"`
	Provider       string `yaml:"provider" mapstructure:"provider"`
	Endpoint       string `yaml:"endpoint" mapstructure:"endpoint"`
	APIKey         string `yaml:"api_key" mapstructure:"api_key"`
	Model          string `yaml:"model" mapstructure:"model"`
	TimeoutSeconds int    `yaml:"timeout_seconds" mapstructure:"timeout_seconds"`
	EnableSource   bool   `yaml:"enable_source" mapstructure:"enable_source"`
	Disclaimer     string `yaml:"disclaimer" mapstructure:"disclaimer"`
}

type OAuthConfig struct {
	Notion NotionOAuthConfig `yaml:"notion" mapstructure:"notion"`
}

type NotionOAuthConfig struct {
	ClientID     string `yaml:"client_id" mapstructure:"client_id"`
	ClientSecret string `yaml:"client_secret" mapstructure:"client_secret"`
	RedirectURL  string `yaml:"redirect_url" mapstructure:"redirect_url"`
}

// Load 读取并初始化应用配置
func Load(path string) (*Config, error) {
	_ = godotenv.Load()

	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	setDefaults(v)
	bindEnv(v)

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	applyDefaults(cfg)
	expandEnv(cfg)
	if strings.TrimSpace(cfg.JWT.Secret) == "" {
		return nil, errors.New("JWT 密钥不能为空，请设置 JWT_SECRET 或 config.yaml 中的 jwt.secret")
	}
	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", 8080)
	v.SetDefault("rag.chunk_size", 1000)
	v.SetDefault("rag.chunk_overlap", 200)
	v.SetDefault("rag.top_k", 5)
	v.SetDefault("rag.score_threshold", 0.3)
	v.SetDefault("rag.max_context_tokens", 6000)
	v.SetDefault("rag.strict_mode", true)
	v.SetDefault("rag.query_rewrite_enabled", true)
	v.SetDefault("rag.query_rewrite_max_queries", 3)
	v.SetDefault("rag.query_rewrite_include_original", true)
	v.SetDefault("rag.query_rewrite_model_temperature", 0.1)
	v.SetDefault("rag.query_rewrite_hyde_enabled", false)
	v.SetDefault("rag.query_rewrite_step_back_enabled", false)
	v.SetDefault("rag.hybrid_enabled", true)
	v.SetDefault("rag.bm25_enabled", true)
	v.SetDefault("rag.bm25_top_k", 5)
	v.SetDefault("rag.rrf_k", 60)
	v.SetDefault("rerank.enabled", true)
	v.SetDefault("rerank.provider", "dashscope")
	v.SetDefault("rerank.base_url", "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank")
	v.SetDefault("rerank.model", "qwen3-vl-rerank")
	v.SetDefault("rerank.candidate_limit", 20)
	v.SetDefault("rerank.top_n", 8)
	v.SetDefault("rerank.timeout_seconds", 30)
	v.SetDefault("jwt.expires_hours", 168)
	v.SetDefault("search.bluge_dir", "storage/bluge")
	v.SetDefault("rabbitmq.enabled", true)
	v.SetDefault("rabbitmq.url", "amqp://guest:guest@localhost:5672/")
	v.SetDefault("rabbitmq.exchange", "ouragent.tasks")
	v.SetDefault("rabbitmq.index_queue", "ouragent.document.index")
	v.SetDefault("rabbitmq.delete_queue", "ouragent.document.delete.cleanup")
	v.SetDefault("rabbitmq.source_sync_queue", "ouragent.source.sync")
	v.SetDefault("rabbitmq.retry_delay_seconds", 30)
	v.SetDefault("rabbitmq.max_retries", 5)
	v.SetDefault("rabbitmq.index_workers", 2)
	v.SetDefault("rabbitmq.delete_workers", 2)
	v.SetDefault("rabbitmq.source_sync_workers", 1)
	v.SetDefault("rabbitmq.prefetch_count", 1)
	v.SetDefault("web_search.enabled", true)
	v.SetDefault("web_search.fallback_only", true)
	v.SetDefault("web_search.provider", "dashscope")
	v.SetDefault("web_search.endpoint", "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation")
	v.SetDefault("web_search.model", "qwen3.6-flash")
	v.SetDefault("web_search.timeout_seconds", 60)
	v.SetDefault("web_search.enable_source", true)
	v.SetDefault("oauth.notion.redirect_url", "http://localhost:8080/api/v1/oauth/notion/callback")
	v.SetDefault("web_search.disclaimer", "当前知识库没有找到足够信息，以下内容基于联网搜索结果生成，仅供参考。网络资料可能不准确、过期或与实际情况不一致。")
}

func bindEnv(v *viper.Viper) {
	_ = v.BindEnv("mysql.dsn", "MYSQL_DSN")
	_ = v.BindEnv("qdrant.url", "QDRANT_URL")
	_ = v.BindEnv("qdrant.collection", "QDRANT_COLLECTION")
	_ = v.BindEnv("minio.endpoint", "MINIO_ENDPOINT")
	_ = v.BindEnv("minio.access_key", "MINIO_ACCESS_KEY")
	_ = v.BindEnv("minio.secret_key", "MINIO_SECRET_KEY")
	_ = v.BindEnv("minio.bucket", "MINIO_BUCKET")
	_ = v.BindEnv("minio.use_ssl", "MINIO_USE_SSL")
	_ = v.BindEnv("llm.base_url", "LLM_BASE_URL")
	_ = v.BindEnv("llm.api_key", "OPENAI_API_KEY")
	_ = v.BindEnv("llm.chat_model", "CHAT_MODEL")
	_ = v.BindEnv("llm.embedding_model", "EMBEDDING_MODEL")
	_ = v.BindEnv("rag.query_rewrite_enabled", "QUERY_REWRITE_ENABLED")
	_ = v.BindEnv("rag.query_rewrite_max_queries", "QUERY_REWRITE_MAX_QUERIES")
	_ = v.BindEnv("rag.hybrid_enabled", "HYBRID_ENABLED")
	_ = v.BindEnv("rag.bm25_enabled", "BM25_ENABLED")
	_ = v.BindEnv("rag.bm25_top_k", "BM25_TOP_K")
	_ = v.BindEnv("rag.rrf_k", "RRF_K")
	_ = v.BindEnv("rerank.enabled", "RERANK_ENABLED")
	_ = v.BindEnv("rerank.base_url", "RERANK_BASE_URL")
	_ = v.BindEnv("rerank.api_key", "RERANK_API_KEY")
	_ = v.BindEnv("rerank.model", "RERANK_MODEL")
	_ = v.BindEnv("rerank.candidate_limit", "RERANK_CANDIDATE_LIMIT")
	_ = v.BindEnv("rerank.top_n", "RERANK_TOP_N")
	_ = v.BindEnv("rerank.timeout_seconds", "RERANK_TIMEOUT_SECONDS")
	_ = v.BindEnv("jwt.secret", "JWT_SECRET")
	_ = v.BindEnv("search.bluge_dir", "BLUGE_DIR")
	_ = v.BindEnv("rabbitmq.enabled", "RABBITMQ_ENABLED")
	_ = v.BindEnv("rabbitmq.url", "RABBITMQ_URL")
	_ = v.BindEnv("rabbitmq.exchange", "RABBITMQ_EXCHANGE")
	_ = v.BindEnv("rabbitmq.index_queue", "RABBITMQ_INDEX_QUEUE")
	_ = v.BindEnv("rabbitmq.delete_queue", "RABBITMQ_DELETE_QUEUE")
	_ = v.BindEnv("rabbitmq.source_sync_queue", "RABBITMQ_SOURCE_SYNC_QUEUE")
	_ = v.BindEnv("rabbitmq.retry_delay_seconds", "RABBITMQ_RETRY_DELAY_SECONDS")
	_ = v.BindEnv("rabbitmq.max_retries", "RABBITMQ_MAX_RETRIES")
	_ = v.BindEnv("rabbitmq.index_workers", "RABBITMQ_INDEX_WORKERS")
	_ = v.BindEnv("rabbitmq.delete_workers", "RABBITMQ_DELETE_WORKERS")
	_ = v.BindEnv("rabbitmq.source_sync_workers", "RABBITMQ_SOURCE_SYNC_WORKERS")
	_ = v.BindEnv("rabbitmq.prefetch_count", "RABBITMQ_PREFETCH_COUNT")
	_ = v.BindEnv("web_search.enabled", "WEB_SEARCH_ENABLED")
	_ = v.BindEnv("web_search.fallback_only", "WEB_SEARCH_FALLBACK_ONLY")
	_ = v.BindEnv("web_search.provider", "WEB_SEARCH_PROVIDER")
	_ = v.BindEnv("web_search.endpoint", "WEB_SEARCH_ENDPOINT")
	_ = v.BindEnv("web_search.api_key", "WEB_SEARCH_API_KEY")
	_ = v.BindEnv("web_search.model", "WEB_SEARCH_MODEL")
	_ = v.BindEnv("web_search.timeout_seconds", "WEB_SEARCH_TIMEOUT_SECONDS")
	_ = v.BindEnv("web_search.enable_source", "WEB_SEARCH_ENABLE_SOURCE")
	_ = v.BindEnv("web_search.disclaimer", "WEB_SEARCH_DISCLAIMER")
	_ = v.BindEnv("oauth.notion.client_id", "NOTION_CLIENT_ID")
	_ = v.BindEnv("oauth.notion.client_secret", "NOTION_CLIENT_SECRET")
	_ = v.BindEnv("oauth.notion.redirect_url", "NOTION_REDIRECT_URL")
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.RAG.ChunkSize == 0 {
		cfg.RAG.ChunkSize = 1000
	}
	if cfg.RAG.ChunkOverlap < 0 {
		cfg.RAG.ChunkOverlap = 0
	}
	if cfg.RAG.TopK == 0 {
		cfg.RAG.TopK = 5
	}
	if cfg.RAG.ScoreThreshold < 0 {
		cfg.RAG.ScoreThreshold = 0
	}
	if cfg.RAG.MaxContextTokens == 0 {
		cfg.RAG.MaxContextTokens = 6000
	}
	if cfg.RAG.QueryRewriteMaxQueries <= 0 {
		cfg.RAG.QueryRewriteMaxQueries = 3
	}
	if cfg.RAG.QueryRewriteMaxQueries > 5 {
		cfg.RAG.QueryRewriteMaxQueries = 5
	}
	if cfg.RAG.QueryRewriteModelTemperature < 0 {
		cfg.RAG.QueryRewriteModelTemperature = 0.1
	}
	if cfg.RAG.BM25TopK <= 0 {
		cfg.RAG.BM25TopK = 5
	}
	if cfg.RAG.RRFK <= 0 {
		cfg.RAG.RRFK = 60
	}
	if cfg.Rerank.Provider == "" {
		cfg.Rerank.Provider = "dashscope"
	}
	if cfg.Rerank.BaseURL == "" {
		cfg.Rerank.BaseURL = "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
	}
	if cfg.Rerank.Model == "" {
		cfg.Rerank.Model = "qwen3-vl-rerank"
	}
	if cfg.Rerank.CandidateLimit <= 0 {
		cfg.Rerank.CandidateLimit = 20
	}
	if cfg.Rerank.TopN <= 0 {
		cfg.Rerank.TopN = 8
	}
	if cfg.Rerank.TimeoutSeconds <= 0 {
		cfg.Rerank.TimeoutSeconds = 30
	}
	if cfg.JWT.ExpiresHours == 0 {
		cfg.JWT.ExpiresHours = 168
	}
	if cfg.Search.BlugeDir == "" {
		cfg.Search.BlugeDir = "storage/bluge"
	}
	if cfg.MinIO.Bucket == "" {
		cfg.MinIO.Bucket = "our-agent-documents"
	}
	if cfg.Rabbit.URL == "" {
		cfg.Rabbit.URL = "amqp://guest:guest@localhost:5672/"
	}
	if cfg.Rabbit.Exchange == "" {
		cfg.Rabbit.Exchange = "ouragent.tasks"
	}
	if cfg.Rabbit.IndexQueue == "" {
		cfg.Rabbit.IndexQueue = "ouragent.document.index"
	}
	if cfg.Rabbit.DeleteQueue == "" {
		cfg.Rabbit.DeleteQueue = "ouragent.document.delete.cleanup"
	}
	if cfg.Rabbit.SourceSyncQueue == "" {
		cfg.Rabbit.SourceSyncQueue = "ouragent.source.sync"
	}
	if cfg.Rabbit.RetryDelaySeconds <= 0 {
		cfg.Rabbit.RetryDelaySeconds = 30
	}
	if cfg.Rabbit.MaxRetries <= 0 {
		cfg.Rabbit.MaxRetries = 5
	}
	if cfg.Rabbit.IndexWorkers <= 0 {
		cfg.Rabbit.IndexWorkers = 2
	}
	if cfg.Rabbit.DeleteWorkers <= 0 {
		cfg.Rabbit.DeleteWorkers = 2
	}
	if cfg.Rabbit.SourceSyncWorkers <= 0 {
		cfg.Rabbit.SourceSyncWorkers = 1
	}
	if cfg.Rabbit.PrefetchCount <= 0 {
		cfg.Rabbit.PrefetchCount = 1
	}
	if cfg.Web.Provider == "" {
		cfg.Web.Provider = "dashscope"
	}
	if cfg.Web.Endpoint == "" {
		cfg.Web.Endpoint = "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	}
	if cfg.Web.APIKey == "" {
		cfg.Web.APIKey = cfg.LLM.APIKey
	}
	if cfg.Web.Model == "" {
		cfg.Web.Model = "qwen3.6-flash"
	}
	if cfg.Web.TimeoutSeconds <= 0 {
		cfg.Web.TimeoutSeconds = 60
	}
	if cfg.Web.Disclaimer == "" {
		cfg.Web.Disclaimer = "当前知识库没有找到足够信息，以下内容基于联网搜索结果生成，仅供参考。网络资料可能不准确、过期或与实际情况不一致。"
	}
	if cfg.OAuth.Notion.RedirectURL == "" {
		cfg.OAuth.Notion.RedirectURL = "http://localhost:8080/api/v1/oauth/notion/callback"
	}
}

func expandEnv(cfg *Config) {
	cfg.MySQL.DSN = os.ExpandEnv(cfg.MySQL.DSN)
	cfg.Qdrant.URL = os.ExpandEnv(cfg.Qdrant.URL)
	cfg.Qdrant.Collection = os.ExpandEnv(cfg.Qdrant.Collection)
	cfg.MinIO.Endpoint = os.ExpandEnv(cfg.MinIO.Endpoint)
	cfg.MinIO.AccessKey = os.ExpandEnv(cfg.MinIO.AccessKey)
	cfg.MinIO.SecretKey = os.ExpandEnv(cfg.MinIO.SecretKey)
	cfg.MinIO.Bucket = os.ExpandEnv(cfg.MinIO.Bucket)
	cfg.LLM.BaseURL = os.ExpandEnv(cfg.LLM.BaseURL)
	cfg.LLM.APIKey = os.ExpandEnv(cfg.LLM.APIKey)
	cfg.LLM.ChatModel = os.ExpandEnv(cfg.LLM.ChatModel)
	cfg.LLM.EmbeddingModel = os.ExpandEnv(cfg.LLM.EmbeddingModel)
	cfg.Rerank.BaseURL = os.ExpandEnv(cfg.Rerank.BaseURL)
	cfg.Rerank.APIKey = os.ExpandEnv(cfg.Rerank.APIKey)
	cfg.Rerank.Model = os.ExpandEnv(cfg.Rerank.Model)
	if cfg.Rerank.APIKey == "" {
		cfg.Rerank.APIKey = cfg.LLM.APIKey
	}
	cfg.JWT.Secret = os.ExpandEnv(cfg.JWT.Secret)
	cfg.Search.BlugeDir = os.ExpandEnv(cfg.Search.BlugeDir)
	cfg.Rabbit.URL = os.ExpandEnv(cfg.Rabbit.URL)
	cfg.Rabbit.Exchange = os.ExpandEnv(cfg.Rabbit.Exchange)
	cfg.Rabbit.IndexQueue = os.ExpandEnv(cfg.Rabbit.IndexQueue)
	cfg.Rabbit.DeleteQueue = os.ExpandEnv(cfg.Rabbit.DeleteQueue)
	cfg.Rabbit.SourceSyncQueue = os.ExpandEnv(cfg.Rabbit.SourceSyncQueue)
	cfg.Web.Endpoint = os.ExpandEnv(cfg.Web.Endpoint)
	cfg.Web.APIKey = os.ExpandEnv(cfg.Web.APIKey)
	cfg.Web.Model = os.ExpandEnv(cfg.Web.Model)
	cfg.Web.Disclaimer = os.ExpandEnv(cfg.Web.Disclaimer)
	cfg.OAuth.Notion.ClientID = os.ExpandEnv(cfg.OAuth.Notion.ClientID)
	cfg.OAuth.Notion.ClientSecret = os.ExpandEnv(cfg.OAuth.Notion.ClientSecret)
	cfg.OAuth.Notion.RedirectURL = os.ExpandEnv(cfg.OAuth.Notion.RedirectURL)
}
