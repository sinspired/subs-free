// app.go
package main

import (
	"context"
	"fmt"
	"runtime"

	coreapp "github.com/sinspired/subs-check-pro/v3/app"
	"github.com/sinspired/subs-check-pro/v3/config"
	"github.com/sinspired/subs-check-pro/v3/utils"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wupdater "github.com/wailsapp/wails/v3/pkg/updater"

	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sinspired/subs-free/cmd/desktop/updater"

	"gopkg.in/yaml.v3"
)

// globalGuiApp 包级指针，供 router handler（如 handleGuiBackToLogin）访问。
var globalGuiApp *GuiApp

// GuiApp Wails 绑定结构体。
type GuiApp struct {
	configPath string
	backend    *coreapp.App

	// loginWin 登录小窗（加载 Wails 前端资产）。由 main.go 注入。
	loginWin *application.WebviewWindow
	// webUIWin WebUI 大窗（加载 webui/admin.html，初始隐藏）。由 main.go 注入。
	webUIWin *application.WebviewWindow

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
	updateWin *application.WebviewWindow
	// updateWinUnsubs 当前更新窗口注册的事件取消函数，关闭窗口时统一清理。
	updateWinUnsubs []func()

	// autostart Wails 跨平台开机自启管理器
	autostart *application.AutostartManager

	// updaterApp 持有 wails App 引用，供 CheckForUpdates 调用 Updater。
	updaterApp *application.App

	// checkingUpdate 防止用户连续快速点击"检查更新"图标：Check() 经代理
	// 访问 GitHub，耗时可能到十几秒，这段时间里 updateWin 仍是 nil，若不
	// 加这个防重入标志，每次点击都会另起一个 goroutine 重复调用 Check()，
	// 多个 goroutine 前后脚判断出"窗口不存在"后各自尝试创建/订阅更新
	// 窗口，导致窗口状态混乱、事件监听器重复订阅，表现为"点两次图标更新
	// 窗口就出问题""下载完成偶发不显示更新日志"等间歇性故障。
	checkingUpdate atomic.Bool
}

// AppInfo 前端展示所需的应用运行信息。
type AppInfo struct {
	APIKey       string `json:"apiKey"`
	ListenPort   string `json:"listenPort"`
	SubStorePort string `json:"subStorePort"`
	// SubStorePath Sub-Store 后端 API 路径（config.yaml 中的 sub-store-path）。
	SubStorePath string `json:"subStorePath"`
	// KeyIsRandom 为 true 表示 api-key 随机生成（重启后变更）
	KeyIsRandom bool `json:"keyIsRandom"`
	// IsFirstRun 为 true 表示本次是首次运行
	IsFirstRun bool `json:"isFirstRun"`
	// ConfigPath config.yaml 的实际路径
	ConfigPath string `json:"configPath"`
	// PortConflictHTTP 为 true 表示 HTTP 端口被占用
	PortConflictHTTP bool `json:"portConflictHTTP"`
	// PortConflictSubStore 为 true 表示 Sub-Store 端口被占用
	PortConflictSubStore bool `json:"portConflictSubStore"`
	// PendingInit 为 true 表示后端尚未初始化，需要前端先解决端口冲突
	PendingInit bool `json:"pendingInit"`
	// AutostartEnabled 当前平台开机自启状态
	AutostartEnabled bool `json:"autostartEnabled"`
	// GuiVersion 桌面客户端版本（ldflags 注入，如 "v1.2.0"）
	GuiVersion string `json:"guiVersion"`
	// CoreVersion 内核版本+短提交哈希（如 "v2.5.4@7c23868"）
	CoreVersion string `json:"coreVersion"`
	// OriginVersion 内核版本
	OriginVersion string `json:"originVersion"`
	// LocalIPs 本机所有可用局域网 IPv4 地址（不含回环），供订阅链接窗口切换访问地址。
	LocalIPs []string `json:"localIPs"`
}

// getLocalIPv4s 枚举本机所有活动网络接口上的 IPv4 地址（排除回环、
// 未启用、虚拟/回环类接口），用于生成局域网可访问的订阅链接。
// 多网卡（有线+无线、虚拟网卡等）情况下可能返回多个地址，
// 交由前端以胶囊按钮形式展示供用户选择。
func getLocalIPv4s() []string {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		// 跳过未启用或回环接口
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
				continue // 仅取 IPv4，跳过 IPv6
			}
			ips = append(ips, ip4.String())
		}
	}
	return ips
}

