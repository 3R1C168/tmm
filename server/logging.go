package server

import (
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/iyear/tdl/pkg/consts"
)

// initLogger builds a zap logger with two sinks, both at info level:
//  1. stderr — human-readable console output, visible in the foreground cmd
//     window (start.bat runs the service in the foreground so logs stream live).
//  2. <exe>/logs/server-YYYY-MM-DD-HHMM.log — JSON for later analysis.
//
// 每次启动新开一个日志文件（文件名带启动时间），并在开头记一条版本信息，
// 方便回溯「某次部署后发生了什么」。旧文件保留。
//
// server.go passes logctx.From(context.Background()) whose default is a no-op
// logger; if base is that no-op we replace it with a real stderr console core
// so logs are actually emitted instead of being swallowed.
func (s *Server) initLogger(base *zap.Logger) {
	// console (stderr) core: readable, info level. Replaces a Nop base so the
	// foreground window shows what the service is doing.
	consoleCfg := zap.NewDevelopmentEncoderConfig()
	consoleCfg.EncodeTime = zapcore.ISO8601TimeEncoder        // 2026-08-01T...
	consoleCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder // INFO(绿)/WARN(黄)/ERROR(红)
	cores := []zapcore.Core{
		zapcore.NewCore(
			zapcore.NewConsoleEncoder(consoleCfg),
			zapcore.AddSync(os.Stderr),
			zapcore.InfoLevel,
		),
	}

	// file core: JSON under <exe>/logs/server-YYYY-MM-DD-HHMM.log, info level.
	// 按启动时间切文件：每次启动新开一个，文件名即「何时启动的」。
	exe, err := os.Executable()
	if err == nil {
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		if mkErr := os.MkdirAll(logDir, 0o755); mkErr == nil {
			stamp := time.Now().Format("2006-01-02-1504")
			logPath := filepath.Join(logDir, "server-"+stamp+".log")
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
	// otherwise start purely from our own console+file cores.
	var combined zapcore.Core
	if base != nil && base.Core().Enabled(zapcore.InfoLevel) {
		// base may itself be a Nop (Enabled returns true for Nop), so prefer to
		// always include our own cores and tee base in for safety.
		combined = zapcore.NewTee(append([]zapcore.Core{base.Core()}, cores...)...)
	} else {
		combined = zapcore.NewTee(cores...)
	}
	s.logger = zap.New(combined)

	// 启动横幅：把版本/commit/构建时间记成第一条日志，每个日志文件开头都能看到
	s.logger.Info("server started",
		zap.String("version", consts.Version),
		zap.String("commit", consts.Commit),
		zap.String("buildDate", consts.CommitDate))
}

// log returns the server logger (never nil).
func (s *Server) log() *zap.Logger {
	if s.logger == nil {
		return zap.NewNop()
	}
	return s.logger
}
