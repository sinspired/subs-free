package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/sinspired/subs-check-pro-webui/webui"
	"github.com/sinspired/subs-check-pro/v3/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

var (
	tmplCache   *template.Template
	tmplOnce    sync.Once
	reSwScript  = regexp.MustCompile(`<script src="/static/js/reg-sw\.js\?+[^"]*"></script>`)
	reGuiImport = regexp.MustCompile(`(?s)<script type="module">.*?import\s*\{\s*GuiApp\s*\}.*?</script>`)
)

// stripMobileAuthSuffix 识别形如 "/admin~<key>" 等特殊路径
func stripMobileAuthSuffix(p string) string {
	lastSlash := strings.LastIndexByte(p, '/')
	if lastSlash < 0 {
		return p
	}
	seg := p[lastSlash+1:]
	if before, _, ok := strings.Cut(seg, "~"); ok {
		return p[:lastSlash+1] + before
	}
	return p
}

func resolveAPIHost(listenPort string) string {
	port := strings.TrimSpace(listenPort)
	if port == "" {
		return "127.0.0.1:8199"
	}
	if strings.HasPrefix(port, ":") {
		return "127.0.0.1" + port
	}
	if _, _, err := net.SplitHostPort(port); err == nil {
		return port
	}
	return "127.0.0.1:" + port
}

func createSubStoreProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			port := strings.TrimPrefix(config.GlobalConfig.SubStorePort, ":")
			req.URL.Scheme = "http"
			req.URL.Host = "127.0.0.1:" + port

			p := req.URL.Path
			subPath := strings.TrimSuffix(strings.TrimSpace(config.GlobalConfig.SubStorePath), "/")

			targetPath := p
			if strings.HasPrefix(p, "/substore") {
				targetPath = strings.TrimPrefix(p, "/substore")
			} else if subPath != "" && strings.HasPrefix(p, subPath) {
				targetPath = strings.TrimPrefix(p, subPath)
			}
			if targetPath == "" {
				targetPath = "/"
			}
			req.URL.Path = targetPath

			// 禁用 Sub-store 响应 gzip 压缩，以便我们能成功 bytes.ReplaceAll 替换 HTML 内容
			req.Header.Del("Accept-Encoding")
		},
		ModifyResponse: func(resp *http.Response) error {
			if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return err
				}
				body = bytes.ReplaceAll(body, []byte(`src="/`), []byte(`src="/substore/`))
				body = bytes.ReplaceAll(body, []byte(`href="/`), []byte(`href="/substore/`))

				resp.Body = io.NopCloser(bytes.NewReader(body))
				resp.ContentLength = int64(len(body))
				resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "Sub-Store 未就绪: "+err.Error(), http.StatusBadGateway)
		},
	}
}

func createAPIProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = resolveAPIHost(config.GlobalConfig.ListenPort)
			if strings.HasPrefix(req.URL.Path, "/api/") {
				req.Header.Set("X-API-Key", config.GlobalConfig.APIKey)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "后端 API 未就绪: "+err.Error(), http.StatusBadGateway)
		},
	}
}

