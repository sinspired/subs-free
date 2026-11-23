// cmd\desktop\app.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	coreapp "github.com/sinspired/subs-check-pro/v3/app"
	"github.com/sinspired/subs-check-pro/v3/config"
	"github.com/sinspired/subs-check-pro/v3/utils"
	"github.com/sinspired/subs-free/updater"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wupdater "github.com/wailsapp/wails/v3/pkg/updater"
	"gopkg.in/yaml.v3"
)

// globalGuiApp 包级指针，供 router handler（如 handleGuiBackToLogin）访问。
var globalGuiApp *GuiApp

// GuiApp Wails 绑定结构体。
type GuiApp struct {
	configPath string
	backend    *coreapp.App

	// loginWin 登录小窗（按需创建；进入 WebUI / 最小化 / 收进托盘时销毁）。
	loginWin *application.WebviewWindow
	// webUIWin WebUI 大窗（进入管理界面时按需创建；点击 × 关闭销毁并回登录窗，最小化则隐藏进托盘保留状态）。
	webUIWin *application.WebviewWindow

	// loginClosing 为 true 时放行登录窗直接销毁（不弹出退出确认框）。
	loginClosing atomic.Bool
	// webUISilentClose 为 true 时表示代码主动关闭 WebUI（如 BackToLogin / HideToTray），不重复触发再打开登录窗。
	webUISilentClose atomic.Bool

	// autostartMenuItem 托盘菜单中"开机自启"菜单项的引用。
	autostartMenuItem *application.MenuItem

	// pendingInit 为 true 时表示端口预检发现冲突，Initialize() 尚未调用。
	pendingInit bool

	// isFirstRun 标记本次启动是否为首次运行（创建了默认配置）
	isFirstRun  bool
	inWebUI     atomic.Bool
	aboutWin    *application.WebviewWindow
	subLinksWin *application.WebviewWindow
	subStoreWin *application.WebviewWindow
	filesWin    *application.WebviewWindow
	analysisWin *application.WebviewWindow

	// updateWin 自定义更新窗口（懒加载，复用）。
	updateWin       *application.WebviewWindow
	updateWinUnsubs []func()

	// autostart Wails 跨平台开机自启管理器
	autostart *application.AutostartManager

	// updaterApp 持有 wails App 引用，供 CheckForUpdates 调用 Updater。
	updaterApp *application.App

	// checkingUpdate 防止用户连续快速点击"检查更新"图标
	checkingUpdate atomic.Bool

	// onTraySync 由 tray.go 注入，用于在窗口状态变更时立即刷新托盘菜单语义
	onTraySync func()
}

// AppInfo 前端展示所需的应用运行信息。
type AppInfo struct {
	APIKey               string   `json:"apiKey"`
	ListenPort           string   `json:"listenPort"`
	SubStorePort         string   `json:"subStorePort"`
	SubStorePath         string   `json:"subStorePath"`
	KeyIsRandom          bool     `json:"keyIsRandom"`
	IsFirstRun           bool     `json:"isFirstRun"`
	ConfigPath           string   `json:"configPath"`
	PortConflictHTTP     bool     `json:"portConflictHTTP"`
	PortConflictSubStore bool     `json:"portConflictSubStore"`
	PendingInit          bool     `json:"pendingInit"`
	AutostartEnabled     bool     `json:"autostartEnabled"`
	GuiVersion           string   `json:"guiVersion"`
	CoreVersion          string   `json:"coreVersion"`
	OriginVersion        string   `json:"originVersion"`
	LocalIPs             []string `json:"localIPs"`
}

// getLocalIPv4s 枚举本机所有活动网络接口上的 IPv4 地址。
func getLocalIPv4s() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			ips = append(ips, ip4.String())
		}
	}
	return ips
}