// OpenBrandURL 在 Wails 无地址栏窗口中打开品牌 / 社交链接。
func (g *GuiApp) OpenBrandURL(url string, windowSize string) {
	if url == "" {
		return
	}
	// 安全校验：只允许 http/https 协议
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return
	}
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}

	capturedURL := url
	application.InvokeAsync(func() {
		// 先加载本地 loading 页（即时显示，无白屏）。
		// Hash 仅供 loading.html 显示目标域名提示，实际跳转由 Go 端 SetURL 完成。
		opts := newPopupOptions("Subs Check Pro", "/loading.html#"+capturedURL, windowSize)
		popup := wailsApp.Window.NewWithOptions(opts)
		popup.Show()
		popup.Center()
		popup.Focus()

		// 300 ms 对于本地静态页足够，且不会出现 JS 导航被 WebView 拦截的问题。
		time.AfterFunc(300*time.Millisecond, func() {
			application.InvokeAsync(func() {
				popup.SetURL(capturedURL)
			})
		})
	})
}

// EnterWebUI 由前端调用：切换到本地 WebUI 大窗，隐藏登录小窗。
// 每次调用都会用 g.configPath 刷新 URL，确保切换配置后 admin 显示正确的配置路径。
func (g *GuiApp) EnterWebUI() {
	if g.webUIWin == nil || g.loginWin == nil {
		return
	}
	g.inWebUI.Store(true)
	// 将当前 configPath 作为查询参数传入，保证切换配置后 admin 页面
	// 读取到的是新配置文件的路径，而非启动时的旧路径。
	adminURL := "/webui/admin.html?configPath=" + url.QueryEscape(g.configPath)
	g.webUIWin.SetURL(adminURL)
	g.webUIWin.Show()
	g.webUIWin.Center()
	g.webUIWin.Focus()
	g.loginWin.Hide()
}

// GetAPIKey 返回当前配置的 API Key，供本地 WebUI 页面通过 Wails binding 调用。
func (g *GuiApp) GetAPIKey() string {
	return config.GlobalConfig.APIKey
}

// defaultListenPort 返回当前配置的 HTTP 监听端口号（不含冒号前缀）。
// GlobalConfig 未初始化或端口为空时，回退到内置默认值 "8199"。
func defaultListenPort() string {
	port := strings.TrimPrefix(config.GlobalConfig.ListenPort, ":")
	if port == "" {
		return "8199"
	}
	return port
}

// GetListenPort 返回 Gin HTTP 服务监听的端口号（不含冒号），
// 供 newCombinedAssetHandler 构造反向代理目标地址使用。
func (g *GuiApp) GetListenPort() string {
	return defaultListenPort()
}

// GetConfigPath 返回当前生效配置文件的绝对路径。
// 以函数引用形式传给 newCombinedAssetHandler，保证切换配置后注入值实时更新。
func (g *GuiApp) GetConfigPath() string {
	return g.configPath
}

// BackToLogin 从 WebUI 返回登录窗口（可选功能，供托盘菜单使用）
func (g *GuiApp) BackToLogin() {
	if g.loginWin == nil {
		return
	}
	g.inWebUI.Store(false)
	g.loginWin.Show()
	g.loginWin.Center()
	g.loginWin.Focus()
	g.webUIWin.Hide()
}

