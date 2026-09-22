//go:build android

package main

import (
	"encoding/json"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// sendOSNotification Android 原生通知实现
func sendOSNotification(title, body string) {
	// 构建 Wails 3 Android 期望的 JSON 格式
	payload := map[string]string{
		"title": title,
		"body":  body,
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Android 通知 JSON 构建失败", "error", err)
		return
	}

	// 调用 Wails3 专属的 Android API
	application.Android.Notify(string(jsonBytes))
	application.Mobile.Haptic("notification")
	slog.Debug("已发送 Android 原生通知", "title", title)
}