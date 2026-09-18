package main

import (
	"embed"
	"log/slog"
	"os"
	"strings"
	"time"

	guiupdater "github.com/sinspired/subs-free/cmd/desktop/updater"
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
		Name:        "Subs Check Pro",
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

		// 第二次启动时：唤醒第一实例
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:      "com.sinspired.subs_free",
			EncryptionKey: singleInstanceKey,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				slog.Debug("收到第二实例唤醒", "args", data.Args)
				application.InvokeAsync(func() {
					guiApp.showActiveWindow()
				})
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

	// 登录窗，加载 wails3 前端资产
	loginWin := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          "login",
		Title:         "Subs Check Pro",
		Width:         560,
		Height:        400,
		MinWidth:      540,
		MaxWidth:      600,
		MinHeight:     380,
		MaxHeight:     420,
		DisableResize: false,
		Frameless:     false,
		AlwaysOnTop:   false,
		URL:           "/",
		KeyBindings: map[string]func(window application.Window){
			"F1": func(window application.Window) {
				guiApp.OpenAboutWindow()
			},
		},
		Mac: macWindowOpts(30),
		Windows: application.WindowsWindow{
			DisableIcon:             false,
			HiddenOnTaskbar:         false,
			EnableSwipeGestures:     false,
			GeneralAutofillEnabled:  true,
			PasswordAutosaveEnabled: true,
		},
		BackgroundType: application.BackgroundTypeTranslucent,
	})

	// WebUI 大窗（加载 admin 页面，初始隐藏）
	webUIWin := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          "webui",
		Title:         "Subs Check Pro",
		Width:         1100,
		Height:        700,
		MinWidth:      700,
		MinHeight:     600,
		Hidden:        true,
		DisableResize: false,
		Frameless:     false,
		URL:           "about:blank",
		KeyBindings: map[string]func(window application.Window){
			"F1": func(window application.Window) {
				guiApp.OpenAboutWindow()
			},
		},
		Mac: macWindowOpts(40),
		Windows: application.WindowsWindow{
			DisableIcon:             false,
			HiddenOnTaskbar:         false,
			EnableSwipeGestures:     true,
			GeneralAutofillEnabled:  true,
			PasswordAutosaveEnabled: true,
		},
		BackgroundType: application.BackgroundTypeTranslucent,
	})

	// 注入窗口引用
	guiApp.loginWin = loginWin
	guiApp.webUIWin = webUIWin
	guiApp.autostart = wailsApp.Autostart

	// 窗口关闭/最小化拦截
	loginWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		loginWin.EmitEvent("window:close-requested", nil)
	})

	webUIWin.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		hideWindow(webUIWin)
		e.Cancel()
		sendOSNotification(
			"Subs Check Pro 后台运行",
			"右键点击托盘图标，选择「立即退出」可关闭程序",
		)
	})

	webUIWin.OnWindowEvent(events.Common.WindowMinimise, func(e *application.WindowEvent) {
		webUIWin.Hide()
		windowVisible.Store(false)
		sendOSNotification("Subs Check PRO", "管理界面已最小化到系统托盘")
	})

	loginWin.OnWindowEvent(events.Common.WindowMinimise, func(e *application.WindowEvent) {
		loginWin.Hide()
		windowVisible.Store(false)
		sendOSNotification("Subs Check PRO GUI", "登录窗口已最小化到系统托盘")
	})

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
