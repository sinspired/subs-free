// cmd\desktop\app.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreapp "github.com/sinspired/subs-check-pro/v3/app"
	"github.com/sinspired/subs-check-pro/v3/config"
	"github.com/sinspired/subs-check-pro/v3/utils"
	"github.com/sinspired/subs-free/updater"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wupdater "github.com/wailsapp/wails/v3/pkg/updater"
	ghprovider "github.com/wailsapp/wails/v3/pkg/updater/providers/github"
	"gopkg.in/yaml.v3"
)

// globalGuiApp 包级指针，供 router handler（如 handleGuiBackToMain）访问。
var globalGuiApp *GuiApp

// GuiApp Wails 绑定结构体。
type GuiApp struct {
	configPath string
	backend    *coreapp.App

	// mainWin App 主界面窗口（按需创建；进入管理面板 / 收进托盘时销毁）。
	mainWin *application.WebviewWindow
	// webUIWin WebUI 管理面板大窗（按需创建；关闭时销毁并回到主界面，最小化则隐藏进托盘）。
	webUIWin *application.WebviewWindow

	// mainClosing 为 true 时放行主界面直接销毁（不弹出退出确认框）。
	mainClosing atomic.Bool
	// webUISilentClose 为 true 时表示代码主动关闭 WebUI（如 BackToMain / HideToTray），不重复打开主界面。
	webUISilentClose atomic.Bool

	// keyManualOverride 标记本次运行期间用户已手动修改过 API Key，立即消除随机密钥告警。
	keyManualOverride atomic.Bool

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
	logsWin     *application.WebviewWindow

	// updateWin 自定义更新窗口（懒加载，复用）。
	updateWin       *application.WebviewWindow
	updateWinUnsubs []func()

	// autostart Wails 跨平台开机自启管理器
	autostart *application.AutostartManager

	// updaterApp 持有 wails App 引用，供 CheckForUpdates 调用 Updater。
	updaterApp *application.App

	// checkingUpdate 防止用户连续快速点击"检查更新"图标
	checkingUpdate atomic.Bool

	// onTraySync 由 tray.go 注入，用于在窗口或自启状态变化时刷新托盘菜单语义。
	onTraySync func()

	// autostartCached 缓存开机自启状态，供托盘菜单快速比对刷新。
	autostartCached atomic.Bool
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

// createMainWindow 在主线程创建或聚焦 App 主界面窗口。
func (g *GuiApp) createMainWindow() {
	wailsApp := application.Get()
	if wailsApp == nil {
		wailsApp = g.updaterApp
	}
	if wailsApp == nil {
		return
	}

	if g.mainWin != nil {
		if g.mainWin.IsMinimised() {
			g.mainWin.UnMinimise()
		}
		g.mainWin.Show()
		g.mainWin.Focus()
		windowVisible.Store(true)
		if g.onTraySync != nil {
			g.onTraySync()
		}
		return
	}

	win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:          "main",
		Title:         "Subs Free",
		Width:         580,
		Height:        416,
		MinWidth:      560,
		MaxWidth:      600,
		MinHeight:     400,
		MaxHeight:     426,
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

	g.mainWin = win
	windowVisible.Store(true)

	// 点击主界面 ×：拦截并弹出 QuitDialog，由用户选择「退出」或「后台运行」
	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if g.mainClosing.CompareAndSwap(true, false) {
			g.mainWin = nil
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

	// 保留主界面原生最小化到任务栏，仅同步托盘菜单状态
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

// openMainWindow 从任意 goroutine 安全唤起 App 主界面窗口。
func (g *GuiApp) openMainWindow() {
	application.InvokeAsync(g.createMainWindow)
}

// closeMainWindow 主动销毁主界面窗口（不弹出退出确认框）。
func (g *GuiApp) closeMainWindow() {
	application.InvokeAsync(func() {
		if g.mainWin == nil {
			return
		}
		g.mainClosing.Store(true)
		g.mainWin.Close()
	})
}

// NotifyAutostartChanged 安全向主界面同步开机自启 / 静默自启状态变化事件。
func (g *GuiApp) NotifyAutostartChanged(enabled bool) {
	application.InvokeAsync(func() {
		if g.mainWin != nil {
			g.mainWin.EmitEvent("autostart:changed", enabled)
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

// EnterWebUI 由前端调用：按需创建本地 WebUI 管理面板大窗，同时销毁主界面窗口省内存。
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

			// WebUI 关闭 ×：直接销毁管理面板释放 WebView2 进程，并回到 App 主界面
			win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
				g.webUIWin = nil
				g.inWebUI.Store(false)
				if g.mainWin == nil {
					windowVisible.Store(false)
				}
				if g.onTraySync != nil {
					g.onTraySync()
				}
				if g.webUISilentClose.CompareAndSwap(true, false) {
					return
				}
				g.openMainWindow()
			})

			// WebUI 最小化 _：隐藏收进托盘并暂停前端轮询，保留原窗口状态
			win.OnWindowEvent(events.Common.WindowMinimise, func(e *application.WindowEvent) {
				hideWindow(win)
				sendOSNotification("Subs Free", "管理面板已最小化到系统托盘，点击托盘图标可恢复")
				if g.onTraySync != nil {
					g.onTraySync()
				}
			})

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

		// 进入管理界面后销毁主界面窗口，避免同时常驻两个 WebView2 进程
		if g.mainWin != nil {
			g.mainClosing.Store(true)
			g.mainWin.Close()
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

// BackToMain 从管理面板返回 App 主界面（销毁 WebUI 进程并重新打开主界面）
func (g *GuiApp) BackToMain() {
	g.inWebUI.Store(false)
	g.openMainWindow()
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
		KeyIsRandom:          !g.keyManualOverride.Load() && os.Getenv("GUI_KEY_IS_RANDOM") == "1",
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

	sendOSNotification("Subs Free", "内核服务已成功启动")
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
	g.keyManualOverride.Store(false)

	sendOSNotification("Subs Free", "内核已切换到新配置并重新启动")
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

// ShowWindow 供前端主动调用，显示并聚焦主界面窗口。
func (g *GuiApp) ShowWindow() {
	g.openMainWindow()
}

// TrayWindowState 描述当前窗口与界面所处真实状态，供托盘菜单和单击切换使用。
type TrayWindowState struct {
	Mode      string // "background" | "main" | "webui"
	IsVisible bool
}

// GetTrayWindowState 必须在主线程调用，返回真实窗口状态。
func (g *GuiApp) GetTrayWindowState() TrayWindowState {
	if g.inWebUI.Load() && g.webUIWin != nil {
		vis := g.webUIWin.IsVisible() && !g.webUIWin.IsMinimised()
		return TrayWindowState{Mode: "webui", IsVisible: vis}
	}
	if g.mainWin != nil {
		vis := g.mainWin.IsVisible() && !g.mainWin.IsMinimised()
		return TrayWindowState{Mode: "main", IsVisible: vis}
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
		case "main":
			if st.IsVisible {
				g.mainWin.Minimise()
			} else {
				if g.mainWin.IsMinimised() {
					g.mainWin.UnMinimise()
				}
				g.mainWin.Show()
				g.mainWin.Focus()
				windowVisible.Store(true)
			}
		default: // "background"
			g.inWebUI.Store(false)
			g.createMainWindow()
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
		if g.mainWin != nil {
			g.mainClosing.Store(true)
			g.mainWin.Close()
		}
		if g.logsWin != nil {
			g.logsWin.Close()
		}
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
	sendOSNotification("Subs Free", "已关闭界面窗口，核心服务在后台持续运行\n点击系统托盘图标可重新打开")
}

// showActiveWindow 从托盘/二次启动恢复窗口：WebUI 最小化进托盘则恢复原 WebUI，否则打开主界面。
func (g *GuiApp) showActiveWindow() {
	application.InvokeAsync(func() {
		if g.inWebUI.Load() && g.webUIWin != nil {
			showWindow(g.webUIWin)
		} else {
			g.inWebUI.Store(false)
			g.createMainWindow()
		}
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
}

// hideActiveWindow 将当前活动窗口收进托盘：WebUI 保留状态并休眠前端；主界面直接销毁。
func (g *GuiApp) hideActiveWindow() {
	application.InvokeAsync(func() {
		if g.inWebUI.Load() && g.webUIWin != nil {
			hideWindow(g.webUIWin)
		} else if g.mainWin != nil {
			g.mainClosing.Store(true)
			g.mainWin.Close()
		}
		if g.onTraySync != nil {
			g.onTraySync()
		}
	})
}

// QuitApp 供前端"关闭确认对话框"选择退出时调用。
func (g *GuiApp) QuitApp() {
	sendOSNotification("Subs Free", "应用正在关闭…")
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

// ── GUI 偏好持久化 & 开机自启 ──────────────────────────────────────────────

// GetAutoStartEnabled 查询当前开机自启状态。
func (g *GuiApp) GetAutoStartEnabled() (bool, error) {
	return g.autostart.IsEnabled()
}

type guiPrefs struct {
	SilentAutostart   bool   `json:"silentAutostart"`
	UpdaterToken      string `json:"updaterToken,omitempty"`
	UpdaterPrerelease bool   `json:"updaterPrerelease"`
}

func guiPrefsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "gui-prefs.json"
	}
	return filepath.Join(dir, "subs-free", "gui-prefs.json")
}

func loadGuiPrefs() guiPrefs {
	var p guiPrefs
	data, err := os.ReadFile(guiPrefsPath())
	if err != nil {
		return p
	}
	_ = json.Unmarshal(data, &p)
	return p
}

func saveGuiPrefs(p guiPrefs) error {
	path := guiPrefsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (g *GuiApp) applyAutostart(enabled, silent bool) error {
	// 先清理旧注册项，防止与不带 --hidden 的历史条目并存
	if err := g.autostart.Disable(); err != nil {
		slog.Debug("清理旧自启注册项失败（可忽略）", "error", err)
	}
	if !enabled {
		return nil
	}

	opts := application.AutostartOptions{
		Identifier: "com.sinspired.subs-free",
	}
	if silent {
		opts.Arguments = []string{"--hidden"}
	}

	if err := g.autostart.EnableWithOptions(opts); err != nil {
		return err
	}

	if st, err := g.autostart.Status(); err == nil {
		slog.Info("开机自启配置已更新", "enabled", st.Enabled, "strategy", st.Strategy, "path", st.Path, "silent", silent)
	}
	return nil
}

// GetSilentAutoStartEnabled 读取是否开启「静默自启」。
func (g *GuiApp) GetSilentAutoStartEnabled() bool {
	return loadGuiPrefs().SilentAutostart
}

// SetAutostartMode 统一设置自启开关与是否静默（供托盘子菜单调用）。
func (g *GuiApp) SetAutostartMode(enabled, silent bool) error {
	prefs := loadGuiPrefs()
	if enabled {
		prefs.SilentAutostart = silent
		if err := saveGuiPrefs(prefs); err != nil {
			return err
		}
	}
	if err := g.applyAutostart(enabled, prefs.SilentAutostart); err != nil {
		return err
	}
	g.autostartCached.Store(enabled)
	g.NotifyAutostartChanged(enabled)
	if g.onTraySync != nil {
		application.InvokeAsync(g.onTraySync)
	}
	return nil
}

// SetSilentAutoStart 供前端开关调用，切换「静默自启」。
func (g *GuiApp) SetSilentAutoStart(silent bool) error {
	prefs := loadGuiPrefs()
	prefs.SilentAutostart = silent
	if err := saveGuiPrefs(prefs); err != nil {
		return err
	}
	if enabled, err := g.autostart.IsEnabled(); err == nil && enabled {
		if err := g.applyAutostart(true, silent); err != nil {
			return err
		}
	}
	g.NotifyAutostartChanged(silent)
	if g.onTraySync != nil {
		application.InvokeAsync(g.onTraySync)
	}
	return nil
}

// SetAutoStart 供前端开关调用，切换「开机自启」。
func (g *GuiApp) SetAutoStart(enabled bool) error {
	silent := loadGuiPrefs().SilentAutostart
	if err := g.applyAutostart(enabled, silent); err != nil {
		return err
	}
	g.autostartCached.Store(enabled)
	if g.onTraySync != nil {
		application.InvokeAsync(g.onTraySync)
	}
	return nil
}

// SetAutoStartEnabled 保留兼容调用。
func (g *GuiApp) SetAutoStartEnabled(enable bool) error {
	return g.SetAutoStart(enable)
}

// ── 子窗口与内置页面 ──────────────────────────────────────────────────────────

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
			sendOSNotification("Subs Free 更新失败", err.Error())
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
					sendOSNotification("Subs Free 更新失败", err.Error())
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

// OpenLogsWindow 打开或聚焦「实时日志」独立窗口（单例模式）。
func (g *GuiApp) OpenLogsWindow() {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}
	application.InvokeAsync(func() {
		if g.logsWin != nil {
			if g.logsWin.IsMinimised() {
				g.logsWin.UnMinimise()
			}
			g.logsWin.Show()
			g.logsWin.Focus()
			return
		}

		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "logs",
			Title:          "Subs Free — 实时日志",
			Width:          780,
			Height:         500,
			MinWidth:       520,
			MinHeight:      360,
			DisableResize:  false,
			Frameless:      false,
			URL:            "/?window=logs",
			Mac:            macWindowOpts(30),
			BackgroundType: application.BackgroundTypeTranslucent,
		})
		g.logsWin = win
		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			g.logsWin = nil
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

// CheckState 前端展示检测进度与下次计划所需的状态信息
type CheckState struct {
	IsChecking    bool   `json:"isChecking"`
	StepName      string `json:"stepName"`
	Available     int64  `json:"available"`
	Progress      int64  `json:"progress"`
	ProxyCount    int64  `json:"proxyCount"`
	LastResult    string `json:"lastResult"`
	NextCheckTime string `json:"nextCheckTime"`
	NextCheckUnix int64  `json:"nextCheckUnix"`
	ScheduleMode  string `json:"scheduleMode"`
}

func formatDesktopNextCheckLabel(t time.Time) string {
	local := t.Local()
	now := time.Now().Local()

	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	targetDay := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	diffDays := int(targetDay.Sub(today).Hours() / 24)

	switch diffDays {
	case 0:
		return "今天 " + local.Format("15:04")
	case 1:
		return "明天 " + local.Format("15:04")
	default:
		return local.Format("01-02 15:04")
	}
}

// GetCheckState 返回当前后端的检测进度与下次检测时间
func (g *GuiApp) GetCheckState() CheckState {
	if g.backend == nil || g.pendingInit {
		return CheckState{
			IsChecking: false,
			StepName:   "内核未就绪",
		}
	}

	st := g.backend.GetCurrentState()
	nextTime, mode := g.backend.GetNextCheckTime()

	var nextStr string
	var nextUnix int64
	if !nextTime.IsZero() {
		nextUnix = nextTime.Unix()
		nextStr = formatDesktopNextCheckLabel(nextTime)
	}

	return CheckState{
		IsChecking:    g.backend.IsChecking(),
		StepName:      st.StepName,
		Available:     st.Available,
		Progress:      st.Progress,
		ProxyCount:    st.ProxyCount,
		LastResult:    g.backend.GetLastCheckResult(),
		NextCheckTime: nextStr,
		NextCheckUnix: nextUnix,
		ScheduleMode:  mode,
	}
}

// ── 动态 GitHub 更新 Provider（突破 Updater.Init 单次限制，支持热更新配置）──────

type dynamicGitHubProvider struct {
	repo      string
	mu        sync.Mutex
	lastToken string
	lastPre   bool
	inner     wupdater.Provider
}

func newDynamicGitHubProvider(repo string) *dynamicGitHubProvider {
	return &dynamicGitHubProvider{repo: repo}
}

func (p *dynamicGitHubProvider) Name() string {
	return "github"
}

func (p *dynamicGitHubProvider) getProvider() (wupdater.Provider, error) {
	prefs := loadGuiPrefs()

	// 前端优先；前端未设置时回退到后端 config.yaml 中的 github-token
	token := strings.TrimSpace(prefs.UpdaterToken)
	if token == "" {
		token = strings.TrimSpace(config.GlobalConfig.GithubToken)
	}
	prerelease := prefs.UpdaterPrerelease

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.inner != nil && p.lastToken == token && p.lastPre == prerelease {
		return p.inner, nil
	}

	gh, err := ghprovider.New(ghprovider.Config{
		Repository:    p.repo,
		Token:         token,
		Prerelease:    prerelease,
		ChecksumAsset: "SHA256SUMS",
		HTTPClient:    updater.NewHTTPClient(),
		AssetMatcher:  updater.AssetMatcher,
	})
	if err != nil {
		return nil, err
	}

	p.inner = gh
	p.lastToken = token
	p.lastPre = prerelease
	return p.inner, nil
}

func (p *dynamicGitHubProvider) Check(ctx context.Context, req wupdater.CheckRequest) (*wupdater.Release, error) {
	prov, err := p.getProvider()
	if err != nil {
		return nil, err
	}
	return prov.Check(ctx, req)
}

func (p *dynamicGitHubProvider) Download(ctx context.Context, r *wupdater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	prov, err := p.getProvider()
	if err != nil {
		return err
	}
	return prov.Download(ctx, r, dst, onProgress)
}

// UpdaterSettings 前端设置弹窗展示与保存所需的结构体。
type UpdaterSettings struct {
	Token           string `json:"token"`           // 前端设置的 Token（前端优先）
	Prerelease      bool   `json:"prerelease"`      // 是否检测预览版
	HasBackendToken bool   `json:"hasBackendToken"` // 后端是否配置了 github-token
	EffectiveSource string `json:"effectiveSource"` // "frontend" | "backend" | "none"
}

// GetUpdaterSettings 返回当前更新器配置及 Token 生效来源（不向前端暴露后端明文 Token）。
func (g *GuiApp) GetUpdaterSettings() UpdaterSettings {
	prefs := loadGuiPrefs()
	feToken := strings.TrimSpace(prefs.UpdaterToken)
	beToken := strings.TrimSpace(config.GlobalConfig.GithubToken)

	source := "none"
	if feToken != "" {
		source = "frontend"
	} else if beToken != "" {
		source = "backend"
	}

	return UpdaterSettings{
		Token:           prefs.UpdaterToken,
		Prerelease:      prefs.UpdaterPrerelease,
		HasBackendToken: beToken != "",
		EffectiveSource: source,
	}
}

// SaveUpdaterSettings 持久化更新器配置（下次 Check/Download 自动热生效，重启不丢失）。
func (g *GuiApp) SaveUpdaterSettings(token string, prerelease bool) (UpdaterSettings, error) {
	prefs := loadGuiPrefs()
	prefs.UpdaterToken = strings.TrimSpace(token)
	prefs.UpdaterPrerelease = prerelease
	if err := saveGuiPrefs(prefs); err != nil {
		return UpdaterSettings{}, err
	}
	return g.GetUpdaterSettings(), nil
}

// MarkAPIKeyManual 标记本次运行期间已手动设置过 API Key，立刻消除随机密钥告警。
func (g *GuiApp) MarkAPIKeyManual() {
	g.keyManualOverride.Store(true)
}

// APIProxy 在进程内直调 Gin 路由，供桌面主界面无 CORS 地读写配置、启停检测、拉取报告与日志。
func (g *GuiApp) APIProxy(method, path, body string) string {
	type apiResult struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	fail := func(status int, msg string) string {
		b, _ := json.Marshal(apiResult{
			Status: status,
			Body:   fmt.Sprintf(`{"error":%q}`, msg),
		})
		return string(b)
	}

	if g.backend == nil || g.pendingInit {
		return fail(503, "内核尚未就绪")
	}

	router := g.backend.GetRouter()
	if router == nil {
		if strings.EqualFold(method, "POST") && path == "/api/trigger-check" {
			g.backend.TriggerCheck()
			return `{"status":200,"body":"{\"message\":\"已触发检测\"}"}`
		}
		return fail(503, "路由器未初始化（请检查 listen-port）")
	}

	req := httptest.NewRequest(strings.ToUpper(method), path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-API-Key", config.GlobalConfig.APIKey)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	result, err := json.Marshal(apiResult{
		Status: rec.Code,
		Body:   rec.Body.String(),
	})
	if err != nil {
		return fail(500, "响应序列化失败")
	}
	return string(result)
}
