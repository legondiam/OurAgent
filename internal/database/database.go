package database

import (
	"OurAgent/internal/model"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Connect 连接 MySQL 数据库
func Connect(dsn string) (*gorm.DB, error) {
	return gorm.Open(mysql.Open(dsn), &gorm.Config{})
}

// AutoMigrate 自动迁移数据库表结构
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.User{},
		&model.KnowledgeBase{},
		&model.KnowledgeSource{},
		&model.ExternalDocument{},
		&model.Document{},
		&model.DocumentParentChunk{},
		&model.DocumentChildChunk{},
		&model.Conversation{},
		&model.ChatLog{},
		&model.ChatFeedback{},
		&model.LongTermMemory{},
		&model.LongTermMemoryVersion{},
		&model.LongTermMemoryEvidence{},
		&model.LongTermMemoryJob{},
		&model.LongTermMemoryForgetTombstone{},
		&model.MemoryConsolidationSignal{},
	)
}
