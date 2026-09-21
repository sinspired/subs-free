package main

import (
	"os"
	mihomoLog "github.com/metacubex/mihomo/log"
)

func init() {
	// 依赖库日志静默
	if os.Getenv("MIHOMO_DEBUG") != "" {
		mihomoLog.SetLevel(mihomoLog.DEBUG)
	} else {
		mihomoLog.SetLevel(mihomoLog.SILENT)
	}
}