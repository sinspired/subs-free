package main

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/sinspired/subs-check-pro-webui/webui"
	"github.com/sinspired/subs-check-pro/v3/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// newCombinedAssetHandler 创建合并资产处理器，供 Wails AssetOptions.Handler 使用。
//
// 路由规则：
//   - /webui/static/… → 从 webui.StaticFS 提供（CSS、JS、图标等）
//   - /webui/admin.html 或 /webui/admin → 渲染 admin.html Go 模板
//   - /webui/… 其他  → 从 webui.TemplatesFS 直接提供（未来扩展用）
//   - 其余路径        → 转给 frontend/dist 处理器（React 登录页）
//
// 这样 loginWin 继续使用 frontend/dist，webUIWin 使用 /webui/* 路径，
// 两个窗口共用同一个 Wails 资产服务器，无需各自独立的 HTTP 服务。
// ⚠️ getListenPort 必须是函数引用而非字符串值：
// Wails 应用对象在 main() 中仅初始化一次，此时若端口冲突则后端尚未启动，
// 真正的端口在 CompleteInit() 后才写入 config.GlobalConfig。
// 传函数引用可确保每次请求动态读取最新端口。
func newCombinedAssetHandler(getConfigPath func() string, getPort func() string) http.Handler {
	// React 登录前端（frontend/dist embed.FS，由 main.go 的 //go:embed 注入）
	frontendHandler := application.AssetFileServerFS(assets)

	// webui 静态文件子 FS：去掉 "static/" 前缀后直接暴露 css/、js/、icon/ 等目录
	staticSubFS, err := fs.Sub(webui.StaticFS, "static")
	if err != nil {
		// 不应发生——embed 路径在编译时已确认
		panic("webui: cannot sub StaticFS: " + err.Error())
	}
	staticHandler := http.FileServerFS(staticSubFS)

	// webui 模板子 FS：去掉 "templates/" 前缀后文件直接可寻址（admin.html 等）
	templatesSubFS, err := fs.Sub(webui.TemplatesFS, "templates")
	if err != nil {
		panic("webui: cannot sub TemplatesFS: " + err.Error())
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path

		switch {
		// ── /webui/static/… ─────────────────────────────────────────────────
		// 例：/webui/static/css/admin.css → staticSubFS 中的 /css/admin.css
		case strings.HasPrefix(p, "/webui/static/"):
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(p, "/webui/static")
			if r2.URL.Path == "" {
				r2.URL.Path = "/"
			}
			staticHandler.ServeHTTP(w, r2)

		// ── /webui/admin.html 或 /webui/admin ───────────────────────────────
		// 作为 Go 模板渲染，注入 configPath 等动态数据
		case p == "/webui/admin.html" || p == "/webui/admin":
			renderWebuiAdmin(w, templatesSubFS, getConfigPath())

		// ── /webui/… 其他路径（预留）───────────────────────────────────────
		// 例如将来可能添加的 /webui/analysis.html（静态读取，无模板变量）
		case strings.HasPrefix(p, "/webui/"):
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(p, "/webui")
			if r2.URL.Path == "" {
				r2.URL.Path = "/"
			}
			http.FileServerFS(templatesSubFS).ServeHTTP(w, r2)

		// ── /api/… /admin/… ────────────────────────────────────────────────
		// 反向代理到 Gin HTTP 服务，绕开 Wails webview 的跨域限制。
		// 请求由 Go 服务器端发出，不存在 CORS 问题。
		case strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/admin/") || strings.HasPrefix(p, "/gui/"):
			// 每次请求调用 getListenPort() 动态获取，确保端口修改后立即生效
			reverseProxyToGin(w, r, getPort())

		// ── /static/… ──────────────────────────────────────────────────────
		// admin.html 内所有资源引用均使用 /static/ 绝对路径（与 Gin 保持一致），
		// Wails 资产服务器收到这些请求时在此处拦截，转发给同一个 staticHandler。
		case strings.HasPrefix(p, "/static/"):
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(p, "/static")
			if r2.URL.Path == "" {
				r2.URL.Path = "/"
			}
			staticHandler.ServeHTTP(w, r2)

		// ── 默认：交给 React 前端处理器（loginWin 的资产）──────────────────
		default:
			frontendHandler.ServeHTTP(w, r)
		}
	})
}

// renderWebuiAdmin 将 admin.html 作为 Go 模板渲染并写入响应。
// 目前只有一个模板变量 .configPath，其余逻辑（APIKey、端口）由前端
// 通过 Wails binding 调用 GuiApp.GetAppInfo() / GuiApp.GetApiKey() 完成。
func renderWebuiAdmin(w http.ResponseWriter, templatesFS fs.FS, configPath string) {
	t, err := template.ParseFS(templatesFS, "admin.html")
	if err != nil {
		slog.Error("webui: 解析 admin.html 模板失败", "error", err)
		http.Error(w, "template parse error", http.StatusInternalServerError)
		return
	}

	port := defaultListenPort()

	data := map[string]any{
		"configPath": configPath,
		"apiKey":     config.GlobalConfig.APIKey,
		"listenPort": port,
		"wailsGUI":   true,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store")

	if err := t.Execute(w, data); err != nil {
		slog.Error("webui: 渲染 admin.html 失败", "error", err)
	}
}

// reverseProxyToGin 将请求透明转发给本机 Gin HTTP 服务（127.0.0.1:port），
// 用于让 Wails webview 内的页面访问 /api/* 等端点，绕开浏览器 CORS 限制。
func reverseProxyToGin(w http.ResponseWriter, r *http.Request, listenPort string) {
	target := fmt.Sprintf("http://127.0.0.1:%s%s", listenPort, r.URL.RequestURI())

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "proxy: build request failed", http.StatusBadGateway)
		return
	}
	// 透传请求头（X-API-Key 等）
	for k, vals := range r.Header {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	// 不转发 Origin/Referer，避免 Gin 侧产生多余日志
	req.Header.Del("Origin")
	req.Header.Del("Referer")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("proxy: gin request failed", "url", target, "error", err)
		http.Error(w, "proxy: upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 透传响应头
	for k, vals := range resp.Header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body) //nolint:errcheck
}
