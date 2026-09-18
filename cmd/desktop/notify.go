// Package main: notify.go
package main

import (
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

type Notifier struct{}

var (
	appNotifier *notifications.NotificationService
	notifyOnce  sync.Once
)

func (n *Notifier) SendOSNotification(title, message string) {
	sendOSNotification(title, message)
}

// InitNotifier 在 main.go 中初始化一次。
func InitNotifier(n *notifications.NotificationService) {
	notifyOnce.Do(func() {
		appNotifier = n
	})
}

// sendOSNotification 发送一条系统通知（异步，失败静默）。
func sendOSNotification(title, message string) {
	if appNotifier == nil {
		slog.Debug("通知服务未初始化")
		return
	}

	go func() {
		authorized, err := appNotifier.CheckNotificationAuthorization()
		if err != nil {
			slog.Debug("检查通知授权失败", "error", err)
			return
		}

		if !authorized {
			authorized, err = appNotifier.RequestNotificationAuthorization()
			if err != nil {
				slog.Debug("请求通知授权失败", "error", err)
				return
			}
			if !authorized {
				slog.Debug("通知授权未通过")
				return
			}
		}

		if err := appNotifier.SendNotification(notifications.NotificationOptions{
			ID:    "subs-check-pro",
			Title: title,
			Body:  message,
		}); err != nil {
			slog.Debug("系统通知发送失败", "error", err)
		}
	}()
}
