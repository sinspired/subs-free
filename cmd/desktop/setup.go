// setup.go
package main

import (
	"errors"
	"log/slog"
	"os"

	"github.com/sinspired/subs-check-pro/v3/app"
	"github.com/sinspired/subs-check-pro/v3/utils"
)

var (
	// GuiVersion 桌面客户端自身版本，由构建脚本通过 -ldflags 注入。
	//   go build -ldflags "-X main.GuiVersion=$(git describe --tags --abbrev=0) -X main.Version=v1.0.0 -X main.CurrentCommit=7c23868"
	GuiVersion    = "dev"
	Version       = "dev"
	CurrentCommit = "unknown"
)

// setupApp 仅完成轻量级初始化：设置环境变量、加载配置、创建 GuiApp 结构体。
// 不启动后端，规避 updater helper 实例提前拉起 Node 子进程后直接退出留下幽灵进程。
// 后端初始化由 startBackend() 在 Wails ApplicationStarted 事件后完成。
func setupApp() (*app.App, *GuiApp) {
	os.Setenv("START_FROM_GUI", "1")

	coreApp := app.New(Version, Version+CurrentCommit, "")
	guiApp := &GuiApp{backend: coreApp}

	// 仅加载配置，不初始化后端
	if err := coreApp.InitConfigLoad(); err != nil {
		if errors.Is(err, app.ErrFirstRun) {
			guiApp.isFirstRun = true
		} else {
			slog.Error("配置加载失败", "error", err)
			os.Exit(1)
		}
	}

	// InitConfigLoad() 完成后路径已确定，提前写入供登录窗口首帧渲染使用。
	guiApp.configPath = coreApp.GetConfigPath()

	// 端口冲突检测
	httpOK, subOK := coreApp.CheckPortConflict()
	if !httpOK || !subOK {
		guiApp.pendingInit = true
	}

	return coreApp, guiApp
}

// startBackend 在 Wails 框架就绪（ApplicationStarted）后调用，完成后端完整初始化。
// 端口冲突时设置 pendingInit=true 并返回 false，等待用户通过 UI 解决后由 CompleteInit 续接。
func startBackend(coreApp *app.App) bool {
	if err := coreApp.Initialize(); err != nil {
		slog.Error("应用初始化失败，无法启动 GUI", "error", err)
		os.Exit(1)
	}

	registerGuiRoutes(coreApp.GetRouter())

	// 注入系统通知回调：后端检测完成 → Wails3 托盘通知
	utils.OSNotifyHook = func(title, body string) {
		sendOSNotification(title, body)
	}

	go coreApp.Run()
	return true
}
