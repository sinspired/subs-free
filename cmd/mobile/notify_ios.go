//go:build ios

package main

import (
	"encoding/json"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// sendOSNotification iOS 原生通知实现
func sendOSNotification(title, body string) {
	// 构建 Wails 3 iOS 期望的 JSON 格式
	payload := map[string]string{
		"title": title,
		"body":  body,
	}
	
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("iOS 通知 JSON 构建失败", "error", err)
		return
	}

	// 调用 Wails3 专属的 iOS API
	application.IOS.PostNotification(string(jsonBytes))
	slog.Debug("已发送 iOS 原生通知", "title", title)
}