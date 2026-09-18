// Package main: tray.go
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	guiupdater "github.com/sinspired/subs-free/cmd/desktop/updater"
	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/sinspired/subs-check-pro/v3/app"
	"github.com/sinspired/subs-check-pro/v3/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// trayIcon 嵌入托盘彩色图标（Windows/Linux）。
//
//go:embed frontend/public/logo.png
var trayIcon []byte

//go:embed frontend/public/logo_32x32.png
var logo32 []byte

// trayTemplateIcon 嵌入黑白模板图标，供 macOS 菜单栏使用。
// 要求：纯黑色笔画 + 完全透明背景，18x18px（同时提供 @2x 36x36）。
// macOS 会自动在深色模式下将黑色反转为白色。
//
//go:embed frontend/public/logo_template.png
var trayTemplateIcon []byte

// windowVisible 跟踪当前窗口可见状态。
var windowVisible atomic.Bool

func init() {
	windowVisible.Store(true)
}

// 退出状态机
var (
	gracefulQuitPending atomic.Bool // 首次"结束检测后退出"已触发
	gracefulQuitOnce    sync.Once   // 保证后台等待 goroutine 只启动一次
)

// startSysTray 初始化 Wails v3 原生系统托盘。
func startSysTray(
	wailsApp *application.App,
	guiApp *GuiApp,
	coreApp *app.App,
	onQuit func(),
) {
	tray := wailsApp.SystemTray.New()

	// 设置图标：
	// - SetIcon 供 Windows/Linux 使用（彩色 PNG）
	// - SetTemplateIcon 供 macOS 菜单栏使用（黑白模板，自动适配深浅色）
	// Wails v3 会根据平台自动选择使用哪个
	tray.SetIcon(trayIcon)
	tray.SetTemplateIcon(trayTemplateIcon) // ← 修复：传入变量，而非参数声明

	// Support for template icons on macOS
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplateIcon)
	} else {
		// Support for light/dark mode icons
		// tray.SetDarkModeIcon(trayIconDark)
		tray.SetIcon(trayIcon)
	}

	tray.SetTooltip(formatSysTrayTooltip(coreApp, guiApp))

	updateTooltip := func() {
		tray.SetTooltip(formatSysTrayTooltip(coreApp, guiApp))
	}

	menu, checkUpdateItem := buildTrayMenu(wailsApp, guiApp, coreApp, onQuit, updateTooltip)
	tray.SetMenu(menu)

	wailsApp.Event.On(updater.EventUpdateAvailable, func(e *application.CustomEvent) {
		application.InvokeAsync(func() {
			rel, _ := e.Data.(*updater.Release)
			version := ""

			if rel != nil {
				version = rel.Version
			}
			if version == "" {
				version = guiupdater.GetUpdateStatus().Version
			}

			// 1. 更新自定义状态并派发事件
			nextSt := guiupdater.UpdateStatus{Available: true, Version: version}
			guiupdater.SetUpdateStatus(nextSt)
			wailsApp.Event.Emit(guiupdater.EventStatusChanged, nextSt)

			// 2. 更新托盘菜单文本
			if version != "" {
				checkUpdateItem.SetLabel("有新版本 v" + version)
				// 3. 发送系统通知提示用户
				sendOSNotification("Subs Check Pro 更新", "发现新版本 v"+version+"，请及时更新")
			} else {
				checkUpdateItem.SetLabel("有新版本")
				sendOSNotification("Subs Check Pro 更新", "发现新版本，请及时更新")
			}
		})
	})

	wailsApp.Event.On(updater.EventNoUpdate, func(_ *application.CustomEvent) {
		application.InvokeAsync(func() {
			checkUpdateItem.SetLabel("检查更新")

			// 确保状态恢复
			nextSt := guiupdater.UpdateStatus{Available: false, Version: ""}
			guiupdater.SetUpdateStatus(nextSt)
			wailsApp.Event.Emit(guiupdater.EventStatusChanged, nextSt)
		})
	})

	wailsApp.Event.On(updater.EventUpdateReady, func(e *application.CustomEvent) {
		application.InvokeAsync(func() {
			rel, _ := e.Data.(*updater.Release)
			version := ""
			if rel != nil {
				version = rel.Version
			}
			if version == "" {
				version = guiupdater.GetUpdateStatus().Version
			}

			if version != "" {
				checkUpdateItem.SetLabel("更新就绪 v" + version + "（点击安装）")
			} else {
				checkUpdateItem.SetLabel("更新就绪（点击安装）")
			}
		})
	})
	tray.OnClick(func() {
		if windowVisible.Load() {
			guiApp.hideActiveWindow()
		} else {
			guiApp.showActiveWindow()
		}
	})

	tray.OnDoubleClick(func() {
		guiApp.showActiveWindow()
	})

	tray.OnRightClick(func() {
		tray.OpenMenu()
	})

	slog.Debug("系统托盘初始化完成（Wails v3 原生）")
}

