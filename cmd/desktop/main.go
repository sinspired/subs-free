// cmd\desktop\main.go
package main

import (
	"embed"
	"log/slog"
	"os"
	"strings"
	"time"

	guiupdater "github.com/sinspired/subs-free/updater"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

//go:embed all:frontend/dist
var assets embed.FS

// singleInstanceKey 单实例 IPC 通道加密密钥，保证同一应用不同版本之间的实例可互相通信
var singleInstanceKey = [32]byte{
	0x73, 0x75, 0x62, 0x73, 0x2d, 0x63, 0x68, 0x65,
	0x63, 0x6b, 0x2d, 0x70, 0x72, 0x6f, 0x2d, 0x67,
	0x75, 0x69, 0x2d, 0x73, 0x69, 0x6e, 0x67, 0x6c,
	0x65, 0x2d, 0x69, 0x6e, 0x73, 0x74, 0x61, 0x6e,
}

func main() {
	notifier := notifications.New()
	InitNotifier(notifier)

	// 轻量级初始化（Wails 就绪前）
	coreApp, guiApp := setupApp()
	globalGuiApp = guiApp

	wailsApp := application.New(application.Options{
		Name:        "Subs Free",
		Description: "订阅检测桌面管理面板",
		Services: []application.Service{
			application.NewService(guiApp),
			application.NewService(notifier),
			application.NewService(&Notifier{}),
		},
		Assets: application.AssetOptions{
			Handler: newCombinedAssetHandler(guiApp.GetConfigPath, guiApp.GetListenPort),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			// 关键配置：允许销毁所有窗口（例如收进托盘时销毁登录窗）而不退出应用
			DisableQuitOnLastWindowClosed: true,
			UseVisualHosting:              true,
		},

		// 第二次启动时：唤醒第一实例
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:      "com.sinspired.subs_free",
			EncryptionKey: singleInstanceKey,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				slog.Debug("收到第二实例唤醒", "args", data.Args)
				guiApp.showActiveWindow()
			},
		},
	})

	// Wails Updater：检查 GitHub Release 更新
	currentVer := strings.TrimPrefix(GuiVersion, "v")
	if currentVer == "" || currentVer == "dev" {
		currentVer = "0.0.0"
	}

	ghProvider, ghErr := github.New(github.Config{
		Repository:    "sinspired/subs-free",
		ChecksumAsset: "SHA256SUMS",
		HTTPClient:    guiupdater.NewHTTPClient(),
		AssetMatcher:  guiupdater.AssetMatcher,
	})

	if ghErr != nil {
		slog.Warn("Updater: 初始化 GitHub provider 失败", "error", ghErr)
	} else {
		if err := wailsApp.Updater.Init(updater.Config{
			CurrentVersion: currentVer,
			Providers:      []updater.Provider{ghProvider},
			CheckInterval:  6 * time.Hour,
			Window:         updater.WindowNone,
		}); err != nil {
			slog.Warn("Updater: Init 失败", "error", err)
		} else {
			slog.Debug("Updater: 已初始化", "currentVersion", currentVer)
		}
	}
	guiApp.updaterApp = wailsApp
	guiApp.autostart = wailsApp.Autostart

	// 解析 --hidden 启动参数（由 Wails AutostartOptions.Arguments 注入）
	startHidden := false
	for _, arg := range os.Args[1:] {
		if arg == "--hidden" {
			startHidden = true
			break
		}
	}

	// 仅在「静默自启 且 无端口冲突」时隐藏启动；一旦端口冲突，必须弹窗让用户处理
	if startHidden && !guiApp.pendingInit {
		windowVisible.Store(false)
		slog.Info("检测到 --hidden 参数，静默启动到系统托盘")
	} else {
		guiApp.createLoginWindow()
		if startHidden && guiApp.pendingInit {
			slog.Warn("检测到 --hidden 启动但存在端口冲突，已自动显示窗口")
		}
	}

	// 启动时只创建登录窗口；WebUI 窗口在点击「进入管理界面」时再按需创建
	guiApp.createLoginWindow()

	// 退出生命周期清理
	wailsApp.OnShutdown(func() {
		slog.Info("GUI 程序正在退出，执行清理工作…")
		if guiApp.IsBackendReady() {
			if err := guiApp.backend.Shutdown(); err != nil {
				slog.Error("关闭应用失败", "error", err)
			}
		}
		slog.Info("GUI 程序已退出")
		sendOSNotification("Subs Check PRO", "GUI 程序已关闭")
	})

	onQuit := func() { wailsApp.Quit() }
	startSysTray(wailsApp, guiApp, coreApp, onQuit)

	// Wails 就绪后才启动后端
	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		appInitOK := startBackend(coreApp)
		slog.Debug("Wails 就绪，后端初始化完成", "appReady", appInitOK)

		if startHidden && guiApp.pendingInit {
			sendOSNotification("Subs Free 端口冲突", "检测到端口被占用，已为您打开端口设置窗口")
		}

		go guiupdater.CheckUpdateStatus(wailsApp)
	})

	if err := wailsApp.Run(); err != nil {
		slog.Error("Wails 运行失败", "error", err)
		os.Exit(1)
	}
}
