package main

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	coreapp "github.com/sinspired/subs-check-pro/v3/app"
	"github.com/sinspired/subs-check-pro/v3/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

var globalGuiApp *GuiApp

// globalApp 保存 Wails application 实例，供后台 goroutine 发送全局事件。
var globalApp *application.App

type GuiApp struct {
	configPath   string
	backend      *coreapp.App
	mainWindow   *application.WebviewWindow
	isFirstRun   bool
	backendReady bool
	initErr      string

	// keyManualOverride 记录本次进程运行期间用户是否已经通过 GUI 手动设置过
	// API Key。GUI_KEY_IS_RANDOM 环境变量只在进程启动时探测一次（core 首次
	// 加载配置、发现 api-key 为空时自动生成随机值才会设置），运行期间不会
	// 自动刷新——所以即使用户随后把 api-key 改成了自定义值，光凭这个环境变量
	// 判断 KeyIsRandom 会一直显示"仍是随机密钥"，直到重启进程为止。
	// 这个字段用来让 GetAppInfo 在本次运行内也能立刻反映出"已手动设置"。
	keyManualOverride bool
}

type AppInfo struct {
	APIKey           string `json:"apiKey"`
	ListenPort       string `json:"listenPort"`
	SubStorePort     string `json:"subStorePort"`
	SubStorePath     string `json:"subStorePath"`
	SingBoxOldVer    string `json:"singBoxOldVer"`
	SingBoxLatestVer string `json:"singBoxLatestVer"`
	KeyIsRandom      bool   `json:"keyIsRandom"`
	IsFirstRun       bool   `json:"isFirstRun"`
	ConfigPath       string `json:"configPath"`
	PendingInit      bool   `json:"pendingInit"`
	InitErr          string `json:"initErr"`

	// GuiVersion 桌面客户端版本（ldflags 注入，如 "v1.2.0"）
	GuiVersion string `json:"guiVersion"`

	// CoreVersion 内核版本+短提交哈希（如 "v2.5.4@7c23868"）
	CoreVersion string `json:"coreVersion"`
}

type CheckState struct {
	IsChecking bool   `json:"isChecking"`
	StepName   string `json:"stepName"`
	Available  int64  `json:"available"`
	Progress   int64  `json:"progress"`
	ProxyCount int64  `json:"proxyCount"`
	LastResult string `json:"lastResult"`
}

type PublicInfo struct {
	ListenPort   string `json:"listenPort"`
	SubStorePort string `json:"subStorePort"`
	SubStorePath string `json:"subStorePath"`
	GuiVersion   string `json:"guiVersion"`
	CoreVersion  string `json:"coreVersion"`
}

type SafeArea struct {
	Top    int `json:"top"`
	Bottom int `json:"bottom"`
	Left   int `json:"left"`
	Right  int `json:"right"`
}

func (g *GuiApp) GetAppInfo() AppInfo {
	port := strings.TrimPrefix(config.GlobalConfig.ListenPort, ":")
	if port == "" {
		port = "8199"
	}

	subPort := strings.TrimPrefix(config.GlobalConfig.SubStorePort, ":")
	subPath := "/" + strings.TrimPrefix(config.GlobalConfig.SubStorePath, "/")

	singBoxLatestVer := config.GlobalConfig.SingboxLatest.Version
	singBoxOldVer := config.GlobalConfig.SingboxOld.Version

	coreVer := Version

	return AppInfo{
		APIKey:           config.GlobalConfig.APIKey,
		ListenPort:       port,
		SubStorePort:     subPort,
		SubStorePath:     subPath,
		SingBoxOldVer:    singBoxOldVer,
		SingBoxLatestVer: singBoxLatestVer,
		KeyIsRandom:      !g.keyManualOverride && os.Getenv("GUI_KEY_IS_RANDOM") == "1",
		IsFirstRun:       g.isFirstRun,
		ConfigPath:       g.configPath,
		PendingInit:      !g.backendReady,
		InitErr:          g.initErr,
		GuiVersion:       GuiVersion,
		CoreVersion:      coreVer,
	}
}

func (g *GuiApp) GetCheckState() CheckState {
	if !g.backendReady || g.backend == nil {
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
		Available:  st.Available,
		Progress:   st.Progress,
		ProxyCount: st.ProxyCount,
		LastResult: g.backend.GetLastCheckResult(),
	}
}

func (g *GuiApp) GetPublicInfo() PublicInfo {
	port := strings.TrimPrefix(config.GlobalConfig.ListenPort, ":")
	if port == "" {
		port = "8199"
	}

	subPort := strings.TrimPrefix(config.GlobalConfig.SubStorePort, ":")
	subPath := "/" + strings.TrimPrefix(config.GlobalConfig.SubStorePath, "/")

	return PublicInfo{
		ListenPort:   port,
		SubStorePort: subPort,
		SubStorePath: subPath,
		GuiVersion:   GuiVersion,
		CoreVersion:  Version,
	}
}

// SaveConfig 保留作为向后兼容的绑定入口（例如首页如果还想直接调用它）。
// 内部不再是裸的 os.WriteFile，而是改为走 APIProxy 调用真正的 /api/config
// 处理逻辑——这样保存配置时才会附带触发 Sub-Store 后台同步等完整副作用，
// 和桌面端 web 面板走 POST /api/config 的行为完全一致。
// webui 的 admin.js 建议直接调用 window.WailsBridge.APIProxy("POST","/api/config",...)，
// 这个方法留给可能还会用到简化调用形式的地方。
func (g *GuiApp) SaveConfig(content string) string {
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return "YAML 格式或解析错误: " + err.Error()
	}

	body, _ := json.Marshal(map[string]string{
		"content": content,
	})

	raw := g.APIProxy("POST", "/api/config", string(body))

	var res struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}

	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return "保存失败：响应解析出错 " + err.Error()
	}

	if res.Status >= 300 {
		return "保存失败: " + res.Body
	}

	return ""
}

