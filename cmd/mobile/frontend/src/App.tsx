import { h, Fragment } from "preact";
import { useState, useEffect, useRef, useCallback } from "preact/hooks";
import { GuiApp } from "../bindings/github.com/sinspired/subs-free/cmd/mobile";

// --- Types ---
interface AppInfo {
  apiKey: string;
  listenPort: string;
  subStorePort: string;
  subStorePath: string;
  singBoxOldVer: string;
  singBoxLatestVer: string;
  keyIsRandom: boolean;
  isFirstRun: boolean;
  configPath: string;
  pendingInit: boolean;
  initErr: string;
  guiVersion: string;
  coreVersion: string;
}

interface CheckState {
  isChecking: boolean;
  stepName: string;
  available: number;
  progress: number;
  proxyCount: number;
  lastResult: string;
}

interface LastStats {
  time: string;
  duration: string;
  total: string;
  available: string;
  traffic: string;
}

export function App() {
  const [theme, setTheme] = useState<"light" | "dark">("light");

  // 从缓存读取最后一次的有效状态，防止热切回出现内容闪缩
  const [info, setInfo] = useState<AppInfo | null>(() => {
    try {
      const cached = sessionStorage.getItem("scp_info_cache") || localStorage.getItem("scp_info_cache");
      return cached ? JSON.parse(cached) : null;
    } catch {
      return null;
    }
  });

  const [loading, setLoading] = useState(() => {
    try {
      if (sessionStorage.getItem("scp_info_cache")) return false;
      const lastActive = localStorage.getItem("scp_last_active");
      // 如果短时间内切后台再切回来，初步认定为热恢复，初始不显示 loading
      if (lastActive && Date.now() - parseInt(lastActive) < 5 * 60 * 1000) {
        return false;
      }
    } catch { }
    return true;
  });

  const [status, setStatus] = useState<CheckState | null>(null);
  const [lastStats, setLastStats] = useState<LastStats | null>(null);
  const [errMsg, setErrMsg] = useState("");
  const [keyShown, setKeyShown] = useState(false);
  const [actionInFlight, setActionInFlight] = useState(false);
  const [finalizing, setFinalizing] = useState(false);

  // Bottom Sheets 与 Toast
  const [sheetSub, setSheetSub] = useState(false);
  const [sheetPath, setSheetPath] = useState(false);
  const [sheetAbout, setSheetAbout] = useState(false);
  const [toast, setToast] = useState<{ msg: string; type: "info" | "success" | "error"; visible: boolean }>({ msg: "", type: "info", visible: false });

  const pathRef = useRef<HTMLSpanElement>(null);
  const initTimerRef = useRef<number>();

  // 初始化与主题
  useEffect(() => {
    const saved = localStorage.getItem("scp-theme") as "light" | "dark" | null;
    const initialTheme = saved || (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
    setTheme(initialTheme);
    document.documentElement.setAttribute("data-theme", initialTheme);
    // 同步一次系统状态栏外观
    GuiApp.SetStatusBarAppearance(initialTheme === "dark");
    // 动态获取安全区
    const applySafeArea = async () => {
      try {
        const sa = await GuiApp.GetSafeArea();
        if (sa) {
          // 写入 CSS 变量，单位为 px
          document.documentElement.style.setProperty("--sa-top", `${sa.top}px`);
          document.documentElement.style.setProperty("--sa-bottom", `${sa.bottom}px`);
          document.documentElement.style.setProperty("--sa-left", `${sa.left}px`);
          document.documentElement.style.setProperty("--sa-right", `${sa.right}px`);
        }
      } catch (e) {
        console.warn("读取原生安全区失败:", e);
      }
    };
    applySafeArea();
    // 监听窗口大小变化（如手机横竖屏旋转）实时重新获取
    window.addEventListener("resize", applySafeArea);

    initApp();
    const interval = setInterval(pollStatus, 1000);

    // 当应用从后台切回前台时，主动验证一次内核是否存活！
    const handleVisibility = () => {
      if (document.visibilityState === "visible") {
        pollStatus(true);
      }
    };
    document.addEventListener("visibilitychange", handleVisibility);

    return () => {
      clearInterval(interval);
      window.clearTimeout(initTimerRef.current);
      window.removeEventListener("resize", applySafeArea);
      document.removeEventListener("visibilitychange", handleVisibility);
    };
  }, []);

  const toggleTheme = () => {
    const next = theme === "light" ? "dark" : "light";
    setTheme(next);
    document.documentElement.setAttribute("data-theme", next);
    localStorage.setItem("scp-theme", next);
    // 深色背景配深色图标（或反过来）会导致状态栏内容看不清。
    GuiApp.SetStatusBarAppearance(next === "dark");
  };

  const showToast = (msg: string, type: "info" | "success" | "error" = "info") => {
    setToast({ msg, type, visible: true });
    setTimeout(() => setToast(t => ({ ...t, visible: false })), 2000);
  };

  const initApp = async () => {
    try {
      const data = await GuiApp.GetAppInfo();
      if (data && data.apiKey) {
        localStorage.setItem("scp_api_key", data.apiKey);
        sessionStorage.setItem("scp_api_key", data.apiKey);
      }

      if (data.initErr) {
        setErrMsg(data.initErr);
        setLoading(false);
        return;
      }

      // 后端代码中 pendingInit 表示内核未就绪，强制拦截并转入 loading
      if (data.pendingInit) {
        setLoading(true);
        window.clearTimeout(initTimerRef.current);
        initTimerRef.current = window.setTimeout(initApp, 500);
        return;
      }

      setInfo(data);
      try {
        const infoStr = JSON.stringify(data);
        sessionStorage.setItem("scp_info_cache", infoStr);
        localStorage.setItem("scp_info_cache", infoStr);
        localStorage.setItem("scp_last_active", Date.now().toString());
      } catch { }

      setLoading(false);
      fetchLastCheckStats();
    } catch (err) {
      // 捕获到 Wails 桥异常时，一般说明进程/上下文断开，强制重启加载流程
      setErrMsg(String(err));
      setLoading(false);
    }
  };

  const sfetch = async (path: string, options: RequestInit = {}, apiKey?: string) => {
    const key = apiKey || info?.apiKey;
    if (!key) return { ok: false, error: "AppInfo not loaded" };
    try {
      const res = await fetch(path, { ...options, headers: { ...options.headers, "X-API-Key": key } });
      const text = await res.text();
      let payload;
      try { payload = JSON.parse(text); } catch { payload = text; }
      return { ok: res.ok, payload };
    } catch (e) {
      return { ok: false, error: e };
    }
  };

  // 通过 Wails 原生绑定（进程内直调 gin 路由）发起请求，完全不经过 WebView 的
  // 网络拦截层，避免 app.go 中 APIProxy 注释所述的安卓端网络时序问题。
  // 用于冷启动阶段等对时序敏感、且不方便做重试兜底的场景（如加载上次检测统计）。
  const apiProxyFetch = async (method: string, path: string, body = "") => {
    try {
      const raw = await GuiApp.APIProxy(method, path, body);
      const res = JSON.parse(raw) as { status: number; body: string };
      let payload: any;
      try { payload = JSON.parse(res.body); } catch { payload = res.body; }
      return { ok: res.status < 300, payload };
    } catch (e) {
      return { ok: false, error: e };
    }
  };

  // 轮询内核检测状态
  const pollStatus = async (isVisibilityResume = false) => {
    try {
      const newStatus = await GuiApp.GetCheckState();

      // 探针生效：发现后端传回“内核未就绪”，说明应用经历了深度休眠/热重载，
      // 立马撤销缓存页，执行完整的内核唤醒重连逻辑。
      if (newStatus.stepName === "内核未就绪") {
        setLoading(true);
        initApp();
        return;
      }

      // 处于前台且正常轮询时更新活跃时间
      try { localStorage.setItem("scp_last_active", Date.now().toString()); } catch { }

      if (isVisibilityResume) {
        initApp(); // 若是切回前台事件，静默同步一次 AppInfo 防旧配置
      }

      setStatus(prev => {
        if (!prev?.isChecking && newStatus.isChecking) {
          // 检测开始：保持屏幕常亮（前台生效），并启动前台服务 + 常驻通知
          // （安卓专属，非安卓平台是空操作），这样即使用户把应用切到后台，检测任务也不会被系统限流/杀掉。
          GuiApp.SetKeepAwake(true);
          GuiApp.StartForegroundService("正在检测节点", "检测任务运行中，请勿关闭应用");
        }
        if (prev?.isChecking && !newStatus.isChecking) {
          GuiApp.SetKeepAwake(false);
          GuiApp.StopForegroundService();
          showToast("检测任务已完成", "success");
          // 会先展示 status.lastResult 兜底文本，等新请求成功后才跳成统计卡片）。
          setFinalizing(true);
          fetchLastCheckStats().finally(() => setFinalizing(false));
        }
        return newStatus;
      });
    } catch (e) {
      // 若获取状态时报底层的异常(Wails断开)，切入重连逻辑
      if (isVisibilityResume) {
        setLoading(true);
        initApp();
      }
    }
  };

  const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

  const fetchLastCheckStats = async (retries = 3) => {
    for (let attempt = 0; attempt <= retries; attempt++) {
      const data = await apiProxyFetch("GET", "/api/analysis-report");
      if (data.ok && data.payload?.report) {
        applyLastCheckStats(data.payload.report as string);
        return;
      }
      if (attempt < retries) await sleep(800);
    }
  };

  const applyLastCheckStats = (yaml: string) => {
    const duration = yaml.match(/check_duration_raw:\s*(\d+)/)?.[1];
    const total = yaml.match(/check_count_raw:\s*(\d+)/)?.[1];
    const available = yaml.match(/alive_count:\s*(\d+)/)?.[1];
    const traffic = yaml.match(/check_traffic_total:\s*(.+)/)?.[1];
    let time = yaml.match(/check_time_raw:\s*['"]?(.*?)['"]?(?:\n|$)/)?.[1];

    if (time) {
      const d = new Date(time.replace(" ", "T"));
      if (!isNaN(d.getTime())) {
        time = `${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")} ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
      }
    }
    if (total && available) {
      setLastStats({
        time: time || "-",
        duration: duration ? (parseInt(duration) >= 60 ? `${Math.floor(parseInt(duration) / 60)}分` : `${duration}秒`) : "0秒",
        total: parseInt(total) >= 10000 ? (parseInt(total) / 10000).toFixed(1) + "万" : total,
        available: available,
        traffic: traffic || "-",
      });
    }
  };

  const toggleCheck = async () => {
    if (actionInFlight) return;
    setActionInFlight(true);
    try {
      if (status?.isChecking) {
        showToast("正在发送停止指令...", "info");
        const res = await sfetch("/api/force-close", { method: "POST" });
        if (res.ok) showToast("已发送停止指令", "success");
      } else {
        showToast("正在启动检测...", "info");
        const res = await sfetch("/api/trigger-check", { method: "POST" });
        if (!res.ok) showToast(`启动检测失败`, "error");
      }
    } finally {
      setActionInFlight(false);
    }
  };

  // API Key 行内编辑逻辑 (保留 YAML 注释)
  // 交互：点击编辑图标 -> 光标聚焦进输入框；内容与原值不同时，编辑图标变为
  // 保存图标；点击保存图标（或 Enter）通过 API 保存；Esc 或失焦（未修改时）取消。
  const [editingKey, setEditingKey] = useState(false);
  const [keyDraft, setKeyDraft] = useState("");
  const [keySaving, setKeySaving] = useState(false);
  const [keySaved, setKeySaved] = useState(false);
  const [justSaved, setJustSaved] = useState(false);
  const keyInputRef = useRef<HTMLInputElement>(null);
  const keyChanged = editingKey && keyDraft.trim() !== "" && keyDraft.trim() !== info?.apiKey;

  // 进入编辑模式后，等输入框真正挂载再聚焦并全选，避免手动 setTimeout 猜时序
  useEffect(() => {
    if (editingKey) {
      keyInputRef.current?.focus();
      keyInputRef.current?.select();
    }
  }, [editingKey]);

  const startEditKey = () => {
    if (!info || keySaving) return;
    setKeyDraft(info.apiKey);
    setKeyShown(true); // 编辑时强制明文显示，方便核对
    setEditingKey(true);
  };

  const cancelEditKey = () => {
    setEditingKey(false);
    setKeyDraft(info?.apiKey || "");
    setKeyShown(false); // 取消编辑时，强制恢复为隐藏（模糊）状态
  };

  const saveApiKey = async () => {
    if (!info || keySaving) return;
    const newKey = keyDraft.trim();
    if (!newKey || newKey === info.apiKey) { setEditingKey(false); return; }

    setKeySaving(true);
    try {
      // 1. 获取原汁原味的配置文件内容（原生桥接读取，避免网络层时序问题）
      const res = await apiProxyFetch("GET", "/api/config");
      if (!res.ok) throw new Error("无法读取底层配置文件");

      let rawYaml = typeof res.payload === 'string' ? res.payload : (res.payload?.config || res.payload?.content || "");
      if (!rawYaml) throw new Error("获取的配置内容为空");

      // 2. 使用正则精准替换 api-key 的值，完美保留文件的注释和缩进。
      const apiKeyLineRe = /^(\s*api-key\s*:\s*)(['"]?)([^'"\r\n#]*?)\2(\s*(?:#.*)?)$/m;
      if (!apiKeyLineRe.test(rawYaml)) {
        throw new Error("未在配置文件中找到 api-key 字段，请手动检查");
      }
      const newYaml = rawYaml.replace(apiKeyLineRe, (_m: string, prefix: string, quote: string, _old: string, suffix: string) => `${prefix}${quote}${newKey}${quote}${suffix}`);

      if (newYaml === rawYaml) {
        throw new Error("api-key 未发生变化，已取消保存");
      }

      // 3. 利用 APIProxy 在进程内无损 POST 回后端
      const payload = JSON.stringify({ content: newYaml });
      const saveRes = await GuiApp.APIProxy("POST", "/api/config", payload);
      const saveObj = JSON.parse(saveRes);

      // 1. 如果写入配置失败，必定进此异常，能到下一步说明内核已经写入文件
      if (saveObj.status >= 300) throw new Error(saveObj.body);

      // 2. 兼容处理：手机端可能由于绑定落后没有 MarkAPIKeyManual 方法
      if (typeof GuiApp.MarkAPIKeyManual === 'function') {
        try { await GuiApp.MarkAPIKeyManual(); } catch (e) { console.warn(e); }
      }

      // 3. 给内核重载配置的时间
      await new Promise(r => setTimeout(r, 600));

      // 4. 乐观更新（不再强制校验抛错）：只要上一步存进去了，即使内核还没反应过来导致校验未过，我们也认定它是成功的。
      let verifyInfo: AppInfo | null;
      try { verifyInfo = await GuiApp.GetAppInfo(); } catch (e) { }

      setInfo(prev => {
        if (!prev) return verifyInfo;
        return { ...prev, apiKey: newKey, keyIsRandom: false };
      });

      localStorage.setItem("scp_api_key", newKey);
      sessionStorage.setItem("scp_api_key", newKey);

      // 编辑成功：恢复为隐藏状态、退出编辑模式，并触发一次短暂的成功动画
      setKeyShown(false);
      setEditingKey(false);
      setJustSaved(true);
      setKeySaved(true)
      setTimeout(() => setJustSaved(false), 900);

      showToast("API 密钥更新成功", "success");
    } catch (err) {
      showToast("更新失败: " + String(err), "error");
    } finally {
      setKeySaving(false);
    }
  };

  const onKeyEditIconClick = () => {
    if (!editingKey) return startEditKey();
    if (keyChanged) return saveApiKey();
    cancelEditKey();
  };

  const onKeyInputKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Enter") { e.preventDefault(); if (keyChanged) saveApiKey(); }
    else if (e.key === "Escape") { e.preventDefault(); cancelEditKey(); }
  };

  // 工具函数
  const copyText = async (text: string, name: string) => {
    let success = false;

    // 1. 首选：现代 Web API
    // 优势：在现代手机上触发 OS 级别的原生剪贴板反馈（如 Android 13+ 屏幕左下角的气泡预览）
    if (navigator.clipboard) {
      try {
        await navigator.clipboard.writeText(text);
        success = true;
      } catch (e) {
        console.warn("现代 Clipboard API 失败，准备降级:", e);
      }
    }

    // 2. 兜底一：Wails 原生剪贴板 API
    if (!success) {
      try {
        if (typeof (GuiApp as any).CopyToClipboard === 'function') {
          success = await (GuiApp as any).CopyToClipboard(text);
        }
      } catch (e) {
        console.warn("Wails 原生复制失败:", e);
      }
    }

    // 3. 兜底二：传统 Web API（纯浏览器调试环境时的最终妥协）
    if (!success) {
      try {
        const textArea = document.createElement("textarea");
        textArea.value = text;
        // 绝对隐藏，防止拉起键盘或画面闪烁
        textArea.style.position = "fixed";
        textArea.style.top = "-9999px";
        textArea.style.left = "-9999px";
        textArea.style.opacity = "0";

        document.body.appendChild(textArea);
        textArea.focus();
        textArea.select();

        success = document.execCommand("copy");
        document.body.removeChild(textArea);
      } catch (e) {
        console.warn("传统 execCommand 彻底失败:", e);
      }
    }

    // 统一 UI 反馈
    if (success) {
      showToast(`已复制 ${name}`, "success");
    } else {
      showToast(`复制失败，请尝试手动复制`, "error");
    }

    setSheetSub(false);
    setSheetPath(false);
  };

  // 构建 sub-store 地址
  const buildSubStoreUrl = () => {
    if (!info) return { url: "", subStorePath: "" };
    let path = info.subStorePath || "";
    if (!path.startsWith("/")) path = "/" + path;
    const cleanPort = String(info.subStorePort).trim().replace(/^:/, "");
    const baseUrl = `http://127.0.0.1:${cleanPort}`;
    const lastPath = sessionStorage.getItem("scp_lastSubStorePath");
    const isFirstTime = lastPath === null;
    sessionStorage.setItem("scp_lastSubStorePath", path);
    return {
      url: (isFirstTime || lastPath !== path) ? `${baseUrl}?api=${path}` : baseUrl,
      subStorePath: path,
    };
  };

  const getSubLink = (path: string) => {
    if (!info) return "";
    const basePath = info.subStorePath?.endsWith("/") ? info.subStorePath.slice(0, -1) : (info.subStorePath || "");
    const cleanPath = path.startsWith("/") ? path : `/${path}`;
    const cleanPort = String(info.subStorePort).trim().replace(/^:/, "");
    return `http://127.0.0.1:${cleanPort}${basePath}${cleanPath}`;
  };

  // 路径中段省略逻辑
  const infoRef = useRef<AppInfo | null>(null);
  infoRef.current = info;

  const truncatePathEl = (el: HTMLSpanElement) => {
    const path = infoRef.current?.configPath;
    if (!path) return;

    const availW = el.clientWidth;
    if (availW <= 0) return;

    const canvas = document.createElement("canvas");
    const ctx = canvas.getContext("2d");
    if (!ctx) { el.textContent = path; return; }

    const style = window.getComputedStyle(el);
    ctx.font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;

    if (ctx.measureText(path).width <= availW) { el.textContent = path; return; }

    let lo = 0, hi = path.length;
    while (hi - lo > 1) {
      const mid = (lo + hi) >> 1;
      const candidate = path.slice(0, Math.ceil(mid / 2)) + "……" + path.slice(-Math.floor(mid / 2));
      if (ctx.measureText(candidate).width <= availW) lo = mid; else hi = mid;
    }
    el.textContent = path.slice(0, Math.ceil(lo / 2)) + "……" + path.slice(-Math.floor(lo / 2));
  };

  // 用回调 ref 代替"仅依赖 info?.configPath 的 useEffect"
  const pathObserverCleanup = useRef<(() => void) | null>(null);
  const attachPathTruncate = useCallback((el: HTMLSpanElement | null) => {
    pathObserverCleanup.current?.();
    pathObserverCleanup.current = null;
    pathRef.current = el;
    if (!el) return;

    truncatePathEl(el);
    // 监听 span 自身可用尺寸的变化
    const ro = new ResizeObserver(() => requestAnimationFrame(() => truncatePathEl(el)));
    ro.observe(el);
    pathObserverCleanup.current = () => ro.disconnect();
  }, []);

  // configPath 内容真正变化时（例如切换配置文件后），元素已挂载，重新截断一次即可
  useEffect(() => {
    if (pathRef.current) truncatePathEl(pathRef.current);
  }, [info?.configPath]);

  useEffect(() => () => pathObserverCleanup.current?.(), []);

  // 渲染分支
  if (loading) {
    return (
      <div class="m-page flex-center">
        <div class="loading-wrapper">
          <img class="logo-pulse" src="/static/icon/subs-check-pro.svg" alt="logo" />
          <div class="loading-ring"></div>
        </div>
        <p class="loading-text">正在唤醒内核</p>
      </div>
    );
  }

  if (errMsg) {
    return (
      <div class="m-page flex-center">
        <div class="error-card">
          <div class="error-icon">
            <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="7.86 2 16.14 2 22 7.86 22 16.14 16.14 22 7.86 22 2 16.14 2 7.86 7.86 2"></polygon><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>
          </div>
          <h3>内核启动失败</h3>
          <p class="error-msg">{errMsg}</p>
          <p class="error-hint">请检查端口是否被占用，或彻底清理后台进程后重试。</p>
        </div>
      </div>
    );
  }

  const isChecking = status?.isChecking;
  const progressPercent = status?.proxyCount ? (status.progress / status.proxyCount) * 100 : 0;

  return (
    <div class="m-page">
      <header class="m-header">
        <button class="icon-btn theme-btn" onClick={toggleTheme} title="切换主题">
          {theme === "light" ? (
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="5" /><line x1="12" y1="1" x2="12" y2="3" /><line x1="12" y1="21" x2="12" y2="23" /><line x1="4.22" y1="4.22" x2="5.64" y2="5.64" /><line x1="18.36" y1="18.36" x2="19.78" y2="19.78" /><line x1="1" y1="12" x2="3" y2="12" /><line x1="21" y1="12" x2="23" y2="12" /><line x1="4.22" y1="19.78" x2="5.64" y2="18.36" /><line x1="18.36" y1="5.64" x2="19.78" y2="4.22" /></svg>
          ) : (
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" /></svg>
          )}
        </button>
        <div class="logo-box"><img src="/static/icon/subs-check-pro.svg" alt="Logo" /></div>
        <div class="slogan">高性能网络节点检测管理引擎</div>
        <div class="lp-footer">
          <a class="ver-tag ver-gui" onClick={() => GuiApp.OpenInBrowser("https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-free")}>
            GUI&nbsp;{info?.guiVersion || "dev"}
          </a>
          <span class="ver-dot">·</span>
          <a class="ver-tag ver-core" onClick={() => GuiApp.OpenInBrowser("https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro")}>
            内核&nbsp;{info?.coreVersion || "dev"}
          </a>
        </div>
      </header>

      <main class="m-content">
        {/* 状态卡片 */}
        <div class="card status-card">
          <div class="status-header">
            <span class={`label ${isChecking || finalizing ? "checking-label" : ""}`}>{isChecking ? status?.stepName : finalizing ? "生成报告中" : "运行状态"}</span>
            <div class={`status-badge ${isChecking || finalizing ? "checking" : "idle"}`}>
              <span class="dot"></span>{isChecking ? "检测中" : finalizing ? "整理中" : "空闲"}
            </div>
          </div>
          <div class="status-body">
            {isChecking ? (
              <Fragment>
                <div class="progress-bar"><div class="progress-fill" style={{ width: `${progressPercent}%` }}></div></div>
                <div class="progress-info">
                  <div class="available-wrap">
                    <span class="progress-label">可用：</span><span class="available-pulse">{status?.available || 0}</span>
                  </div>
                  <span class="progress-text">{status?.progress} / {status?.proxyCount}</span>
                </div>
              </Fragment>
            ) : finalizing ? (
              <div class="status-text muted status-finalizing">
                <svg class="icon-spin" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M21 12a9 9 0 1 1-9-9" /></svg>
                正在生成检测报告…
              </div>
            ) : (
              lastStats ? (
                <Fragment>
                  <div class="stats-grid">
                    <div class="stat-item"><span class="val">{lastStats.available}</span><span class="lbl">可用节点</span></div>
                    <div class="stat-item"><span class="val">{lastStats.total}</span><span class="lbl">检测数量</span></div>
                    <div class="stat-item"><span class="val">{lastStats.duration}</span><span class="lbl">检测耗时</span></div>
                    <div class="stat-item"><span class="val">{lastStats.traffic}</span><span class="lbl">消耗流量</span></div>
                  </div>
                  <div class="status-text muted" style={{ fontSize: '11px', textAlign: 'right' }}>最后检测于: {lastStats.time}</div>
                </Fragment>
              ) : (
                <div class="status-text muted">{status?.lastResult || "尚无检测记录，一切准备就绪。"}</div>
              )
            )}
          </div>
        </div>

        {/* 只有当首次运行 OR 密钥随机时，才渲染整个提示容器 */}
        {(info?.isFirstRun || info?.keyIsRandom) && (
          <div className="alert-container">
            {info?.isFirstRun && (
              <div className={`alert alert-success ${(info?.isFirstRun && info?.keyIsRandom) ? "half" : "full"}`}>
                {!keySaved ? "🎉 已创建默认配置文件" : "🎉 密钥已修改，Web 访问请使用新密钥"}
              </div>
            )}
            {info?.keyIsRandom && (
              <div className={`alert alert-warning ${(info?.isFirstRun && info?.keyIsRandom) ? "half" : "full"}`}>
                ⚠️ Web 访问建议固定密钥
              </div>
            )}
          </div>
        )}

        {/* 信息卡片 */}
        <div class="card info-card">
          <div class="info-groups-wrapper">
            <div class="info-group">
              <div class="label-wrap">
                <div class="label">密钥</div>
                {info?.keyIsRandom ? (
                  <div class="alert alert-warning subtle disabled">
                    <svg class="icon-random" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="16 3 21 3 21 8"></polyline>
                      <line x1="4" y1="20" x2="21" y2="3"></line>
                      <polyline points="21 16 21 21 16 21"></polyline>
                      <line x1="15" y1="15" x2="21" y2="21"></line>
                      <line x1="4" y1="4" x2="9" y2="9"></line>
                    </svg>
                  </div>
                ) : (
                  <div class={`alert alert-success subtle ${justSaved ? "key-alert-pop" : ""}`}>
                    <svg class="icon-check" xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                      <polyline points="20 6 9 17 4 12"></polyline>
                    </svg>
                  </div>
                )}
              </div>
              <div class={`key-wrap ${editingKey ? "editing" : ""} ${justSaved ? "just-saved" : ""}`}>
                {editingKey ? (
                  <input
                    ref={keyInputRef}
                    class="key-text key-text-input"
                    type={keyShown ? "text" : "password"}
                    inputmode="latin"
                    value={keyDraft}
                    disabled={keySaving}
                    onInput={(e: any) => setKeyDraft(e.currentTarget.value)}
                    onKeyDown={onKeyInputKeyDown}
                    onBlur={() => { cancelEditKey(); }}
                  />
                ) : (
                  <span class={`key-text ${keyShown ? "" : "blur"}`} onClick={() => setKeyShown(!keyShown)}>{info?.apiKey}</span>
                )}

                {/* 三个按钮位置始终固定；只有编辑/保存这一个按钮的图标随编辑状态变化 */}
                <button
                  class={`icon-btn ${keyChanged ? "btn-save-active" : ""}`}
                  onMouseDown={(e: any) => e.preventDefault()}
                  onClick={onKeyEditIconClick}
                  disabled={keySaving}
                  title={editingKey ? (keyChanged ? "保存" : "取消编辑") : "修改 API 密钥"}
                >
                  {keySaving ? (
                    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="feather feather-loader"><line x1="12" y1="2" x2="12" y2="6"></line><line x1="12" y1="18" x2="12" y2="22"></line><line x1="4.93" y1="4.93" x2="7.76" y2="7.76"></line><line x1="16.24" y1="16.24" x2="19.07" y2="19.07"></line><line x1="2" y1="12" x2="6" y2="12"></line><line x1="18" y1="12" x2="22" y2="12"></line><line x1="4.93" y1="19.07" x2="7.76" y2="16.24"></line><line x1="16.24" y1="7.76" x2="19.07" y2="4.93"></line></svg>
                  ) : keyChanged ? (
                    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z"></path><polyline points="17 21 17 13 7 13 7 21"></polyline><polyline points="7 3 7 8 15 8"></polyline></svg>
                  ) : (
                    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="feather feather-edit-3"><path d="M12 20h9"></path><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"></path></svg>
                  )}
                </button>

                <button class="icon-btn" onMouseDown={(e: any) => e.preventDefault()} onClick={() => setKeyShown(!keyShown)} title="显示/隐藏">
                  {keyShown ? <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" /><circle cx="12" cy="12" r="3" /></svg> : <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" /><path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" /><line x1="1" y1="1" x2="23" y2="23" /></svg>}
                </button>
                <button class="icon-btn" onMouseDown={(e: any) => e.preventDefault()} onClick={() => copyText(editingKey ? keyDraft : info!.apiKey, "API 密钥")} title="复制">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></svg>
                </button>
              </div>
            </div>
            <div class="info-group">
              <div class="label">服务</div>
              <div class="port-badges">
                <span class="port-badge"><span class="port-dot"></span> HTTP <span class="val">{info?.listenPort}</span></span>
                {info?.subStorePort && <span class="port-badge"><span class="port-dot"></span> Sub <span class="val">{info.subStorePort}</span></span>}
              </div>
            </div>
            <div class="info-group">
              <div class="label">配置</div>
              <div class="cfg-path-row" onClick={() => setSheetPath(true)}>
                <span class="cfg-path-text" ref={attachPathTruncate}>{info?.configPath}</span>
              </div>
            </div>
          </div>
        </div>
      </main>

      <footer class="m-footer">
        <div class="quick-actions">
          <button class="btn-quick" onClick={() => GuiApp.OpenInBrowser(`http://127.0.0.1:${info?.listenPort}`)} title="在系统浏览器中打开管理面板">
            <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10" /><line x1="2" y1="12" x2="22" y2="12" /><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" /></svg>
          </button>
          <button class="btn-quick" onClick={() => window.location.href = "/analysis.html"} title="分析报告">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="20" x2="18" y2="10" /><line x1="12" y1="20" x2="12" y2="4" /><line x1="6" y1="20" x2="6" y2="14" /><line x1="2" y1="20" x2="22" y2="20" /></svg>
          </button>
          <button class="btn-quick" onClick={() => window.location.href = "/files.html"} title="内置文件">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" /></svg>
          </button>
          {info?.subStorePort && (
            <Fragment>
              <button class="btn-quick" onClick={() => window.location.href = buildSubStoreUrl().url} title="订阅管理">
                <svg viewBox="0 0 108 108" fill="currentColor"><path d="M12.6 35C8.2 21.8 21 8.5 34.3 12.5c3.4 1 8.2 4.9 15.2 11.8l10.2 10.3-2.8 2.8-2.8 2.8-10-9.9c-8.2-8.2-10.7-9.9-14.2-9.9-9.2 0-12.5 10.6-5.4 17.4l3.8 3.8-2.8 3-2.8 3-4.2-4.1c-2.3-2.2-4.9-6-5.6-8.4h-.2z" /><path d="M48.1 46.5l-7.4 7.6 3.8 3.8 3.8 3.8-2.8 2.8-2.8 3-6.7-6.8-6.8-6.7 6.4-6.4c3.4-3.4 6.7-6.4 7.2-6.4s2 1.8 5.6 5.2zM59.7 46.5l7.4 7.6-3.8 3.8-3.8 3.8 2.8 2.8 2.8 3 6.7-6.8 6.8-6.7-6.4-6.4c-3.4-3.4-6.7-6.4-7.2-6.4s-2 1.8-5.6 5.2zM24.4 70.4c-4.5 5.2-5 10.8-1.3 14.6 4 4 10.3 3.4 14.8-1.3l3.8-3.8 3 2.8 3 2.8-4.1 4.2c-8 8.2-18.4 8.8-26 1-7.7-7.6-7.4-17.5.9-26l4-4.2 3 2.8 2.8 2.7-3.8 4.4zM83.6 37.6c4.5-5.2 5-10.8 1.3-14.6-4-4-10.3-3.4-14.8 1.3l-3.8 3.8-3-2.8-3-2.8 4.1-4.2c8-8.2 18.4-8.8 26-1 7.7 7.6 7.4 17.5-.9 26l-4 4.2-3-2.8-2.8-2.7 3.8-4.4z" /><path d="M95.4 73c4.4 13.3-8.4 26.5-21.6 22.5-3.4-1-8.2-4.9-15.2-11.8L48.4 73.4l2.8-2.8 2.8-2.8 10 9.9c8.2 8.2 10.7 9.9 14.2 9.9 9.2 0 12.5-10.6 5.4-17.4l-3.8-3.8 2.8-3 2.8-3 4.2 4.1c2.3 2.2 4.9 6 5.6 8.4z" /></svg>
              </button>
              <button class="btn-quick" onClick={() => setSheetSub(true)} title="订阅分享">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" /><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" /></svg>
              </button>
            </Fragment>
          )}
        </div>

        <div class="dynamic-footer">
          <button class={`action-fab-small ${isChecking ? "btn-stop" : "btn-play"}`} onClick={toggleCheck}>
            {isChecking ? <svg viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="6" width="12" height="12" rx="2" /></svg> : <svg viewBox="0 0 24 24" fill="currentColor"><path d="M8 5v14l11-7z" /></svg>}
          </button>

          <button class="btn-primary-long" onClick={() => window.location.href = "/admin.html"}>
            <span class="btn-text">管理面板</span>
          </button>

          {/* 右侧：关于按钮*/}
          <button class="action-fab-small btn-about" onClick={() => setSheetAbout(true)}>
            <svg viewBox="0 0 1024 1024" width="24" height="24"><path d="M858.026667 307.2H186.026667c-12.373333 0-22.186667-9.813333-22.186667-22.186667v-23.466666c0-12.373333 9.813333-22.186667 22.186667-22.186667h672c12.373333 0 22.186667 9.813333 22.186666 22.186667v23.466666c0 12.373333-9.813333 22.186667-22.186666 22.186667zM858.026667 546.133333H186.026667c-12.373333 0-22.186667-9.813333-22.186667-22.186666v-23.466667c0-12.373333 9.813333-22.186667 22.186667-22.186667h672c12.373333 0 22.186667 9.813333 22.186666 22.186667v23.466667c0 12.373333-9.813333 22.186667-22.186666 22.186666zM858.026667 785.066667H186.026667c-12.373333 0-22.186667-9.813333-22.186667-22.186667v-23.466667c0-12.373333 9.813333-22.186667 22.186667-22.186666h672c12.373333 0 22.186667 9.813333 22.186666 22.186666v23.466667c0 12.373333-9.813333 22.186667-22.186666 22.186667z" fill="currentColor"></path></svg>
          </button>
        </div>
      </footer>

      {/* --- Bottom Sheets 弹窗群 --- */}
      <div class={`bottom-sheet-overlay ${sheetSub ? "active" : ""}`} onClick={() => setSheetSub(false)}>
        <div class="bottom-sheet" onClick={e => e.stopPropagation()}>
          <div class="sheet-drag-handle"></div>
          <h3 class="sheet-title">订阅链接</h3>
          <p class="sheet-desc">建议在 Subs Free 同一局域网的代理客户端导入以下链接</p>
          <div class="sheet-list">
            {[
              { title: "通用订阅", url: "/download/sub", icon: <svg class="link-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"></path><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"></path></svg> },
              { title: "v2ray", url: "/download/sub?target=V2Ray", img: "/static/icon/v2ray.svg" },
              { title: "Mihomo", url: "/api/file/mihomo", img: "/static/icon/mihomo.svg" },
              { title: "Shadowrocket", url: "/download/sub?target=ShadowRocket", img: "/static/icon/shadowrocket.svg" },
              { title: `singbox-${info?.singBoxOldVer}`, url: `/api/file/singbox-${info?.singBoxOldVer}`, img: "/static/icon/sing-box.svg" },
              { title: `singbox-${info?.singBoxLatestVer}`, url: `/api/file/singbox-${info?.singBoxLatestVer}`, img: "/static/icon/sing-box.svg" },
            ].map(item => (
              <div class="list-item sub-copy" onClick={() => copyText(getSubLink(item.url), item.title)}>
                {item.img ? <img src={item.img} class="link-icon" /> : item.icon}
                <span class="link-text">{item.title}</span>
                <button class="share-link-btn" onMouseDown={(e: any) => e.preventDefault()} onClick={(e: any) => { e.stopPropagation(); GuiApp.ShareLink(item.title, getSubLink(item.url)); }} title="分享到其他应用">
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="18" cy="5" r="3"></circle><circle cx="6" cy="12" r="3"></circle><circle cx="18" cy="19" r="3"></circle><line x1="8.59" y1="13.51" x2="15.42" y2="17.49"></line><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"></line></svg>
                </button>
                <svg class="link-copy-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>
              </div>
            ))}
          </div>
        </div>
      </div>

      <div class={`bottom-sheet-overlay ${sheetPath ? "active" : ""}`} onClick={() => setSheetPath(false)}>
        <div class="bottom-sheet" onClick={e => e.stopPropagation()}>
          <div class="sheet-drag-handle"></div>
          <h3 class="sheet-title">配置文件路径</h3>
          <div class="path-full-box"><div class="path-full-text">{info?.configPath}</div></div>
          <button class="btn-primary-long" onClick={() => copyText(info!.configPath, "配置文件路径")} style={{ width: '100%', marginTop: '16px' }}>
            <span class="btn-text">复制路径</span>
          </button>
        </div>
      </div>

      <div class={`bottom-sheet-overlay ${sheetAbout ? "active" : ""}`} onClick={() => setSheetAbout(false)}>
        <div class="bottom-sheet" onClick={e => e.stopPropagation()}>
          <div class="sheet-drag-handle"></div>
          <div class="about-header-mobile">
            <img src="/static/icon/subs-check-pro.svg" class="about-logo-mobile" />
            <div class="about-title-mobile">Subs Free</div>
            <div class="about-desc-mobile">基于 Subs Check Pro v3 内核的高性能网络节点检测和管理客户端</div>
            <div class="lp-footer" style={{ marginTop: '12px' }}>
              <span class="ver-tag ver-gui" onClick={() => GuiApp.OpenInBrowser("https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-free")}>GUI&nbsp;{info?.guiVersion || "dev"}</span>
              <span class="ver-dot">·</span>
              <span class="ver-tag ver-core" onClick={() => GuiApp.OpenInBrowser("https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro")}>内核&nbsp;{info?.coreVersion || "dev"}</span>
            </div>
          </div>

          <div class="about-links-grid">
            <div class="aw-link-card aw-featured" onClick={() => GuiApp.OpenInBrowser("https://proxy.linkpc.dpdns.org/https://t.me/subs_check_pro")}>
              <div class="aw-link-icon-wrap aw-featured-icon">
                <svg class="aw-link-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <line x1="22" y1="2" x2="11" y2="13"></line><polygon points="22 2 15 22 11 13 2 9 22 2"></polygon>
                </svg>
              </div>
              <div class="aw-link-body">
                <strong class="aw-link-title">Telegram 交流群</strong>
                <span class="aw-link-desc">技术交流 · 异常反馈 · 最新动态</span>
              </div>
              <svg class="aw-link-arrow" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="7" y1="17" x2="17" y2="7" /><polyline points="7 7 17 7 17 17" />
              </svg>
            </div>
            <div class="aw-link-card" onClick={() => GuiApp.OpenInBrowser("https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-free")}>
              <div class="aw-link-icon-wrap">
                <svg class="aw-link-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M9 19c-5 1.5-5-2.5-7-3m14 6v-3.87a3.37 3.37 0 0 0-.94-2.61c3.14-.35 6.44-1.54 6.44-7A5.44 5.44 0 0 0 20 4.77 5.07 5.07 0 0 0 19.91 1S18.73.65 16 2.48a13.38 13.38 0 0 0-7 0C6.27.65 5.09 1 5.09 1A5.07 5.07 0 0 0 5 4.77a5.44 5.44 0 0 0-1.5 3.78c0 5.42 3.3 6.61 6.44 7A3.37 3.37 0 0 0 9 18.13V22"></path>
                </svg>
              </div>
              <div class="aw-link-body">
                <strong class="aw-link-title">GUI 客户端仓库</strong>
                <span class="aw-link-desc">获取 Windows、Linux、Mac 和 Android 客户端</span>
              </div>
              <svg class="aw-link-arrow" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="7" y1="17" x2="17" y2="7" /><polyline points="7 7 17 7 17 17" />
              </svg>
            </div>
            <div class="aw-link-card" onClick={() => GuiApp.OpenInBrowser("https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro")}>
              <div class="aw-link-icon-wrap">
                <svg class="aw-link-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M9 19c-5 1.5-5-2.5-7-3m14 6v-3.87a3.37 3.37 0 0 0-.94-2.61c3.14-.35 6.44-1.54 6.44-7A5.44 5.44 0 0 0 20 4.77 5.07 5.07 0 0 0 19.91 1S18.73.65 16 2.48a13.38 13.38 0 0 0-7 0C6.27.65 5.09 1 5.09 1A5.07 5.07 0 0 0 5 4.77a5.44 5.44 0 0 0-1.5 3.78c0 5.42 3.3 6.61 6.44 7A3.37 3.37 0 0 0 9 18.13V22"></path>
                </svg>
              </div>
              <div class="aw-link-body">
                <strong class="aw-link-title">内核引擎仓库</strong>
                <span class="aw-link-desc">Sub-Check-Pro v3 引擎，支持全平台运行和 Docker 部署</span>
              </div>
              <svg class="aw-link-arrow" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <line x1="7" y1="17" x2="17" y2="7" /><polyline points="7 7 17 7 17 17" />
              </svg>
            </div>
          </div>
          <div class="about-footer-copyright">
            © 2026 Sinspired · GPL-3.0 License
          </div>
        </div>
      </div>

      <div class={`toast toast-${toast.type} ${toast.visible ? "show" : ""}`}>{toast.msg}</div>
    </div>
  );
}