// createLoginWindow 在主线程创建或聚焦登录小窗。
func (g *GuiApp) createLoginWindow() {
	wailsApp := application.Get()
	if wailsApp == nil {
		wailsApp = g.updaterApp
	}
	if wailsApp == nil {
		return
	}

	if g.loginWin != nil {
		if g.loginWin.IsMinimised() {
			g.loginWin.UnMinimise()
		}
		g.loginWin.Show()
		g.loginWin.Focus()
		windowVisible.Store(true)
		if g.onTraySync != nil {
			g.onTraySync()
		}
		return
	}

	win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          "login",
		Title:         "Subs Free",
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
				g.OpenAboutWindow()
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

	g.loginWin = win
	windowVisible.Store(true)

	// 点击登录窗口 ×：拦截并弹出 QuitDialog，由用户选择「退出」或「后台运行」
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if g.loginClosing.CompareAndSwap(true, false) {
			g.loginWin = nil
			if g.webUIWin == nil || !g.webUIWin.IsVisible() {
				windowVisible.Store(false)
			}
			if g.onTraySync != nil {
				g.onTraySync()
			}
			return
		}
		e.Cancel()
		win.EmitEvent("window:close-requested", nil)
	})

	// 保留登录窗口原生最小化到任务栏，仅同步托盘菜单文案
	win.OnWindowEvent(events.Common.WindowMinimise, func(e *application.WindowEvent) {
		windowVisible.Store(false)
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
	win.OnWindowEvent(events.Common.WindowUnMinimise, func(e *application.WindowEvent) {
		windowVisible.Store(true)
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
	win.OnWindowEvent(events.Common.WindowRestore, func(e *application.WindowEvent) {
		windowVisible.Store(true)
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
	win.OnWindowEvent(events.Common.WindowShow, func(e *application.WindowEvent) {
		windowVisible.Store(true)
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
	win.OnWindowEvent(events.Common.WindowHide, func(e *application.WindowEvent) {
		windowVisible.Store(false)
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})

	if g.onTraySync != nil {
		g.onTraySync()
	}
}

// openLoginWindow 从任意 goroutine 安全唤起登录窗口。
func (g *GuiApp) openLoginWindow() {
	application.InvokeAsync(g.createLoginWindow)
}

// closeLoginWindow 主动销毁登录窗口（不触发 QuitDialog）。
func (g *GuiApp) closeLoginWindow() {
	application.InvokeAsync(func() {
		if g.loginWin == nil {
			return
		}
		g.loginClosing.Store(true)
		g.loginWin.Close()
	})
}

// NotifyAutostartChanged 安全同步开机自启状态到登录窗。
func (g *GuiApp) NotifyAutostartChanged(enabled bool) {
	application.InvokeAsync(func() {
		if g.loginWin != nil {
			g.loginWin.EmitEvent("autostart:changed", enabled)
		}
	})
}

// OpenBrandURL 在 Wails 无地址栏窗口中打开品牌 / 社交链接。
func (g *GuiApp) OpenBrandURL(url string, windowSize string) {
	if url == "" {
		return
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return
	}
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}

	capturedURL := url
	application.InvokeAsync(func() {
		opts := newPopupOptions("Subs Free", "/loading.html#"+capturedURL, windowSize)
		popup := wailsApp.Window.NewWithOptions(opts)
		popup.Show()
		popup.Center()
		popup.Focus()

		time.AfterFunc(300*time.Millisecond, func() {
			application.InvokeAsync(func() {
				popup.SetURL(capturedURL)
			})
		})
	})
}

// EnterWebUI 由前端调用：按需创建并进入 WebUI 大窗，同时销毁登录小窗。
func (g *GuiApp) EnterWebUI() {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}

	adminURL := "/webui/admin.html?configPath=" + url.QueryEscape(g.configPath)

	application.InvokeAsync(func() {
		g.inWebUI.Store(true)

		if g.webUIWin != nil {
			if g.webUIWin.IsMinimised() {
				g.webUIWin.UnMinimise()
			}
			g.webUIWin.SetURL(adminURL)
			g.webUIWin.Show()
			g.webUIWin.Center()
			g.webUIWin.Focus()
			windowVisible.Store(true)
			g.webUIWin.EmitEvent("webui:visibility", true)
		} else {
			win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
				Name:          "webui",
				Title:         "Subs Free",
				Width:         1100,
				Height:        700,
				MinWidth:      700,
				MinHeight:     600,
				DisableResize: false,
				Frameless:     false,
				URL:           adminURL,
				KeyBindings: map[string]func(window application.Window){
					"F1": func(window application.Window) {
						g.OpenAboutWindow()
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

			g.webUIWin = win
			windowVisible.Store(true)

			// 1. WebUI 点击关闭 ×：直接销毁 WebUI 窗口释放 WebView2 进程，并回到登录窗口
			win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
				g.webUIWin = nil
				g.inWebUI.Store(false)
				if g.loginWin == nil {
					windowVisible.Store(false)
				}
				if g.onTraySync != nil {
					g.onTraySync()
				}
				if g.webUISilentClose.CompareAndSwap(true, false) {
					return
				}
				g.openLoginWindow()
			})

			// 2. WebUI 点击最小化 _：隐藏收进托盘并暂停前端轮询/动画，保留原窗口状态
			win.OnWindowEvent(events.Common.WindowMinimise, func(e *application.WindowEvent) {
				hideWindow(win)
				sendOSNotification("Subs Check PRO", "管理界面已最小化到系统托盘，点击托盘图标可恢复")
				if g.onTraySync != nil {
					g.onTraySync()
				}
			})

			// 同步隐藏/显示状态给前端与托盘，确保最小化到托盘期间零后台轮询
			win.OnWindowEvent(events.Common.WindowHide, func(e *application.WindowEvent) {
				windowVisible.Store(false)
				win.EmitEvent("webui:visibility", false)
				if g.onTraySync != nil {
					g.onTraySync()
				}
			})
			win.OnWindowEvent(events.Common.WindowShow, func(e *application.WindowEvent) {
				windowVisible.Store(true)
				win.EmitEvent("webui:visibility", true)
				if g.onTraySync != nil {
					g.onTraySync()
				}
			})
			win.OnWindowEvent(events.Common.WindowUnMinimise, func(e *application.WindowEvent) {
				windowVisible.Store(true)
				win.EmitEvent("webui:visibility", true)
				if g.onTraySync != nil {
					g.onTraySync()
				}
			})
			win.OnWindowEvent(events.Common.WindowRestore, func(e *application.WindowEvent) {
				windowVisible.Store(true)
				win.EmitEvent("webui:visibility", true)
				if g.onTraySync != nil {
					g.onTraySync()
				}
			})

			win.Show()
			win.Center()
			win.Focus()
		}

		// 进入管理界面后销毁登录小窗，避免额外常驻一个 WebView2 进程
		if g.loginWin != nil {
			g.loginClosing.Store(true)
			g.loginWin.Close()
		}

		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
}

// GetAPIKey 返回当前配置的 API Key，供本地 WebUI 页面通过 Wails binding 调用。
func (g *GuiApp) GetAPIKey() string {
	return config.GlobalConfig.APIKey
}

// defaultListenPort 返回当前配置的 HTTP 监听端口号（不含冒号前缀）。
func defaultListenPort() string {
	port := strings.TrimPrefix(config.GlobalConfig.ListenPort, ":")
	if port == "" {
		return "8199"
	}
	return port
}

// GetListenPort 返回 Gin HTTP 服务监听的端口号（不含冒号）。
func (g *GuiApp) GetListenPort() string {
	return defaultListenPort()
}

// GetConfigPath 返回当前生效配置文件的绝对路径。
func (g *GuiApp) GetConfigPath() string {
	return g.configPath
}

// BackToLogin 从 WebUI 返回登录窗口（销毁 WebUI 进程并打开登录窗口）
func (g *GuiApp) BackToLogin() {
	g.inWebUI.Store(false)
	g.openLoginWindow()
	application.InvokeAsync(func() {
		if g.webUIWin != nil {
			g.webUISilentClose.Store(true)
			g.webUIWin.Close()
		}
	})
}

// GetAppInfo 返回应用运行信息（含端口冲突检测）。
func (g *GuiApp) GetAppInfo() AppInfo {
	port := defaultListenPort()
	subStorePort := strings.TrimPrefix(config.GlobalConfig.SubStorePort, ":")

	var conflictHTTP, conflictSubStore bool
	if g.pendingInit && g.backend != nil {
		httpAvail, subAvail := g.backend.CheckPortConflict()
		conflictHTTP, conflictSubStore = !httpAvail, !subAvail
	}

	autostartEnabled, _ := g.autostart.IsEnabled()

	coreVer := Version
	if CurrentCommit != "" && CurrentCommit != "unknown" {
		coreVer = Version + "-" + CurrentCommit
	}

	subStorePath := strings.TrimPrefix(
		strings.TrimSpace(config.GlobalConfig.SubStorePath), "/",
	)

	return AppInfo{
		APIKey:               config.GlobalConfig.APIKey,
		ListenPort:           port,
		SubStorePort:         subStorePort,
		SubStorePath:         subStorePath,
		KeyIsRandom:          os.Getenv("GUI_KEY_IS_RANDOM") == "1",
		IsFirstRun:           g.isFirstRun,
		ConfigPath:           g.configPath,
		PortConflictHTTP:     conflictHTTP,
		PortConflictSubStore: conflictSubStore,
		PendingInit:          g.pendingInit,
		AutostartEnabled:     autostartEnabled,
		GuiVersion:           GuiVersion,
		CoreVersion:          coreVer,
		OriginVersion:        Version,
		LocalIPs:             getLocalIPv4s(),
	}
}

// IsBackendReady 动态查询后端是否已成功初始化并正在运行。
func (g *GuiApp) IsBackendReady() bool {
	return !g.pendingInit && g.backend != nil
}

// CompleteInit 在用户修正端口冲突后，由前端调用，完成后端初始化。
func (g *GuiApp) CompleteInit() error {
	if !g.pendingInit {
		return nil
	}
	if g.backend == nil {
		return fmt.Errorf("内部错误：backend 未设置")
	}

	if err := g.backend.Initialize(); err != nil {
		return fmt.Errorf("初始化后端失败: %w", err)
	}

	registerGuiRoutes(g.backend.GetRouter())

	utils.OSNotifyHook = func(title, body string) {
		sendOSNotification(title, body)
	}

	go g.backend.Run()

	g.configPath = g.backend.GetConfigPath()
	g.pendingInit = false

	sendOSNotification("Subs Check PRO", "内核服务已成功启动")
	return nil
}

// GetEnterNonce 生成一次性 nonce，用于 /gui/enter 安全跳转。
func (g *GuiApp) GetEnterNonce(remember bool) string {
	return generateNonce(config.GlobalConfig.APIKey, remember)
}

// ValidateConfigKey 验证密钥，通过后返回一次性 nonce。
func (g *GuiApp) ValidateConfigKey(enteredKey string, remember bool) (string, error) {
	actual := config.GlobalConfig.APIKey
	if actual == "" {
		return "", fmt.Errorf("配置文件未设置 api-key")
	}
	if strings.TrimSpace(enteredKey) != actual {
		return "", fmt.Errorf("密钥错误")
	}
	return generateNonce(actual, remember), nil
}

// peekConfigAPIKey 读取并解析指定配置文件中的 api-key。
func peekConfigAPIKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取配置文件失败: %w", err)
	}
	var target struct {
		APIKey string `yaml:"api-key"`
	}
	if err := yaml.Unmarshal(data, &target); err != nil {
		return "", fmt.Errorf("解析配置文件失败: %w", err)
	}
	if strings.TrimSpace(target.APIKey) == "" {
		return "", fmt.Errorf("配置文件未设置 api-key")
	}
	return target.APIKey, nil
}

// SwitchConfigFile 切换到用户重新选择的配置文件。
func (g *GuiApp) SwitchConfigFile(path, enteredKey string) (AppInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return AppInfo{}, fmt.Errorf("配置文件路径为空")
	}

	actual, err := peekConfigAPIKey(path)
	if err != nil {
		return AppInfo{}, err
	}
	if strings.TrimSpace(enteredKey) != actual {
		return AppInfo{}, fmt.Errorf("密钥错误")
	}

	oldHTTPPort := normalizePort(config.GlobalConfig.ListenPort)

	if g.backend != nil {
		if err := g.backend.Shutdown(); err != nil {
			slog.Warn("SwitchConfigFile：关闭旧内核时发生非致命错误", "error", err)
		}
		g.backend = nil
	}

	if oldHTTPPort != "" {
		waitForPortRelease(oldHTTPPort, 3*time.Second)
	}

	newBackend := coreapp.New(Version, Version+CurrentCommit, path)
	if err := newBackend.Initialize(); err != nil {
		g.pendingInit = true
		return AppInfo{}, fmt.Errorf("初始化新配置失败: %w", err)
	}

	registerGuiRoutes(newBackend.GetRouter())

	utils.OSNotifyHook = func(title, body string) {
		sendOSNotification(title, body)
	}

	go newBackend.Run()

	g.backend = newBackend
	g.configPath = newBackend.GetConfigPath()
	g.isFirstRun = false
	g.pendingInit = false

	sendOSNotification("Subs Check PRO", "内核已切换到新配置并重新启动")
	return g.GetAppInfo(), nil
}

// waitForPortRelease 轮询直到指定端口可用（或超时）。
func waitForPortRelease(port string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isPortInUse(port) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	slog.Warn("SwitchConfigFile：等待端口释放超时，继续尝试初始化新内核", "port", port)
}

// ValidatePort 实时验证单个端口号合法性。
func (g *GuiApp) ValidatePort(port string) string {
	p := normalizePort(port)
	if err := validatePort(p); err != nil {
		return err.Error()
	}
	if isPortInUse(p) {
		return "已占用"
	}
	return ""
}

// SetPorts 更新端口配置。
func (g *GuiApp) SetPorts(httpPort, subStorePort string) error {
	return g.backend.SetPorts(httpPort, subStorePort)
}

// ShowWindow 供前端主动调用，显示并聚焦登录窗口。
func (g *GuiApp) ShowWindow() {
	g.openLoginWindow()
}

// TrayWindowState 描述当前窗口与界面所处真实状态，供托盘菜单和单击切换使用。
type TrayWindowState struct {
	Mode      string // "background" | "login" | "webui"
	IsVisible bool   // 窗口当前是否可见且未最小化
}

// GetTrayWindowState 必须在主线程调用，返回真实窗口状态。
func (g *GuiApp) GetTrayWindowState() TrayWindowState {
	if g.inWebUI.Load() && g.webUIWin != nil {
		vis := g.webUIWin.IsVisible() && !g.webUIWin.IsMinimised()
		return TrayWindowState{Mode: "webui", IsVisible: vis}
	}
	if g.loginWin != nil {
		vis := g.loginWin.IsVisible() && !g.loginWin.IsMinimised()
		return TrayWindowState{Mode: "login", IsVisible: vis}
	}
	return TrayWindowState{Mode: "background", IsVisible: false}
}

// ToggleFromTray 托盘图标/主菜单项单击切换逻辑：根据真实窗口状态精确切换。
func (g *GuiApp) ToggleFromTray() {
	application.InvokeAsync(func() {
		st := g.GetTrayWindowState()
		slog.Debug("托盘单击触发", "mode", st.Mode, "isVisible", st.IsVisible)

		switch st.Mode {
		case "webui":
			if st.IsVisible {
				hideWindow(g.webUIWin)
			} else {
				showWindow(g.webUIWin)
			}
		case "login":
			if st.IsVisible {
				g.loginWin.Minimise()
			} else {
				if g.loginWin.IsMinimised() {
					g.loginWin.UnMinimise()
				}
				g.loginWin.Show()
				g.loginWin.Focus()
				windowVisible.Store(true)
			}
		default: // "background"
			g.inWebUI.Store(false)
			g.createLoginWindow()
		}

		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
}

// HideToTray 关闭所有窗口进入纯后台模式，仅保留核心服务在托盘运行。
func (g *GuiApp) HideToTray() {
	g.inWebUI.Store(false)
	application.InvokeAsync(func() {
		if g.webUIWin != nil {
			g.webUISilentClose.Store(true)
			g.webUIWin.Close()
		}
		if g.loginWin != nil {
			g.loginClosing.Store(true)
			g.loginWin.Close()
		}
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
	sendOSNotification("Subs Check PRO", "已关闭界面窗口，核心服务在后台持续运行\n点击系统托盘图标可重新打开")
}

// showActiveWindow 恢复/唤起当前主窗口（WebUI 最小化进托盘则恢复原 WebUI，否则打开登录窗口）。
func (g *GuiApp) showActiveWindow() {
	application.InvokeAsync(func() {
		if g.inWebUI.Load() && g.webUIWin != nil {
			showWindow(g.webUIWin)
		} else {
			g.inWebUI.Store(false)
			g.createLoginWindow()
		}
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
}

// hideActiveWindow 将当前活动窗口收进托盘：WebUI 保留状态并休眠前端；登录窗直接销毁。
func (g *GuiApp) hideActiveWindow() {
	application.InvokeAsync(func() {
		if g.inWebUI.Load() && g.webUIWin != nil {
			hideWindow(g.webUIWin)
		} else if g.loginWin != nil {
			g.loginClosing.Store(true)
			g.loginWin.Close()
		}
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
}

// QuitApp 供前端"关闭确认对话框"选择退出时调用。
func (g *GuiApp) QuitApp() {
	sendOSNotification("Subs Check PRO", "GUI正在关闭…")
	app := application.Get()
	if app != nil {
		app.Quit()
	}
}

// OpenConfigFile 打开系统文件选择对话框。
func (g *GuiApp) OpenConfigFile() (string, error) {
	app := application.Get()
	if app == nil {
		return "", fmt.Errorf("应用实例不可用")
	}

	result, err := app.Dialog.OpenFile().
		SetTitle("选择配置文件").
		AddFilter("YAML 配置文件", "*.yaml;*.yml").
		PromptForSingleSelection()
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if result == "" &&
			(strings.Contains(errLower, "cancel") ||
				strings.Contains(errLower, "cancelled") ||
				strings.Contains(errLower, "no file") ||
				strings.Contains(errLower, "user cancelled")) {
			return "", nil
		}
		return "", fmt.Errorf("打开文件对话框失败: %w", err)
	}
	return result, nil
}

// ── 端口校验辅助 ─────────────────────────────────────────────────────────────

func normalizePort(port string) string {
	return strings.TrimPrefix(strings.TrimSpace(port), ":")
}

func validatePort(port string) error {
	port = normalizePort(port)

	if port == "" {
		return fmt.Errorf("端口不能为空")
	}

	p, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("端口必须为数字，当前值: %q", port)
	}

	switch {
	case p < 1:
		return fmt.Errorf("端口不能小于 1")
	case p < 1024:
		return fmt.Errorf("端口 %d 是系统保留端口（1-1023），请使用 1024-65535 范围内的端口", p)
	case p > 65535:
		return fmt.Errorf("端口不能大于 65535，当前值: %d", p)
	}

	return nil
}

func isPortInUse(port string) bool {
	if port == "" {
		return false
	}
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// ── 开机自启辅助方法 ──────────────────────────────────────────────────────────

// GetAutoStartEnabled 查询当前开机自启状态（供托盘菜单调用）。
func (g *GuiApp) GetAutoStartEnabled() (bool, error) {
	return g.autostart.IsEnabled()
}

// SetAutoStartEnabled 设置开机自启状态（供托盘菜单内部调用）。
func (g *GuiApp) SetAutoStartEnabled(enable bool) error {
	if enable {
		return g.autostart.Enable()
	}
	return g.autostart.Disable()
}

// SetAutoStart 供前端 JS 绑定调用，切换开机自启。
func (g *GuiApp) SetAutoStart(enabled bool) error {
	if g.autostartMenuItem != nil {
		g.autostartMenuItem.SetChecked(enabled)
	}
	if enabled {
		return g.autostart.Enable()
	}
	return g.autostart.Disable()
}

// OpenInternalPage 在新窗口中打开内置 Web 页面（如 /files、/analysis）。
func (g *GuiApp) OpenInternalPage(path string, title string, windowSize string) {
	listenPort := defaultListenPort()

	baseURL := "http://127.0.0.1:" + listenPort
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	nonce := generateNonce(config.GlobalConfig.APIKey, false)
	targetURL := baseURL + "/gui/enter?n=" + nonce + "&redirect=" + url.QueryEscape(path)

	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}

	application.InvokeAsync(func() {
		opts := newPopupOptions("Subs Free — "+title, targetURL, windowSize)
		opts.MinWidth = 600
		opts.MinHeight = 640
		opts.MaxWidth = 960
		opts.MaxHeight = 660
		popup := wailsApp.Window.NewWithOptions(opts)
		popup.Show()
		popup.Center()
		popup.Focus()
	})
}

// OpenAboutWindow 打开或聚焦「关于」独立窗口（单例模式）。
func (g *GuiApp) OpenAboutWindow() {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}
	application.InvokeAsync(func() {
		if g.aboutWin != nil {
			g.aboutWin.Show()
			g.aboutWin.Focus()
			return
		}
		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "about",
			Title:          "Subs Free — 关于",
			Width:          800,
			Height:         600,
			MinWidth:       640,
			MinHeight:      480,
			DisableResize:  false,
			Frameless:      false,
			URL:            "/about.html",
			Mac:            macWindowOpts(30),
			BackgroundType: application.BackgroundTypeTranslucent,
		})
		g.aboutWin = win
		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			g.aboutWin = nil
		})
	})
}

// CheckForUpdates 触发更新检查，在自定义更新窗口中展示结果。
func (g *GuiApp) CheckForUpdates() {
	if g.updaterApp == nil {
		sendOSNotification("Subs Free", "更新检查暂不可用")
		application.Get().Event.Emit("gui:update:toast", "更新检查暂不可用")
		return
	}

	if g.updateWin != nil {
		application.InvokeAsync(func() {
			g.updateWin.Show()
			g.updateWin.Focus()
		})
		return
	}

	if !g.checkingUpdate.CompareAndSwap(false, true) {
		g.updaterApp.Event.Emit("gui:update:toast", "正在检查更新，请稍候…")
		return
	}

	go func() {
		defer g.checkingUpdate.Store(false)

		ctx, cancel := context.WithTimeout(context.Background(), updater.CheckTimeout)
		defer cancel()

		updateInfo, err := g.updaterApp.Updater.Check(ctx)
		if err != nil {
			slog.Warn("检查更新失败", "error", err)
			sendOSNotification("Subs Check PRO GUI 更新失败", err.Error())
			g.updaterApp.Event.Emit("gui:update:toast", "检查更新失败: "+err.Error())
			return
		}

		if updateInfo == nil {
			slog.Debug("当前已是最新版")
			g.updaterApp.Event.Emit("gui:update:toast", "已经是最新版")
			sendOSNotification("Subs Free", "已经是最新版")
			return
		}

		g.showUpdateWindow(updateInfo)
	}()
}

// showUpdateWindow 打开（或复用）自定义更新窗口。
func (g *GuiApp) showUpdateWindow(rel *wupdater.Release) {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}

	application.InvokeAsync(func() {
		if g.updateWin != nil {
			g.emitUpdateSnapshot(rel)
			g.updateWin.Show()
			g.updateWin.Focus()
			return
		}

		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "updater",
			Title:          "Subs Free — 检查更新",
			Width:          520,
			Height:         540,
			MinWidth:       348,
			MinHeight:      161,
			DisableResize:  false,
			Frameless:      false,
			URL:            "/updater.html",
			Mac:            macWindowOpts(40),
			BackgroundType: application.BackgroundTypeTranslucent,
		})
		g.updateWin = win

		ctx := context.Background()

		g.subscribeUpdateEvent(wupdater.EventWindowReady, func(_ *application.CustomEvent) {
			g.emitUpdateSnapshot(rel)
		})

		g.subscribeUpdateEvent(wupdater.EventUserInstall, func(_ *application.CustomEvent) {
			go func() {
				dlCtx, dlCancel := context.WithTimeout(ctx, updater.MaxDownloadDuration)
				defer dlCancel()
				err := g.updaterApp.Updater.DownloadAndInstall(dlCtx)
				if err != nil {
					slog.Warn("下载/安装更新失败", "error", err)
					if g.updaterApp != nil {
						g.updaterApp.Event.Emit(wupdater.EventError, map[string]any{
							"message": err.Error(),
							"stage":   "install",
						})
					}
					return
				}
				if g.updaterApp != nil {
					g.updaterApp.Event.Emit(wupdater.EventUpdateReady, nil)
				}
			}()
		})

		g.subscribeUpdateEvent(wupdater.EventUserRestart, func(_ *application.CustomEvent) {
			go func() {
				if err := g.updaterApp.Updater.Restart(ctx); err != nil {
					slog.Warn("重启应用更新失败", "error", err)
					sendOSNotification("Subs Check PRO GUI 更新失败", err.Error())
				}
			}()
		})

		g.subscribeUpdateEvent(wupdater.EventUserSkip, func(_ *application.CustomEvent) {
			g.updaterApp.Updater.SkipVersion(rel.Version)
			g.closeUpdateWindow()
		})

		g.subscribeUpdateEvent("wails:updater:user:background", func(_ *application.CustomEvent) {
			application.InvokeAsync(func() {
				if g.updateWin != nil {
					g.updateWin.Hide()
				}
			})
		})

		g.subscribeUpdateEvent(wupdater.EventUserRemind, func(_ *application.CustomEvent) {
			application.InvokeAsync(func() {
				if g.updateWin != nil {
					g.updateWin.Hide()
				}
			})
		})

		g.subscribeUpdateEvent(wupdater.EventUserCancel, func(_ *application.CustomEvent) {
			g.closeUpdateWindow()
		})

		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			g.teardownUpdateWindow()
		})

		g.emitUpdateSnapshot(rel)
		win.Show()
	})
}

