package source

import (
	"os"
	"testing"

	"OurAgent/pkg/logger"

	"go.uber.org/zap"
)

func TestMain(m *testing.M) {
	logger.Logger = zap.NewNop()
	os.Exit(m.Run())
}
