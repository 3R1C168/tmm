package server

import (
	"context"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/iyear/tdl/core/logctx"
)

// initLogger builds a zap logger that writes both to stderr and to a rotating
// log file under <exe>/logs/server.log, and stores it in the returned context
// so every module can use logctx.From(ctx). It is a no-op-friendly helper: the
// file sink is best-effort.
func (s *Server) initLogger(base *zap.Logger) {
	exe, err := os.Executable()
	if err == nil {
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		if mkErr := os.MkdirAll(logDir, 0o755); mkErr == nil {
			logPath := filepath.Join(logDir, "server.log")
			if f, openErr := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); openErr == nil {
				fileCore := zapcore.NewCore(
					zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
					zapcore.AddSync(f),
					zapcore.InfoLevel,
				)
				base = zap.New(zapcore.NewTee(base.Core(), fileCore))
			}
		}
	}
	s.logger = base
}

// log returns the server logger (never nil).
func (s *Server) log() *zap.Logger {
	if s.logger == nil {
		return zap.NewNop()
	}
	return s.logger
}

// ctxWithLog returns a context carrying the server logger for logctx.
func (s *Server) ctxWithLog(ctx context.Context) context.Context {
	return logctx.With(ctx, s.log())
}
