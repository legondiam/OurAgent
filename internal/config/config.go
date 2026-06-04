package config

import (
	"errors"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	Server ServerConfig   `yaml:"server" mapstructure:"server"`
	MySQL  MySQLConfig    `yaml:"mysql" mapstructure:"mysql"`
	Qdrant QdrantConfig   `yaml:"qdrant" mapstructure:"qdrant"`
	MinIO  MinIOConfig    `yaml:"minio" mapstructure:"minio"`
	LLM    LLMConfig      `yaml:"llm" mapstructure:"llm"`
	RAG    RAGConfig      `yaml:"rag" mapstructure:"rag"`
	Rerank RerankConfig   `yaml:"rerank" mapstructure:"rerank"`
	JWT    JWTConfig      `yaml:"jwt" mapstructure:"jwt"`
	Search SearchConfig   `yaml:"search" mapstructure:"search"`
	Rabbit RabbitMQConfig `yaml:"rabbitmq" mapstructure:"rabbitmq"`
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
	RetryDelaySeconds int    `yaml:"retry_delay_seconds" mapstructure:"retry_delay_seconds"`
	MaxRetries        int    `yaml:"max_retries" mapstructure:"max_retries"`
	IndexWorkers      int    `yaml:"index_workers" mapstructure:"index_workers"`
	DeleteWorkers     int    `yaml:"delete_workers" mapstructure:"delete_workers"`
	PrefetchCount     int    `yaml:"prefetch_count" mapstructure:"prefetch_count"`
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
	v.SetDefault("rabbitmq.retry_delay_seconds", 30)
	v.SetDefault("rabbitmq.max_retries", 5)
	v.SetDefault("rabbitmq.index_workers", 2)
	v.SetDefault("rabbitmq.delete_workers", 2)
	v.SetDefault("rabbitmq.prefetch_count", 1)
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
	_ = v.BindEnv("rabbitmq.retry_delay_seconds", "RABBITMQ_RETRY_DELAY_SECONDS")
	_ = v.BindEnv("rabbitmq.max_retries", "RABBITMQ_MAX_RETRIES")
	_ = v.BindEnv("rabbitmq.index_workers", "RABBITMQ_INDEX_WORKERS")
	_ = v.BindEnv("rabbitmq.delete_workers", "RABBITMQ_DELETE_WORKERS")
	_ = v.BindEnv("rabbitmq.prefetch_count", "RABBITMQ_PREFETCH_COUNT")
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
	if cfg.Rabbit.PrefetchCount <= 0 {
		cfg.Rabbit.PrefetchCount = 1
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
}
