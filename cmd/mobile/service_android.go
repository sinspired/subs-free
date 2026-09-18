//go:build android

package main

import (
	"encoding/json"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// StartForegroundService 启动安卓前台服务并挂一条常驻通知，让检测任务在应用
// 被切到后台之后依然能跑完，不会被系统限流甚至杀掉。
func (g *GuiApp) StartForegroundService(title, text string) {
    payload, err := json.Marshal(map[string]string{
        "title": title,
        "text":  text, // ⚠️ 改成 text，和 Java 对齐
    })
    if err != nil {
        slog.Error("前台服务通知 JSON 构建失败", "error", err)
        return
    }
    application.Android.StartForegroundService(string(payload))
    slog.Debug("已启动前台服务", "title", title, "text", text)
}

// StopForegroundService 检测结束时关闭前台服务和常驻通知。
func (g *GuiApp) StopForegroundService() {
	application.Android.StopForegroundService()
	slog.Debug("已停止前台服务")
}
