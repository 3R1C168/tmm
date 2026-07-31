package server

import "go.uber.org/zap"

func testLogger() *zap.Logger {
	return zap.NewNop()
}
