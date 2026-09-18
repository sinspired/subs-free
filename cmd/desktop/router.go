package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sinspired/subs-check-pro/v3/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// wailsOrigins 是 Wails webview 在各平台发出请求时携带的 Origin 头。
// WebView2 (Windows): http://wails.localhost
// WebKitGTK (Linux):  wails://
// WKWebView (macOS):  wails://
var wailsOrigins = map[string]bool{
	"http://wails.localhost": true,
	"wails://":               true,
}

// guiCORSMiddleware 为 Wails webview 的跨域请求添加必要的 CORS 响应头。
// 仅允许已知的 Wails origin，不对外开放。
func guiCORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if wailsOrigins[origin] {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")
			c.Header("Access-Control-Max-Age", "86400")
			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
		}
		c.Next()
	}
}

// registerGuiRoutes 注册所有 /gui/* 辅助路由，并补全 CORS 中间件。
// 统一入口，便于在 setup.go 和 CompleteInit 中复用。
func registerGuiRoutes(router *gin.Engine) {
	if router == nil {
		slog.Warn("HTTP 服务未启动（端口冲突），跳过 /gui/* 路由注册")
		return
	}
	router.Use(guiCORSMiddleware())
	router.GET("/gui/enter", handleGuiEnter)
	router.GET("/gui/popup", handleGuiPopup)
	router.GET("/gui/back-to-login", handleGuiBackToLogin)
	router.GET("/gui/open-about", handleGuiOpenAbout)
	router.GET("/gui/open-files", handleGuiOpenFiles)
	router.GET("/gui/open-analysis", handleGuiOpenAnalysis)
	router.GET("/gui/open-sub-links", handleGuiOpenSubLinks)
	router.GET("/gui/open-sub-store", handleGuiOpenSubStore)
	router.GET("/gui/check-update", handleGuiCheckUpdate)
}

