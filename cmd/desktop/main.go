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
	// 仅加载配置、创建结构体，不启动后端，规避 updater helper 实例
	// 提前拉起 Node 子进程后直接退出留下幽灵进程的问题。
	coreApp, guiApp := setupApp()
	globalGuiApp = guiApp

	// TODO:处理右键菜单
	wailsApp := application.New(application.Options{
		Name:        "Subs Free",
		Description: "订阅检测桌面管理面板",
		Services: []application.Service{
			application.NewService(guiApp),
			application.NewService(notifier),
			application.NewService(&Notifier{}),
		},
		Assets: application.AssetOptions{
			// configPath 仅供 handler 定位静态资源目录，此处仍传字符串保持兼容。
			// admin.html 读取配置路径的正确方式是 EnterWebUI 写入的 URL 查询参数，
			// 而非此处注入的快照值（切换配置后快照不再更新）。
			// 若 newCombinedAssetHandler 内部也注入了 configPath，需将其第一个参数
			// 类型改为 func() string 并传入 guiApp.GetConfigPath，才能彻底修复。
			Handler: newCombinedAssetHandler(guiApp.GetConfigPath, guiApp.GetListenPort),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		Windows: application.WindowsOptions{
			// 关键配置：允许销毁全部窗口后继续保持托盘和 Go 后端在后台运行
			DisableQuitOnLastWindowClosed: true,
			UseVisualHosting:              true,
		},

		// 第二次启动时：唤醒第一实例（若窗口已销毁则重新拉起默认登录窗口）
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
			Window:         updater.WindowNone, // 不弹窗
		}); err != nil {
			slog.Warn("Updater: Init 失败", "error", err)
		} else {
			slog.Debug("Updater: 已初始化", "currentVersion", currentVer)
		}
	}
	guiApp.updaterApp = wailsApp
	guiApp.autostart = wailsApp.Autostart

	// 启动时仅创建登录小窗；webUIWin 改为进入管理界面时按需创建，关闭/最小化即销毁释放内存
	guiApp.createLoginWindow()

	// 退出生命周期清理
	wailsApp.OnShutdown(func() {
		slog.Info("GUI 程序正在退出，执行清理工作…")
		// 使用 guiApp.backend 而非初始的 coreApp：
		// SwitchConfigFile 调用后 guiApp.backend 已指向新内核，
		// 若此处继续使用 coreApp 则会对已关闭的旧实例执行二次 Shutdown。
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

		// 启动时检查更新
		go guiupdater.CheckUpdateStatus(wailsApp)
	})

	if err := wailsApp.Run(); err != nil {
		slog.Error("Wails 运行失败", "error", err)
		os.Exit(1)
	}
}