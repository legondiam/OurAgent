package config

import (
	"errors"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	Server  ServerConfig  `yaml:"server" mapstructure:"server"`
	MySQL   MySQLConfig   `yaml:"mysql" mapstructure:"mysql"`
	Qdrant  QdrantConfig  `yaml:"qdrant" mapstructure:"qdrant"`
	LLM     LLMConfig     `yaml:"llm" mapstructure:"llm"`
	RAG     RAGConfig     `yaml:"rag" mapstructure:"rag"`
	JWT     JWTConfig     `yaml:"jwt" mapstructure:"jwt"`
	Storage StorageConfig `yaml:"storage" mapstructure:"storage"`
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

type LLMConfig struct {
	BaseURL        string `yaml:"base_url" mapstructure:"base_url"`
	APIKey         string `yaml:"api_key" mapstructure:"api_key"`
	ChatModel      string `yaml:"chat_model" mapstructure:"chat_model"`
	EmbeddingModel string `yaml:"embedding_model" mapstructure:"embedding_model"`
}

type RAGConfig struct {
	ChunkSize        int     `yaml:"chunk_size" mapstructure:"chunk_size"`
	ChunkOverlap     int     `yaml:"chunk_overlap" mapstructure:"chunk_overlap"`
	TopK             int     `yaml:"top_k" mapstructure:"top_k"`
	ScoreThreshold   float64 `yaml:"score_threshold" mapstructure:"score_threshold"`
	MaxContextTokens int     `yaml:"max_context_tokens" mapstructure:"max_context_tokens"`
	StrictMode       bool    `yaml:"strict_mode" mapstructure:"strict_mode"`
}

type JWTConfig struct {
	Secret       string `yaml:"secret" mapstructure:"secret"`
	ExpiresHours int    `yaml:"expires_hours" mapstructure:"expires_hours"`
}

type StorageConfig struct {
	DocumentDir string `yaml:"document_dir" mapstructure:"document_dir"`
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
	v.SetDefault("jwt.expires_hours", 168)
	v.SetDefault("storage.document_dir", "storage/documents")
}

func bindEnv(v *viper.Viper) {
	_ = v.BindEnv("mysql.dsn", "MYSQL_DSN")
	_ = v.BindEnv("qdrant.url", "QDRANT_URL")
	_ = v.BindEnv("qdrant.collection", "QDRANT_COLLECTION")
	_ = v.BindEnv("llm.base_url", "LLM_BASE_URL")
	_ = v.BindEnv("llm.api_key", "OPENAI_API_KEY")
	_ = v.BindEnv("llm.chat_model", "CHAT_MODEL")
	_ = v.BindEnv("llm.embedding_model", "EMBEDDING_MODEL")
	_ = v.BindEnv("jwt.secret", "JWT_SECRET")
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
	if cfg.JWT.ExpiresHours == 0 {
		cfg.JWT.ExpiresHours = 168
	}
	if cfg.Storage.DocumentDir == "" {
		cfg.Storage.DocumentDir = "storage/documents"
	}
}

func expandEnv(cfg *Config) {
	cfg.MySQL.DSN = os.ExpandEnv(cfg.MySQL.DSN)
	cfg.Qdrant.URL = os.ExpandEnv(cfg.Qdrant.URL)
	cfg.Qdrant.Collection = os.ExpandEnv(cfg.Qdrant.Collection)
	cfg.LLM.BaseURL = os.ExpandEnv(cfg.LLM.BaseURL)
	cfg.LLM.APIKey = os.ExpandEnv(cfg.LLM.APIKey)
	cfg.LLM.ChatModel = os.ExpandEnv(cfg.LLM.ChatModel)
	cfg.LLM.EmbeddingModel = os.ExpandEnv(cfg.LLM.EmbeddingModel)
	cfg.JWT.Secret = os.ExpandEnv(cfg.JWT.Secret)
	cfg.Storage.DocumentDir = os.ExpandEnv(cfg.Storage.DocumentDir)
}
