// init.go
package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/lmittmann/tint"
	mihomoLog "github.com/metacubex/mihomo/log"
	"github.com/sinspired/subs-check-pro/v3/app"
	"gopkg.in/natefinch/lumberjack.v2"
)

func init() {
	// 依赖库日志静默
	if os.Getenv("MIHOMO_DEBUG") != "" {
		mihomoLog.SetLevel(mihomoLog.DEBUG)
	} else {
		mihomoLog.SetLevel(mihomoLog.SILENT)
	}

	logLevel := getLogLevelWails()


	logPath, err := app.GetLogPath()
	if err != nil {
		slog.Error("无法获取日志存储路径", "error", err)
	}

	// 配置日志文件
	fileLogger := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    10,
		MaxBackups: 3,
		MaxAge:     7,
	}
	
	fileHandler := tint.NewTextHandler(fileLogger, &tint.Options{
		Level:      logLevel,
		TimeFormat: "01-02 15:04:05",
		NoColor:    true,
	})
	slog.SetDefault(slog.New(fileHandler))
}

func getLogLevelWails() slog.Level {
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