func buildTrayMenu(
	wailsApp *application.App,
	guiApp *GuiApp,
	coreApp *app.App,
	onQuit func(),
	updateTooltip func(),
) (*application.Menu, *application.MenuItem) {
	menu := wailsApp.NewMenu()

	menu.Add("Subs Check Pro 桌面端").SetBitmap(logo32).SetEnabled(false)

	statusItem := menu.Add("...")
	statusItem.SetEnabled(false)

	menu.AddSeparator()

	menu.Add("显示主界面").OnClick(func(_ *application.Context) {
		guiApp.showActiveWindow()
	})

	menu.Add("隐藏主界面").OnClick(func(_ *application.Context) {
		guiApp.hideActiveWindow()
		sendOSNotification("Subs Check Pro", "已最小化到系统托盘\n单击图标可恢复窗口")
	})

	menu.AddSeparator()

	menu.Add("返回登录窗口").OnClick(func(_ *application.Context) {
		guiApp.BackToLogin()
		sendOSNotification("Subs Check Pro", "已返回登录窗口")
	})

	menu.AddSeparator()

	triggerCheckMenu := menu.Add("开始检测")
	triggerCheckMenu.OnClick(func(_ *application.Context) {
		coreApp.TriggerCheck()
		sendOSNotification("Subs Check Pro", "已触发检测任务\n可在系统托盘、管理界面查看检测进度")
		triggerCheckMenu.SetEnabled(false)
	})

	stopCheckMenu := menu.Add("停止检测")
	stopCheckMenu.OnClick(func(_ *application.Context) {
		if err := callBackendForceClose(); err != nil {
			slog.Warn("检测任务停止失败", "error", err)
		} else {
			sendOSNotification("Subs Check Pro", "已发送停止检测信号\n正在等待结果收集完成")
			slog.Debug("托盘：已发送停止检测信号")
		}
	})

	menu.AddSeparator()

	stopCheckAndExitMenu := menu.Add("结束检测并退出")
	stopCheckAndExitMenu.OnClick(func(_ *application.Context) {
		if gracefulQuitPending.CompareAndSwap(false, true) {
			sendOSNotification("Subs Check Pro", "正在等待检测完成后退出\n再次点击将立即强制退出")
			slog.Debug("托盘：已发送停止检测信号，等待检测完成后退出")

			gracefulQuitOnce.Do(func() {
				go func() {
					if err := callBackendForceClose(); err != nil {
						slog.Warn("发送 force-close 失败", "error", err)
					}

					waitForBackendIdle(5 * time.Minute)

					slog.Info("后端检测已完成，开始优雅退出")
					sendOSNotification("Subs Check Pro", "检测已完成，正在退出…")
					onQuit()
				}()
			})
		} else {
			slog.Warn("GUI：用户二次确认，立即退出")
			sendOSNotification("Subs Check Pro", "正在强制退出…")
			onQuit()
		}
	})

	menu.AddSeparator()

	// ── 开机自启（Checkbox 菜单项）────────────────────────────────────────
	autostartItem := menu.Add("开机自启")
	autostartItem.SetChecked(false)

	guiApp.autostartMenuItem = autostartItem

	autostartItem.OnClick(func(_ *application.Context) {
		enabled, err := guiApp.autostart.IsEnabled()
		if err != nil {
			slog.Warn("读取开机自启状态失败", "error", err)
			sendOSNotification("Subs Check Pro", "读取开机自启状态失败")
			return
		}

		next := !enabled
		autostartItem.SetChecked(next)

		if next {
			err = guiApp.autostart.Enable()
			sendOSNotification("Subs Check Pro", "已设置开机自启")
		} else {
			err = guiApp.autostart.Disable()
			sendOSNotification("Subs Check Pro", "已取消开机自启")
		}
		if guiApp.loginWin != nil {
			guiApp.loginWin.EmitEvent("autostart:changed", next)
		}
		if err != nil {
			slog.Warn("设置开机自启失败", "error", err)
			sendOSNotification("Subs Check Pro", "设置开机自启失败："+err.Error())
		}
	})

	menu.AddSeparator()

	checkUpdateItem := menu.Add("检查更新")
	checkUpdateItem.OnClick(func(_ *application.Context) {
		guiApp.CheckForUpdates()
	})

	menu.Add("关于").OnClick(func(_ *application.Context) {
		guiApp.OpenAboutWindow()
	})

	menu.AddSeparator()

	menu.Add("退出").OnClick(func(_ *application.Context) {
		slog.Info("GUI：强制退出")
		sendOSNotification("Subs Check Pro", "正在关闭…")
		onQuit()
	})

	// 初始化 Notch Window (macOS 灵动岛通知)
	notchWin := wailsApp.Window.NewNotchWindow(application.NotchWindowOptions{
		Width:    260,
		Height:   46, // 保持细长，贴合物理摄像头模组
		Animated: true,
		WindowOptions: application.WebviewWindowOptions{
			Name:           "notch_progress",
			URL:            "/notch.html",
			BackgroundType: application.BackgroundTypeTransparent,
		},
	})

	var isNotchVisible atomic.Bool

	// 启动后台协程，定时更新托盘状态
	go func() {
		enabled, err := guiApp.GetAutoStartEnabled()
		if err != nil {
			slog.Warn("初始化开机自启 checkbox 失败", "error", err)
			return
		}
		autostartItem.SetChecked(enabled)

		ticker := time.NewTicker(1000 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			if updateTooltip != nil {
				updateTooltip()
			}

			if !guiApp.IsBackendReady() {
				statusItem.SetLabel("后端未启动")
				continue
			}

			// 状态：正在检测
			if coreApp.IsChecking() {
				// 1. 如果开始检测且 Notch 未显示，则滑动滑出
				if !isNotchVisible.Load() {
					isNotchVisible.Store(true) // 立即锁定状态，防止重复调用
					application.InvokeAsync(func() {
						notchWin.Show()
					})
				}
				stopCheckMenu.SetEnabled(true)
				stopCheckAndExitMenu.SetEnabled(true)
				statusItem.SetHidden(false)
				statusItem.SetLabel(renderProgressString(coreApp))
				triggerCheckMenu.SetEnabled(false)
				continue
			}

			// 2. 状态：检测空闲 (刚结束检测)
			if isNotchVisible.Load() {
				// 立即将可见性标记为 false，防止在接下来的 4 秒内重复触发计时器
				isNotchVisible.Store(false)

				// 延迟 4 秒收起，让用户看清最终的检测结果（如：可用 15 个）
				time.AfterFunc(4*time.Second, func() {
					// 4秒后，双重确认用户没有在这4秒内再次点击“开始检测”
					if !coreApp.IsChecking() {
						application.InvokeAsync(func() {
							notchWin.Hide()
						})
					}
				})
			}

			// 更新托盘文案
			lastResult := coreApp.GetLastCheckResult()
			if lastResult != "" {
				statusItem.SetHidden(false)
				statusItem.SetLabel(lastResult)
			} else {
				statusItem.SetHidden(true)
			}

			triggerCheckMenu.SetEnabled(true)
			stopCheckMenu.SetEnabled(false)
			stopCheckAndExitMenu.SetEnabled(false)
		}
	}()

	return menu, checkUpdateItem
}

