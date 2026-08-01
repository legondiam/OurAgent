package tasks

import (
	"OurAgent/pkg/logger"

	"go.uber.org/zap"
)

func init() {
	logger.Logger = zap.NewNop()
}
