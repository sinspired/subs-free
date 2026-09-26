// cmd\mobile\main.go
package main

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	coreapp "github.com/sinspired/subs-check-pro/v3/app"
	"github.com/sinspired/subs-check-pro/v3/utils"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
	guiupdater "github.com/sinspired/subs-free/updater"
)

var (
	// GuiVersion 桌面客户端自身版本，由构建脚本通过 -ldflags 注入。
	// go build -ldflags "-X main.GuiVersion=$(git describe --tags --abbrev=0) -X main.Version=v1.0.0 -X main.CurrentCommit=7c23868"
	GuiVersion    = "dev"
	Version       = "dev"
	CurrentCommit = "unknown"
)

func main() {
	// 获取真实的 Android 沙盒路径，防止退化到 /data/local/tmp
	workDir := utils.GetPrivateStorageDir()

	_ = os.Setenv("HOME", workDir)
	_ = os.Chdir(workDir)
	_ = os.Setenv("START_FROM_GUI", "1")

	// tmpDir := filepath.Join(workDir, "tmp")
	// _ = os.MkdirAll(tmpDir, 0755)
	// _ = os.Setenv("TMPDIR", tmpDir)

	// 环境彻底就绪后，初始化日志文件
	fileHandler, err := coreapp.InitLoggerFile()

	if err != nil || fileHandler == nil {
		slog.Info("日志初始化失败", "error", err)
	}

	// 启动内核...
	core := coreapp.New(Version, Version+CurrentCommit, "")

	isFirstRun := false
	if err := core.InitConfigLoad(); err != nil {
		if errors.Is(err, coreapp.ErrFirstRun) ||
			strings.Contains(err.Error(), "首次运行") {
			isFirstRun = true
		} else {
			slog.Error("配置加载失败", "error", err)
		}
	}

	guiApp := &GuiApp{
		backend:    core,
		configPath: core.GetConfigPath(),
		isFirstRun: isFirstRun,
	}

	globalGuiApp = guiApp

	// assetsDir 由 embed.go（frontend/dist）/ embed_lite.go（frontend/dist-lite）
	// 按 -tags lite 决定，不能在这里写死 "frontend/dist"：
	// lite 版本的 embed.FS 里根本不存在 frontend/dist 这个子目录，
	// fs.Sub 会返回 error，导致应用一启动就 os.Exit(1) —— 这就是
	// LITE=true 真机运行失败的根本原因。
	frontendFS, err := fs.Sub(assets, assetsDir)
	if err != nil {
		slog.Error("前端资源目录挂载失败", "error", err)
		os.Exit(1)
	}

	app := application.New(application.Options{
		Name:        "Subs Free",
		Description: "基于 Subs Check Pro v3 内核的高性能网络节点检测和管理引擎客户端",
		Services: []application.Service{
			application.NewService(guiApp),
		},
		Assets: application.AssetOptions{
			Handler: buildAssetHandler(frontendFS, core.GetConfigPath),
		},
	})

	// 将实例化后的 app 赋值给全局变量，否则事件没法发出！
	globalApp = app

	// 始化无 UI 版 Wails 更新器 ---
	currentVer := strings.TrimPrefix(GuiVersion, "v")
	if currentVer == "" || currentVer == "dev" {
		currentVer = "0.0.0"
	}

	// 针对安卓端定制：直接过滤并选取 .apk 安装包
	ghProvider, ghErr := github.New(github.Config{
		Repository:    "sinspired/subs-free",
		ChecksumAsset: "SHA256SUMS",
		HTTPClient:    guiupdater.NewHTTPClient(), // 复用防污染/指纹伪装网络请求
		AssetMatcher: func(req updater.CheckRequest, assets []github.ReleaseAsset) int {
			// 移动端专用匹配器：强行抓取 .apk
			for i, a := range assets {
				if strings.HasSuffix(strings.ToLower(a.Name), ".apk") {
					return i
				}
			}
			return -1
		},
	})

	if ghErr != nil {
		slog.Warn("Mobile Updater: 初始化 GitHub provider 失败", "error", ghErr)
	} else {
		// 移动端由于没有自动重启更新闭环，仅保留检查能力即可
		_ = app.Updater.Init(updater.Config{
			CurrentVersion: currentVer,
			Providers:      []updater.Provider{ghProvider},
			CheckInterval:  0,                  // 禁用后台定时轮询，交给前端通过 GetUpdateInfo 主动控制
			Window:         updater.WindowNone, // 绝对不能弹窗
		})
		slog.Debug("Mobile Updater: 已初始化", "currentVersion", currentVer)
	}
	// 将注入更新器的 app 挂载到 guiApp 以供调用
	guiApp.updaterApp = app

	guiApp.mainWindow = app.Window.NewWithOptions(
		application.WebviewWindowOptions{
			Name:  "main",
			Title: "Subs Free",
			URL:   "/",
		},
	)

	app.OnShutdown(func() {
		_ = core.Shutdown()
	})

	go func() {
		if err := core.Initialize(); err != nil {
			slog.Error("内核初始化失败", "err", err)
			guiApp.initErr = err.Error()
			guiApp.backendReady = true
			return
		}

		registerGuiRoutes(core.GetRouter())
		guiApp.backendReady = true

		// 注入系统通知回调：跨平台分发
		utils.OSNotifyHook = func(title, body string) {
			sendOSNotification(title, body)
		}

		core.Run()
	}()

	if err := app.Run(); err != nil {
		slog.Error("应用运行失败", "error", err)
		os.Exit(1)
	}
}