// 后端 HTTP API 辅助函数

func backendBase() string {
	return "http://127.0.0.1:" + defaultListenPort()
}

func callBackendForceClose() error {
	url := backendBase() + "/api/force-close"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", config.GlobalConfig.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func isBackendChecking() bool {
	url := backendBase() + "/api/status"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return true
	}
	req.Header.Set("X-API-Key", config.GlobalConfig.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return true
	}

	var status struct {
		Checking bool `json:"checking"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return true
	}
	return status.Checking
}

func waitForBackendIdle(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isBackendChecking() {
			return
		}
		time.Sleep(2 * time.Second)
	}
	slog.Warn("等待后端空闲超时，继续退出流程")
}

// ── 窗口显示/隐藏辅助 ─────────────────────────────────────────────────────────

func showWindow(win *application.WebviewWindow) {
	win.Show()
	win.Focus()
	windowVisible.Store(true)
	slog.Debug("窗口已显示")
}

func hideWindow(win *application.WebviewWindow) {
	win.Hide()
	windowVisible.Store(false)
	slog.Debug("窗口已隐藏到托盘")
}

// NotifyHideToTray 在窗口最小化到托盘时调用，更新可见状态。
func NotifyHideToTray() {
	windowVisible.Store(false)
	sendOSNotification("Subs Check Pro", "已最小化到系统托盘\n单击托盘图标可恢复窗口")
	slog.Debug("已最小化到系统托盘，单击托盘图标可恢复窗口")
}

// renderProgressString 在托盘显示进度
func renderProgressString(coreApp *app.App) string {
	state := coreApp.GetCurrentState()
	stepName := state.StepName
	if stepName == "" {
		stepName = "进度"
	}

	var percent *float64
	// 计算百分比
	if state.ProxyCount > 0 {
		val := float64(state.Progress) / float64(state.ProxyCount) * 100.0
		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}
		percent = &val
	} else if stepName == "保存中" {
		val := 100.0
		percent = &val
	}

	// 特殊情况：代理或结果处理时不显示进度
	if strings.Contains(stepName, "代理") || state.ProcessResults {
		percent = nil
	}

	// 输出
	if percent == nil {
		return fmt.Sprintf("%s... %s", stepName, state.ETASuffix)
	}
	return fmt.Sprintf("%s %.1f%% %s", stepName, *percent, state.ETASuffix)
}

// formatSysTrayTooltip 格式化托盘提示
func formatSysTrayTooltip(coreApp *app.App, guiApp *GuiApp) string {
	base := "Subs Check Pro GUI" + " - 端口 " + strings.TrimPrefix(config.GlobalConfig.ListenPort, ":")

	st := guiupdater.GetUpdateStatus()
	updateLine := ""
	if st.Available {
		if st.Version != "" {
			updateLine = "\n有新版本 v" + st.Version
		} else {
			updateLine = "\n有新版本"
		}
	}

	if !guiApp.IsBackendReady() {
		return base + "\n后端未启动" + updateLine
	}

	if coreApp.IsChecking() {
		return base + "\n" + renderProgressString(coreApp) + updateLine
	}

	lastResult := coreApp.GetLastCheckResult()
	if lastResult != "" {
		return base + "\n" + lastResult + "\n空闲 √" + updateLine
	}
	return base + "\n空闲 √" + updateLine
}