// handleGuiEnter 一次性自动登录中转路由：验证 nonce → 写 session → 跳转 /admin。
func handleGuiEnter(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}

	nonce := c.Query("n")
	if nonce == "" {
		c.String(http.StatusBadRequest, "missing nonce")
		return
	}

	apiKey, remember, ok := consumeNonce(nonce)
	if !ok {
		c.String(http.StatusForbidden, "invalid or expired nonce")
		return
	}
	if apiKey != config.GlobalConfig.APIKey {
		c.String(http.StatusForbidden, "key mismatch")
		return
	}

	// 只允许以 "/" 开头的站内路径，防止开放重定向。
	redirect := c.Query("redirect")
	if redirect == "" || !strings.HasPrefix(redirect, "/") {
		redirect = "/admin"
	}

	// 在这里提前给 Wails WebView 种下 Cookie
	// 这样下一步 JS 执行 window.location.replace(redirect) 时，
	// 请求就会带上 Cookie，直接通过 pageAuthMiddleware 鉴权，而不会被 302 拦截到 /login
	c.SetCookie("scp_api_key", apiKey, 2592000, "/", "", false, false)

	c.Header("Cache-Control", "no-store, no-cache")
	c.Header("Content-Type", "text/html; charset=utf-8")

	var extraLS string
	if remember {
		extraLS = fmt.Sprintf(
			"try { localStorage.setItem('scp_api_key', %q); } catch(e) {}",
			apiKey,
		)
	}

	// 写入 storage key。
	c.String(http.StatusOK, fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="UTF-8">
<script>
(function(){
  try { sessionStorage.setItem('scp_api_key', %q); } catch(e) {}
  %s
  window.location.replace(%q);
})();
</script>
</head><body></body></html>`, apiKey, extraLS, redirect))
}

// handleGuiPopup 接收来自 WebUI 注入脚本的"新窗口"请求。
// 调用方：webUIWin 内注入的 JS 通过 fetch('/gui/popup?url=...&size=small') 触发。
func handleGuiPopup(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}

	rawURL := c.Query("url")
	if rawURL == "" {
		c.String(http.StatusBadRequest, "missing url")
		return
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		c.String(http.StatusBadRequest, "invalid url scheme")
		return
	}

	// 内部页面：通过 /gui/enter 中转自动写入 sessionStorage。
	listenPort := defaultListenPort()
	internalBase := "http://127.0.0.1:" + listenPort
	if strings.HasPrefix(rawURL, internalBase+"/") || rawURL == internalBase {
		internalPath := strings.TrimPrefix(rawURL, internalBase)
		if internalPath == "" {
			internalPath = "/"
		}
		nonce := generateNonce(config.GlobalConfig.APIKey, false)
		rawURL = internalBase + "/gui/enter?n=" + nonce + "&redirect=" + url.QueryEscape(internalPath)
	}

	size := c.Query("size")

	// 先返回 200，让 JS fetch 立即结束，不阻塞页面
	c.String(http.StatusOK, "ok")

	wailsApp := application.Get()
	if wailsApp == nil {
		slog.Error("/gui/popup: application.Get() returned nil")
		return
	}

	capturedURL := rawURL
	slog.Debug("/gui/popup: invoking popup", "url", capturedURL)
	application.InvokeAsync(func() {
		opts := newPopupOptions("Subs Check Pro", "/loading.html#"+capturedURL, size)
		slog.Debug("/gui/popup: inside InvokeAsync, creating window")
		popup := wailsApp.Window.NewWithOptions(opts)
		popup.Show()
		popup.Center()
		popup.Focus()

		finalURL := capturedURL
		time.AfterFunc(300*time.Millisecond, func() {
			application.InvokeAsync(func() {
				popup.SetURL(finalURL)
			})
		})
	})
}

// handleGuiBackToLogin 接收来自 WebUI 的"返回登录窗口"请求。
func handleGuiBackToLogin(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	c.String(http.StatusOK, "ok")
	if globalGuiApp != nil {
		application.InvokeAsync(func() {
			globalGuiApp.BackToLogin()
		})
	}
}

// isLoopback 检查请求是否来自本机回环地址。
func isLoopback(c *gin.Context) bool {
	remoteIP, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(remoteIP)
	return ip != nil && ip.IsLoopback()
}

// handleGuiOpenAbout 在 Go 侧调用 OpenAboutWindow()，在 Wails 主线程打开「关于」窗口。
func handleGuiOpenAbout(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	c.String(http.StatusOK, "ok")
	if globalGuiApp != nil {
		application.InvokeAsync(func() {
			globalGuiApp.OpenAboutWindow()
		})
	}
}

// handleGuiOpenFiles 打开「内置文件」独立窗口（单例）。
func handleGuiOpenFiles(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	c.String(http.StatusOK, "ok")
	if globalGuiApp != nil {
		application.InvokeAsync(func() {
			globalGuiApp.OpenFilesWindow()
		})
	}
}

// handleGuiOpenAnalysis 打开「分析报告」独立窗口（单例）。
func handleGuiOpenAnalysis(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	c.String(http.StatusOK, "ok")
	if globalGuiApp != nil {
		application.InvokeAsync(func() {
			globalGuiApp.OpenAnalysisWindow()
		})
	}
}

// handleGuiOpenSubLinks 打开「订阅链接」独立窗口（单例）。
func handleGuiOpenSubLinks(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	c.String(http.StatusOK, "ok")
	if globalGuiApp != nil {
		application.InvokeAsync(func() {
			globalGuiApp.OpenSubLinksWindow()
		})
	}
}

// handleGuiOpenSubStore 打开 Sub-Store 订阅管理独立窗口（单例）。
func handleGuiOpenSubStore(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	c.String(http.StatusOK, "ok")
	if globalGuiApp != nil {
		application.InvokeAsync(func() {
			globalGuiApp.OpenSubStoreWindow()
		})
	}
}

// handleGuiCheckUpdate 在 Go 侧调用 CheckForUpdates()，触发 Wails 更新流程。
func handleGuiCheckUpdate(c *gin.Context) {
	if !isLoopback(c) {
		c.String(http.StatusForbidden, "forbidden")
		return
	}
	c.String(http.StatusOK, "ok")
	if globalGuiApp != nil {
		go globalGuiApp.CheckForUpdates()
	}
}