func buildAssetHandler(frontendFS fs.FS, getConfigPath func() string) http.Handler {
	guiServer := application.AssetFileServerFS(frontendFS)
	webuiStaticSubFS, _ := fs.Sub(webui.StaticFS, "static")
	staticServer := http.StripPrefix("/static/", http.FileServerFS(webuiStaticSubFS))

	subStoreProxy := createSubStoreProxy()
	apiProxy := createAPIProxy()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 在入口处直接还原真实路径，减少后续各个判断模块心智负担
		r.URL.Path = stripMobileAuthSuffix(r.URL.Path)
		p := r.URL.Path

		// 1. Sub-Store Proxy
		subPath := strings.TrimSuffix(strings.TrimSpace(config.GlobalConfig.SubStorePath), "/")
		isSubStore := strings.HasPrefix(p, "/substore/") || p == "/substore" ||
			(subPath != "" && (p == subPath || strings.HasPrefix(p, subPath+"/")))
		if isSubStore {
			if config.GlobalConfig.SubStorePort == "" {
				http.Error(w, "Sub-Store 未配置", http.StatusNotFound)
				return
			}
			subStoreProxy.ServeHTTP(w, r)
			return
		}

		// 2. 静态资源
		if strings.HasPrefix(p, "/static/") {
			staticServer.ServeHTTP(w, r)
			return
		}

		// 3. 后端 API 反向代理
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/gui/") ||
			strings.HasPrefix(p, "/admin/version") || strings.HasPrefix(p, "/admin/theme") ||
			strings.HasPrefix(p, "/sub/") || p == "/sub" ||
			strings.HasPrefix(p, "/share/") || p == "/share" ||
			strings.HasPrefix(p, "/more/") || p == "/more" {
			apiProxy.ServeHTTP(w, r)
			return
		}

		// 4. WebUI 页面（渲染模板并注入脚本）—— 同时兼容不带 .html 和带 .html 两种写法
		if p == "/admin" || p == "/admin.html" ||
			p == "/analysis" || p == "/analysis.html" ||
			p == "/files" || p == "/files.html" ||
			p == "/share" || p == "/share.html" {
			handleWebUITemplate(w, p, getConfigPath())
			return
		}

		// 5. 阻断移动端进入 WebUI 登录页，避免引发重定向死循环
		if p == "/login" || p == "/login.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<script>window.location.href='/'</script>`))
			return
		}

		// 6. GUI (Wails) 前端接管
		guiServer.ServeHTTP(w, r)
	})
}

func handleWebUITemplate(w http.ResponseWriter, path string, configPath string) {
	// 模板只解析一次，避免每次请求都读取 FS 并进行高成本的正则替换
	tmplOnce.Do(func() {
		tmplCache = template.New("")
		subFS, err := fs.Sub(webui.TemplatesFS, "templates")
		if err != nil {
			return
		}
		_ = fs.WalkDir(subFS, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
				return nil
			}
			b, _ := fs.ReadFile(subFS, p)
			s := string(b)
			// 清理 PWA 注册与 Wails 内置导入
			s = reSwScript.ReplaceAllString(s, "")
			if p == "admin.html" {
				s = reGuiImport.ReplaceAllString(s, "")
			}
			tmplCache.New(p).Parse(s)
			return nil
		})
	})

	tmplName := "admin.html"
	switch path {
	case "/analysis", "/analysis.html":
		tmplName = "analysis.html"
	case "/files", "/files.html":
		tmplName = "files.html"
	case "/share", "/share.html":
		tmplName = "share.html"
	}

	if tmplCache == nil || tmplCache.Lookup(tmplName) == nil {
		http.Error(w, "Template not found", http.StatusNotFound)
		return
	}

	port := strings.TrimPrefix(config.GlobalConfig.ListenPort, ":")
	if port == "" {
		port = "8199"
	}
	apiKey := config.GlobalConfig.APIKey

	data := map[string]any{
		"configPath":      configPath,
		"apiKey":          apiKey,
		"listenPort":      port,
		"wailsGUI":        true,
		"wailsAndroidGUI": true,
	}

	var buf bytes.Buffer
	if err := tmplCache.ExecuteTemplate(&buf, tmplName, data); err != nil {
		http.Error(w, "Template render error", http.StatusInternalServerError)
		return
	}

	// 注入并替换
	keyJSON := strconv.Quote(apiKey)
	portJSON := strconv.Quote(port)
	// wails-bridge.js 是单独打包的 IIFE（见 frontend/vite.bridge.config.ts），
	// 把 Wails 生成的绑定函数（目前是 APIProxy）挂到 window.WailsBridge 上。
	// admin.html/analysis.html/files.html 是纯静态页面，没有走 Vite 打包、无法
	// 直接 import 绑定，所以在这里用普通 <script src> 引入。必须放在内联脚本之前，
	// 保证 window.WailsBridge 在 admin.js 等页面脚本执行前已经就绪。
	bridgeScript := `<script src="/wails-bridge.js"></script>`
	guiScript := fmt.Sprintf(`<script>(()=>{const k=%s;window.__WAILS_GUI={apiKey:k,listenPort:%s,baseURL:"http://127.0.0.1:%s"};try{sessionStorage.setItem('subscheck_api_key',k)}catch(e){}})();</script>`, keyJSON, portJSON, port)
	guiScript = bridgeScript + guiScript

	html := strings.Replace(buf.String(), "</head>", guiScript+"</head>", 1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(html)))
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, html)
}