// APIProxy 是安卓端保存配置等所有 POST 类操作的核心解决方案：
// 在应用进程内直接把请求灌入内核的 gin 路由器处理，完全不经过任何 socket/网络 I/O。
//
// 根因：Android 的 WebResourceRequest API 从未暴露过 POST 请求体（Chromium 遗留问题，
// 官方明确不会修复），导致任何经由 shouldInterceptRequest 拦截转发的 POST 请求，
// 请求体到达 Go 侧时必然是空的（EOF）——这与"Windows/Linux/macOS 均正常，唯独安卓不行"
// 的现象完全吻合，因为那几个平台的原生 WebView（WebView2/WebKitGTK/WKWebView）拦截层
// 能拿到请求体，只有 Android 拿不到。
//
// 通过 Wails 原生绑定（$Call.ByID，即生成的 bindings）调用本方法时，method/path/body
// 是作为普通字符串参数经由 JS↔Go 原生桥直接传递的，从始至终都不是一次被拦截的网络请求，
// 因此不受此限制影响，安卓上也能拿到完整的 body。
//
// 返回值统一为 {"status": <int>, "body": <string>} 的 JSON 字符串，前端 sfetch 按普通
// fetch 响应的方式解析即可。
func (g *GuiApp) APIProxy(method, path, body string) string {
	type apiResult struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}

	fail := func(status int, msg string) string {
		b, _ := json.Marshal(apiResult{
			Status: status,
			Body:   `{"error":"` + msg + `"}`,
		})
		return string(b)
	}

	if g.backend == nil || !g.backendReady {
		return fail(503, "内核尚未就绪")
	}

	// 测试 Wails v3 全局事件发送。
	// 注意：Wails v3 使用 application.App.Event.Emit，而不是 App.EmitEvent。
	if path == "/api/status" &&
		globalGuiApp != nil &&
		globalGuiApp.mainWindow != nil {

		if globalApp != nil {
			globalApp.Event.Emit(
				"ApiProxy",
				"当前正在请求: "+path,
			)
		}
	}

	router := g.backend.GetRouter()
	if router == nil {
		return fail(503, "路由器尚未初始化")
	}

	req := httptest.NewRequest(
		strings.ToUpper(method),
		path,
		strings.NewReader(body),
	)

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

func (g *GuiApp) BackToHome() {
	if g.mainWindow == nil {
		return
	}

	slog.Info("返回首页")

	application.InvokeAsync(func() {
		g.mainWindow.ExecJS("window.location.href = '/';")
	})
}

// OpenInBrowser 调用 Wails 3 Mobile API 在系统浏览器中打开外部链接。
func (g *GuiApp) OpenInBrowser(url string) {
	application.Mobile.OpenURL(url)
}

// ShareLink 调起系统原生分享面板（Android 分享 Intent / iOS
// UIActivityViewController），把一段文本/链接分享给手机上安装的其他 App——
// 比如把订阅链接分享到 IM、笔记，或是能接收订阅链接的其他代理客户端。
// 用的是 application.Mobile.Share，跨 iOS/Android 通用；桌面端是空操作，
// 不需要额外判断平台。
func (g *GuiApp) ShareLink(text, url string) {
	payload, err := json.Marshal(map[string]string{
		"text": text,
		"url":  url,
	})
	if err != nil {
		slog.Error("分享内容 JSON 构建失败", "error", err)
		return
	}
	application.Mobile.Share(string(payload))
}

// SetStatusBarAppearance 根据当前是否为深色主题，同步系统状态栏的图标颜色与背景色。
func (g *GuiApp) SetStatusBarAppearance(isDark bool) {
	// 浅色背景需要深色图标 (style: dark)，深色背景需要浅色图标 (style: light)。
	style := "dark"
	bgColor := "#f2f4f6"

	if isDark {
		style = "light"
		bgColor = "#111317"
	}

	payload, _ := json.Marshal(map[string]any{
		"style":  style,
		"hidden": false,
		"color":  bgColor, // 显式指定状态栏底色，强制覆盖系统默认残留的背景色
	})

	application.Mobile.SetStatusBar(string(payload))
}

func (g *GuiApp) GetSafeArea() SafeArea {
	// 系统自动返回安全区 JSON
	safeAreaJSON := application.Mobile.SafeAreaJSON()

	var safeArea SafeArea
	if err := json.Unmarshal([]byte(safeAreaJSON), &safeArea); err != nil {
		slog.Info("解析 SafeAreaJSON 出错", "error", err)
		return SafeArea{}
	}

	return safeArea
}

// MarkAPIKeyManual 由前端在手动编辑 API Key 并保存成功后调用，标记本次
// 运行期间密钥已不再是随机生成的。GUI_KEY_IS_RANDOM 环境变量只在进程启动
// 时探测一次，不会随配置文件的变化而自动刷新，所以这一步是必要的——否则
// "随机密钥"提示图标会一直显示，直到应用重启为止。
func (g *GuiApp) MarkAPIKeyManual() {
	g.keyManualOverride = true
}

// SetKeepAwake 检测运行期间保持屏幕常亮，避免手机自动锁屏后检测任务被系统
// 限流甚至暂停。注意：这只在应用处于前台可见时有效；真正的"切到后台也保证
// 跑完"由 service_android.go 的 StartForegroundService/StopForegroundService
// 负责（Android 专属能力，非安卓平台见 service_other.go 的空实现）。
func (g *GuiApp) SetKeepAwake(enabled bool) {
	application.Mobile.SetKeepAwake(enabled)
}