func (g *GuiApp) subscribeUpdateEvent(name string, fn func(*application.CustomEvent)) {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}
	off := wailsApp.Event.On(name, fn)
	g.updateWinUnsubs = append(g.updateWinUnsubs, off)
}

func (g *GuiApp) emitUpdateSnapshot(rel *wupdater.Release) {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}
	wailsApp.Event.Emit(wupdater.EventMeta, wupdater.Meta{
		CurrentVersion: GuiVersion,
		SkippedVersion: g.updaterApp.Updater.SkippedVersion(),
	})
	wailsApp.Event.Emit(wupdater.EventUpdateAvailable, rel)
}

func (g *GuiApp) closeUpdateWindow() {
	if g.updateWin != nil {
		g.updateWin.Close()
	}
}

func (g *GuiApp) teardownUpdateWindow() {
	for _, off := range g.updateWinUnsubs {
		if off != nil {
			off()
		}
	}
	g.updateWinUnsubs = nil
	g.updateWin = nil
}

// UpdateInfo 前端展示更新状态所需的结构体。
type UpdateInfo struct {
	HasUpdate      bool   `json:"hasUpdate"`
	LatestVersion  string `json:"latestVersion"`
	CurrentVersion string `json:"currentVersion"`
	ReleaseNotes   string `json:"releaseNotes"`
	DownloadURL    string `json:"downloadURL"`
	Error          string `json:"error"`

	PublishDate string `json:"publishDate"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Filetype    string `json:"filetype"`
	AssetSize   string `json:"assetSize"`
}

// GetUpdateInfo 通过 Wails 内置更新器查询更新状态，返回给前端。
func (g *GuiApp) GetUpdateInfo() UpdateInfo {
	if g.updaterApp == nil {
		return UpdateInfo{Error: "更新器未初始化"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), updater.CheckTimeout)
	defer cancel()

	rel, err := g.updaterApp.Updater.Check(ctx)
	if err != nil {
		return UpdateInfo{Error: "检查更新失败: " + err.Error()}
	}

	current := GuiVersion
	if current == "" || current == "dev" {
		current = "0.0.0"
	}

	if rel == nil {
		return UpdateInfo{
			HasUpdate:      false,
			CurrentVersion: current,
		}
	}

	tagName := rel.Version
	if !strings.HasPrefix(tagName, "v") {
		tagName = "v" + tagName
	}

	targetURL := fmt.Sprintf("https://github.com/sinspired/subs-free/releases/tag/%s", tagName)
	sizeMB := float64(rel.Artifact.Size) / (1024 * 1024)
	sizeStr := fmt.Sprintf("%.2f MB", sizeMB)

	pubDate := ""
	if !rel.PublishedAt.IsZero() {
		pubDate = rel.PublishedAt.Format("2006-01-02")
	}

	return UpdateInfo{
		HasUpdate:      true,
		LatestVersion:  tagName,
		CurrentVersion: current,
		ReleaseNotes:   rel.Notes,
		DownloadURL:    updater.GhProxyBase + targetURL,
		PublishDate:    pubDate,
		Platform:       rel.Artifact.Platform,
		Arch:           rel.Artifact.Arch,
		Filetype:       rel.Artifact.Filetype,
		AssetSize:      sizeStr,
	}
}

// OpenFilesWindow 打开或聚焦「内置文件」独立窗口（单例模式）。
func (g *GuiApp) OpenFilesWindow() {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}
	application.InvokeAsync(func() {
		if g.filesWin != nil {
			g.filesWin.Show()
			g.filesWin.Focus()
			return
		}
		listenPort := defaultListenPort()
		nonce := generateNonce(config.GlobalConfig.APIKey, false)
		targetURL := "http://127.0.0.1:" + listenPort +
			"/gui/enter?n=" + nonce + "&redirect=/files"
		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "files",
			Title:          "Subs Free — 内置文件",
			Width:          940,
			Height:         640,
			MinWidth:       800,
			MaxWidth:       960,
			MinHeight:      640,
			MaxHeight:      660,
			URL:            targetURL,
			Mac:            macWindowOpts(50),
			BackgroundType: application.BackgroundTypeTranslucent,
		})
		g.filesWin = win
		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			g.filesWin = nil
		})
		win.Show()
		win.Center()
		win.Focus()
	})
}

// OpenAnalysisWindow 打开或聚焦「分析报告」独立窗口（单例模式）。
func (g *GuiApp) OpenAnalysisWindow() {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}
	application.InvokeAsync(func() {
		if g.analysisWin != nil {
			g.analysisWin.Show()
			g.analysisWin.Focus()
			return
		}
		listenPort := defaultListenPort()
		nonce := generateNonce(config.GlobalConfig.APIKey, false)
		targetURL := "http://127.0.0.1:" + listenPort +
			"/gui/enter?n=" + nonce + "&redirect=/analysis"
		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "analysis",
			Title:          "Subs Free — 分析报告",
			Width:          1200,
			Height:         700,
			MinWidth:       800,
			MinHeight:      600,
			URL:            targetURL,
			Mac:            macWindowOpts(50),
			BackgroundType: application.BackgroundTypeTranslucent,
		})
		g.analysisWin = win
		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			g.analysisWin = nil
		})
		win.Show()
		win.Center()
		win.Focus()
	})
}

// OpenSubStoreWindow 打开或聚焦 Sub-Store 订阅管理独立窗口（单例模式）。
func (g *GuiApp) OpenSubStoreWindow() {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}

	subStorePort := strings.TrimPrefix(config.GlobalConfig.SubStorePort, ":")
	if subStorePort == "" {
		return
	}

	baseURL := "http://127.0.0.1:" + subStorePort
	subStorePath := strings.TrimSpace(config.GlobalConfig.SubStorePath)
	var targetURL string
	if subStorePath != "" {
		if !strings.HasPrefix(subStorePath, "/") {
			subStorePath = "/" + subStorePath
		}
		targetURL = baseURL + "?api=" + subStorePath
	} else {
		targetURL = baseURL
	}

	capturedURL := targetURL
	application.InvokeAsync(func() {
		if g.subStoreWin != nil {
			g.subStoreWin.Show()
			g.subStoreWin.Focus()
			return
		}
		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "sub-store",
			Title:          "Sub-Store — 订阅管理",
			Width:          530,
			Height:         600,
			MinWidth:       530,
			MinHeight:      500,
			URL:            capturedURL,
			Mac:            macWindowOpts(50),
			BackgroundType: application.BackgroundTypeTranslucent,
		})
		g.subStoreWin = win
		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			g.subStoreWin = nil
		})
		win.Show()
		win.Center()
		win.Focus()
	})
}

// OpenSubLinksWindow 打开或聚焦「订阅链接」独立窗口（单例模式）。
func (g *GuiApp) OpenSubLinksWindow() {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}
	application.InvokeAsync(func() {
		if g.subLinksWin != nil {
			g.subLinksWin.Show()
			g.subLinksWin.Focus()
			return
		}

		winHeight := 545
		minHeight := 545
		maxHeight := 560

		if runtime.GOOS == "darwin" {
			winHeight = 560
			minHeight = 560
			maxHeight = 575
		}

		ips := getLocalIPv4s()
		baseWidth := 520

		Width := baseWidth + (len(ips)-2)*120
		MinWidth := Width - 0
		maxWidth := Width + 20

		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "sub-links",
			Title:          "Subs Free — 订阅链接",
			Width:          Width,
			Height:         winHeight,
			MinWidth:       MinWidth,
			MinHeight:      minHeight,
			MaxWidth:       maxWidth,
			MaxHeight:      maxHeight,
			DisableResize:  false,
			Frameless:      false,
			URL:            "/sub-links.html",
			Mac:            macWindowOpts(40),
			BackgroundType: application.BackgroundTypeTranslucent,
		})

		g.subLinksWin = win
		win.Center()
		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			g.subLinksWin = nil
		})
	})
}

func (g *GuiApp) GetUpdateStatus() updater.UpdateStatus {
	return updater.GetUpdateStatus()
}

// CheckState 前端展示检测进度所需的状态信息
type CheckState struct {
	IsChecking bool   `json:"isChecking"`
	StepName   string `json:"stepName"`
	Progress   int64  `json:"progress"`
	ProxyCount int64  `json:"proxyCount"`
	LastResult string `json:"lastResult"`
}

// GetCheckState 返回当前后端的检测进度状态，供灵动岛等前端页面调用
func (g *GuiApp) GetCheckState() CheckState {
	if g.backend == nil || g.pendingInit {
		return CheckState{
			IsChecking: false,
			StepName:   "内核未就绪",
		}
	}

	isChecking := g.backend.IsChecking()
	st := g.backend.GetCurrentState()

	return CheckState{
		IsChecking: isChecking,
		StepName:   st.StepName,
		Progress:   st.Progress,
		ProxyCount: st.ProxyCount,
		LastResult: g.backend.GetLastCheckResult(),
	}
}