// GetAppInfo 返回应用运行信息（含端口冲突检测）。
func (g *GuiApp) GetAppInfo() AppInfo {
	port := defaultListenPort()
	subStorePort := strings.TrimPrefix(config.GlobalConfig.SubStorePort, ":")

	// 冲突状态仅在 pendingInit 阶段有意义，动态查询当前占用情况。
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

	// 注意：只调用一次 registerGuiRoutes，不重复调用（重复注册同一路径会 panic）。
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

// peekConfigAPIKey 在不影响当前运行配置的前提下，直接读取并解析指定配置文件，
// 返回其中的 api-key。仅用于"切换配置文件"流程中的密钥预校验。
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

// SwitchConfigFile 切换到用户重新选择的配置文件：
//  1. 直接读取目标配置文件中的 api-key 并与用户输入比对（不影响当前运行的内核）；
//  2. 密钥匹配后，关闭（Shutdown）当前正在运行的内核实例；
//  3. 等待旧内核占用的 HTTP 端口完全释放；
//  4. 以新配置文件重新初始化并启动内核（Initialize 内部已包含 InitConfigLoad）；
//  5. 返回切换后的 AppInfo，前端据此刷新界面状态并调用 EnterWebUI 进入管理界面。
//
// 密钥不匹配或步骤 1 失败时，当前内核保持不变。
// 步骤 2–4 失败时旧内核已不可恢复，g.backend 置 nil，g.pendingInit 置 true，
// 错误原样返回给前端展示，用户可重试或重启程序。
func (g *GuiApp) SwitchConfigFile(path, enteredKey string) (AppInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return AppInfo{}, fmt.Errorf("配置文件路径为空")
	}

	// 步骤 1：仅读取目标配置的 api-key，不触碰当前运行中的内核。
	actual, err := peekConfigAPIKey(path)
	if err != nil {
		return AppInfo{}, err
	}
	if strings.TrimSpace(enteredKey) != actual {
		return AppInfo{}, fmt.Errorf("密钥错误")
	}

	// 步骤 2：记录旧 HTTP 端口（Shutdown 后 GlobalConfig 会被覆盖，提前保存）。
	oldHTTPPort := normalizePort(config.GlobalConfig.ListenPort)

	// 步骤 3：关闭旧内核，释放端口和所有后台任务。
	if g.backend != nil {
		if err := g.backend.Shutdown(); err != nil {
			// Shutdown 失败通常是轻微错误（如 watcher 已关闭），继续切换。
			slog.Warn("SwitchConfigFile：关闭旧内核时发生非致命错误", "error", err)
		}
		// 清除引用，防止 OnShutdown 对已关闭的内核执行二次 Shutdown。
		g.backend = nil
	}

	// 步骤 4：等待旧 HTTP 端口完全释放，避免新内核绑定时 "address already in use"。
	if oldHTTPPort != "" {
		waitForPortRelease(oldHTTPPort, 3*time.Second)
	}

	// 步骤 5：创建并初始化新内核。
	// Initialize() 内部已包含 InitConfigLoad()，无需重复调用。
	newBackend := coreapp.New(Version, Version+CurrentCommit, path)
	if err := newBackend.Initialize(); err != nil {
		// 新内核初始化失败：应用处于无后端状态，通知前端显示错误。
		g.pendingInit = true
		return AppInfo{}, fmt.Errorf("初始化新配置失败: %w", err)
	}

	// 新内核拥有全新路由器，重新注册 GUI 专属路由
	// （旧路由器随旧内核一起丢弃，不会触发 panic）。
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

// waitForPortRelease 轮询直到指定端口可用（或超时），用于在旧后端 Shutdown 后、
// 新后端 Initialize 前确保 TCP 端口已被 OS 完全释放，避免 "address already in use"。
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

// ValidatePort 实时验证单个端口号合法性（格式 + 是否被占用）。
// 返回空字符串表示端口格式正确且当前未被占用。
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

// ShowWindow 供前端主动调用，显示并聚焦主窗口。
func (g *GuiApp) ShowWindow() {
	if g.loginWin == nil {
		return
	}
	g.loginWin.Show()
	g.loginWin.Focus()
	windowVisible.Store(true)
}

// HideToTray 供前端"关闭按钮对话框"选择最小化时调用。
func (g *GuiApp) HideToTray() {
	if g.loginWin == nil {
		return
	}
	hideWindow(g.loginWin)
	sendOSNotification("Subs Check PRO", "GUI已最小化到系统托盘")
}

// QuitApp 供前端"关闭按钮对话框"选择退出时调用。
func (g *GuiApp) QuitApp() {
	sendOSNotification("Subs Check PRO", "GUI正在关闭…")
	app := application.Get()
	if app != nil {
		app.Quit()
	}
}

// OpenConfigFile 打开系统文件选择对话框，返回用户选择的配置文件路径。
// 用户取消时返回空字符串（不返回错误）。
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

// ── 双窗口调度辅助 ───────────────────────────────────────────────────────────

// showActiveWindow 根据当前所处模式（WebUI / 登录窗口）显示对应窗口。
func (g *GuiApp) showActiveWindow() {
	if g.inWebUI.Load() {
		if g.webUIWin != nil {
			showWindow(g.webUIWin)
		}
	} else {
		if g.loginWin != nil {
			showWindow(g.loginWin)
		}
	}
}

// hideActiveWindow 根据当前所处模式（WebUI / 登录窗口）隐藏对应窗口。
func (g *GuiApp) hideActiveWindow() {
	if g.inWebUI.Load() {
		if g.webUIWin != nil {
			hideWindow(g.webUIWin)
		}
	} else {
		if g.loginWin != nil {
			hideWindow(g.loginWin)
		}
	}
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

// SetAutoStartEnabled 设置开机自启状态（供托盘菜单内部调用，不重复更新托盘 checkbox）。
// 修复：原实现忽略了 enable 参数，总是调用 Enable()。
func (g *GuiApp) SetAutoStartEnabled(enable bool) error {
	if enable {
		return g.autostart.Enable()
	}
	return g.autostart.Disable()
}

// SetAutoStart 供前端 JS 绑定调用，切换开机自启。
// 成功后同步更新托盘菜单 checkbox，保证两侧状态一致。
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
// 通过 /gui/enter?n=<nonce>&redirect=<path> 中转，确保弹出窗口自动写入 API Key。
func (g *GuiApp) OpenInternalPage(path string, title string, windowSize string) {
	listenPort := defaultListenPort()

	baseURL := "http://127.0.0.1:" + listenPort
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	nonce := generateNonce(config.GlobalConfig.APIKey, false)

	// 使用 url.QueryEscape 对 path 进行编码
	// 防止 path 中本身带有 ?theme=dark 等参数时，破坏完整的 GET 参数结构
	targetURL := baseURL + "/gui/enter?n=" + nonce + "&redirect=" + url.QueryEscape(path)

	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}

	application.InvokeAsync(func() {
		opts := newPopupOptions("Subs Check Pro — "+title, targetURL, windowSize)
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
			Title:          "Subs Check Pro — 关于",
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

// CheckForUpdates 触发更新检查，在自定义更新窗口中展示结果，
// 并等待用户在窗口中点击"下载并安装"后才会真正下载、安装更新。
// 供前端按钮和托盘菜单调用。
func (g *GuiApp) CheckForUpdates() {
	if g.updaterApp == nil {
		sendOSNotification("Subs Check Pro", "更新检查暂不可用")
		// 同时通知前端 toast，避免用户点击后毫无反馈
		application.Get().Event.Emit("gui:update:toast", "更新检查暂不可用")
		return
	}

	// 若更新窗口已存在（后台下载中），直接前置显示，不重新触发检查。
	// 放在加锁之前、goroutine 之外同步判断，响应更快。
	if g.updateWin != nil {
		application.InvokeAsync(func() {
			g.updateWin.Show()
			g.updateWin.Focus()
		})
		return
	}

	// 防重入：Check() 经代理访问 GitHub 耗时不定（最长 updater.CheckTimeout），
	// 这段时间内 updateWin 仍是 nil，用户容易因为"点了没反应"而多点几次。
	// CompareAndSwap 保证同一时刻只有一次 Check() 在跑，重复点击只提示
	// "正在检查中"，不会再并发跑出多个 goroutine 抢着创建/订阅更新窗口。
	if !g.checkingUpdate.CompareAndSwap(false, true) {
		g.updaterApp.Event.Emit("gui:update:toast", "正在检查更新，请稍候…")
		return
	}

	go func() {
		defer g.checkingUpdate.Store(false)

		// 用 updater.CheckTimeout 接上整体上限，之前此处用的是裸 context.Background()，
		// 没有任何超时上限，一旦代理彻底失联，这个 goroutine 会永久挂起。
		ctx, cancel := context.WithTimeout(context.Background(), updater.CheckTimeout)
		defer cancel()

		updateInfo, err := g.updaterApp.Updater.Check(ctx)
		if err != nil {
			slog.Warn("检查更新失败", "error", err)
			sendOSNotification("Subs Check PRO GUI 更新失败", err.Error())
			// emit 给前端 toast 展示
			g.updaterApp.Event.Emit("gui:update:toast", "检查更新失败: "+err.Error())
			return
		}

		if updateInfo == nil {
			// 已经是最新版
			slog.Debug("当前已是最新版")

			// emit 给前端 toast 展示
			g.updaterApp.Event.Emit("gui:update:toast", "已经是最新版")

			// 发送系统通知
			sendOSNotification("Subs Check Pro GUI", "已经是最新版")
			return
		}

		// 发现新版本：打开更新窗口...
		g.showUpdateWindow(updateInfo)
	}()
}

// showUpdateWindow 打开（或复用）自定义更新窗口，回放当前更新状态，
// 并订阅窗口的用户操作事件（user:install / user:restart / user:skip /
// user:remind / user:cancel），驱动后续 DownloadAndInstall / Restart /
// SkipVersion 流程。
//
// 必须在主线程（InvokeAsync）中创建/操作窗口。
func (g *GuiApp) showUpdateWindow(rel *wupdater.Release) {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}

	application.InvokeAsync(func() {
		if g.updateWin != nil {
			// 窗口已存在：仅回放最新状态并前置显示。
			g.emitUpdateSnapshot(rel)
			g.updateWin.Show()
			g.updateWin.Focus()
			return
		}

		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:          "updater",
			Title:         "Subs Check Pro — 检查更新",
			Width:         520,
			Height:        540,
			MinWidth:      348,
			MinHeight:     161,
			DisableResize: false,
			Frameless:     false,
			// 复用前端构建产物（Preact + marked/DOMPurify 渲染 Markdown），
			// 与登录/关于窗口共享同一套样式与依赖，不再使用 go:embed 内嵌 HTML。
			URL:            "/updater.html",
			Mac:            macWindowOpts(40),
			BackgroundType: application.BackgroundTypeTranslucent,
		})
		g.updateWin = win

		// --- 订阅窗口发出的用户操作事件 ---
		// 注意：这个 ctx 不能在此处直接包 context.WithTimeout(updater.MaxDownloadDuration)，
		// 因为它同时被 restart/skip 等事件复用，下载超时不应影响重启。
		// 下载本身的整体超时在 EventUserInstall 的 handler 里单独构造。
		ctx := context.Background()

		g.subscribeUpdateEvent(wupdater.EventWindowReady, func(_ *application.CustomEvent) {
			// 自定义窗口加载完成后会主动请求一次状态回放。
			g.emitUpdateSnapshot(rel)
		})

		g.subscribeUpdateEvent(wupdater.EventUserInstall, func(_ *application.CustomEvent) {
			go func() {
				// 用 updater.MaxDownloadDuration 给整个下载+安装流程一个硬性上限，避免
				// 极端情况下 goroutine 无限挂起；具体的限速及早失败逻辑由
				// updater.NewHTTPClient 内部的 speedGuardTransport 负责，这里只是其上限保障。
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
				// alpha.98 不会在 DownloadAndInstall 成功后 emit EventUpdateReady，手动补发。
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

		// user:background — 仅隐藏窗口，保留下载进度和事件监听
		g.subscribeUpdateEvent("wails:updater:user:background", func(_ *application.CustomEvent) {
			application.InvokeAsync(func() {
				if g.updateWin != nil {
					g.updateWin.Hide()
				}
			})
		})

		// user:remind 只隐藏（非下载阶段的"稍后提醒"，逻辑相同）
		g.subscribeUpdateEvent(wupdater.EventUserRemind, func(_ *application.CustomEvent) {
			application.InvokeAsync(func() {
				if g.updateWin != nil {
					g.updateWin.Hide()
				}
			})
		})

		// user:cancel：真正关闭
		g.subscribeUpdateEvent(wupdater.EventUserCancel, func(_ *application.CustomEvent) {
			g.closeUpdateWindow()
		})

		win.RegisterHook(events.Common.WindowClosing, func(_ *application.WindowEvent) {
			g.teardownUpdateWindow()
		})

		// 首次打开时直接回放一次（部分 webview 在极快加载时可能错过
		// window:ready 的首发事件）。
		g.emitUpdateSnapshot(rel)
		win.Show()
	})
}

// subscribeUpdateEvent 注册一个全局 wails 事件监听，并将其取消函数记录到
// g.updateWinUnsubs，随更新窗口关闭统一清理，避免重复打开窗口时监听器泄漏。
func (g *GuiApp) subscribeUpdateEvent(name string, fn func(*application.CustomEvent)) {
	wailsApp := application.Get()
	if wailsApp == nil {
		return
	}
	off := wailsApp.Event.On(name, fn)
	g.updateWinUnsubs = append(g.updateWinUnsubs, off)
}

// emitUpdateSnapshot 向更新窗口回放"发现新版本"状态：先发送 EventMeta
// （携带当前版本号），再发送 EventUpdateAvailable（携带新版本信息和发布说明）。
// 自定义更新窗口（updater_window.html）据此渲染版本号、发布说明和操作按钮。
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

// closeUpdateWindow 关闭自定义更新窗口；实际的监听器清理由
// WindowClosing 钩子中的 teardownUpdateWindow 完成。
func (g *GuiApp) closeUpdateWindow() {
	if g.updateWin != nil {
		g.updateWin.Close()
	}
}

// teardownUpdateWindow 取消所有为本次更新窗口注册的事件监听，并清空引用，
// 以便下次 CheckForUpdates 重新打开一个干净的窗口。
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

	// 元信息展示字段
	PublishDate string `json:"publishDate"` // 发布日期
	Platform    string `json:"platform"`    // 平台 (如 windows, darwin, linux)
	Arch        string `json:"arch"`        // 架构 (如 amd64, arm64)
	Filetype    string `json:"filetype"`    // 文件类型 (如 .exe, .dmg)
	AssetSize   string `json:"assetSize"`   // 格式化后的文件大小 (如 12.5 MB)
}

// GetUpdateInfo 通过 Wails 内置更新器查询更新状态，返回给前端。
func (g *GuiApp) GetUpdateInfo() UpdateInfo {
	if g.updaterApp == nil {
		return UpdateInfo{Error: "更新器未初始化"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), updater.CheckTimeout)
	defer cancel()

	// 1. 直接调用 Wails 的 Updater，自动享受代理加速！
	rel, err := g.updaterApp.Updater.Check(ctx)
	if err != nil {
		return UpdateInfo{Error: "检查更新失败: " + err.Error()}
	}

	current := GuiVersion
	if current == "" || current == "dev" {
		current = "0.0.0"
	}

	// 2. 如果 rel 为 nil，说明已经是最新版本
	if rel == nil {
		return UpdateInfo{
			HasUpdate:      false,
			CurrentVersion: current,
		}
	}

	// 3. Wails 引擎返回的 rel.Version 可能没有 v 前缀，我们补上以匹配 GitHub Tag
	tagName := rel.Version
	if !strings.HasPrefix(tagName, "v") {
		tagName = "v" + tagName
	}

	// 拼出 Release 页面链接
	targetURL := fmt.Sprintf("https://github.com/sinspired/subs-free/releases/tag/%s", tagName)

	// 计算文件大小 (MB)
	sizeMB := float64(rel.Artifact.Size) / (1024 * 1024)
	sizeStr := fmt.Sprintf("%.2f MB", sizeMB)

	// 格式化发布时间 (只取日期部分，或者加时间也可以)
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
		// 赋值新增字段
		PublishDate: pubDate,
		Platform:    rel.Artifact.Platform,
		Arch:        rel.Artifact.Arch,
		Filetype:    rel.Artifact.Filetype,
		AssetSize:   sizeStr,
	}
}

// OpenFilesWindow 打开或聚焦「内置文件」独立窗口（单例模式）。
// 通过 /gui/enter nonce 中转，自动完成 API Key 写入，无需用户手动登录。
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
			Title:          "Subs Check Pro — 内置文件",
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
// 通过 /gui/enter nonce 中转，自动完成 API Key 写入，无需用户手动登录。
// OpenAnalysisWindow 打开或聚焦「分析报告」独立窗口（单例模式）。
// 通过 /gui/enter nonce 中转，自动完成 API Key 写入，无需用户手动登录。
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
			Title:          "Subs Check Pro — 分析报告",
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

		// 动态计算窗口高度：Mac 因为红绿灯预留了 padding，需增加 18px 高度
		winHeight := 545
		minHeight := 545
		maxHeight := 560

		if runtime.GOOS == "darwin" {
			winHeight = 560
			minHeight = 560
			maxHeight = 575
		}

		// 根据网卡数量动态调整宽度
		ips := getLocalIPv4s()
		baseWidth := 520

		// 每个 IP 增加一定宽度，比如 120px
		Width := baseWidth + (len(ips)-2)*120
		MinWidth := Width - 0
		maxWidth := Width + 20

		win := wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:           "sub-links",
			Title:          "Subs Check Pro — 订阅链接",
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
	// 防止端口冲突未解决时（内核还未启动）前端轮询导致的空指针 Panic
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
