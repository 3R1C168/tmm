package server

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// initLogger builds a zap logger with two sinks, both at info level:
//  1. stderr — human-readable console output, visible in the foreground cmd
//     window (start.bat runs the service in the foreground so logs stream live).
//  2. <exe>/logs/server.log — JSON for later analysis.
//
// server.go passes logctx.From(context.Background()) whose default is a no-op
// logger; if base is that no-op we replace it with a real stderr console core
// so logs are actually emitted instead of being swallowed.
func (s *Server) initLogger(base *zap.Logger) {
	// console (stderr) core: readable, info level. Replaces a Nop base so the
	// foreground window shows what the service is doing.
	consoleCfg := zap.NewDevelopmentEncoderConfig()
	consoleCfg.EncodeTime = zapcore.ISO8601TimeEncoder   // 2026-08-01T...
	consoleCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder // INFO(绿)/WARN(黄)/ERROR(红)
	cores := []zapcore.Core{
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(consoleCfg),
			zapcore.AddSync(os.Stderr),
			zapcore.InfoLevel,
		),
	}

	// file core: JSON under <exe>/logs/server.log, info level.
	exe, err := os.Executable()
	if err == nil {
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		if mkErr := os.MkdirAll(logDir, 0o755); mkErr == nil {
			logPath := filepath.Join(logDir, "server.log")
			if f, openErr := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); openErr == nil {
				cores = append(cores, zapcore.NewCore(
					zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()),
					zapcore.AddSync(f),
					zapcore.InfoLevel,
				))
			}
		}
	}

	// If base carries a real caller-aware core (not the Nop default), keep it;
	// otherwise start purely from our console+file cores.
	var combined zapcore.Core
	if base != nil && base.Core().Enabled(zapcore.InfoLevel) {
		// base may itself be a Nop (Enabled returns true for Nop), so prefer to
		// always include our own cores and tee base in for safety.
		combined = zapcore.NewTee(append([]zapcore.Core{base.Core()}, cores...)...)
	} else {
		combined = zapcore.NewTee(cores...)
	}
	s.logger = zap.New(combined)
}

// log returns the server logger (never nil).
func (s *Server) log() *zap.Logger {
	if s.logger == nil {
		return zap.NewNop()
	}
	return s.logger
}
