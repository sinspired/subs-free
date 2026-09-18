/**
 * frontend/src/hooks/useTheme.ts
 */
import { useEffect, useState } from 'preact/hooks';

const STORAGE_KEY = 'scp_theme';

/** 获取 Gin HTTP 服务的 base URL（由 app.tsx 在 loadAppInfo 后注入）。 */
function getBaseURL(): string {
  return (window as any).__CORE_BASE_URL || 'http://127.0.0.1:8199';
}

/**
 * 将 'auto' / '' 解析为具体的 'dark' 或 'light'。
 * 保留原始字符串以便与服务端保存格式一致（只在渲染时才调用 resolve）。
 */
function resolveTheme(t: string): string {
  if (t === 'auto' || !t) {
    return matchMedia('(prefers-color-scheme:dark)').matches ? 'dark' : 'light';
  }
  return t;
}

/**
 * 从服务端 /admin/theme 读取主题值。
 *
 * 优先级：服务端明确值 > 登录窗口 localStorage > 系统主题
 * 若服务端返回 'auto' / 空，则把 localStorage 里的非 auto 值回写服务端，
 * 实现"重启后自动恢复上次手动设置的主题"。
 */
async function fetchThemeFromServer(): Promise<string> {
  try {
    const r = await fetch(`${getBaseURL()}/admin/theme`);
    const d = await r.json();
    if (d.theme && d.theme !== 'auto') return d.theme;

    // 服务端无明确值，读本地存储
    const local = localStorage.getItem(STORAGE_KEY);
    if (local && local !== 'auto') {
      // 回写服务端，让后续打开的其他窗口也能读到
      saveThemeToServer(local);
      return local;
    }
    return resolveTheme('auto');
  } catch {
    // 网络/端口错误：直接从 localStorage 恢复
    return resolveTheme(localStorage.getItem(STORAGE_KEY) || 'auto');
  }
}

/**
 * 将主题值持久化到服务端（fire-and-forget）。
 * 调用方保证 t 为 'dark' | 'light' | 'auto'。
 */
function saveThemeToServer(t: string) {
  fetch(`${getBaseURL()}/admin/theme`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ theme: t }),
  }).catch(() => { });
}

export function useTheme() {
  /**
   * 初始值：优先读登录窗口自己的 localStorage，避免等待 fetch 期间的首帧闪烁。
   * 若 localStorage 为空则回退系统主题。
   */
  const [theme, setTheme] = useState<string>(() =>
    resolveTheme(localStorage.getItem(STORAGE_KEY) || 'auto')
  );

  /** 每当 theme 变化时同步写入 <html data-theme="…">。 */
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme);
  }, [theme]);

  /**
   * 首次 mount 时发起一次服务端同步。
   * 此时 __CORE_BASE_URL 可能尚未设置（app.tsx loadAppInfo 未完成），
   * getBaseURL() 回退 8199；对于默认端口用户，此次 fetch 即可成功。
   * 非默认端口用户等待 app.tsx 调用 syncFromServer() 再做一次正确同步。
   */
  useEffect(() => {
    fetchThemeFromServer().then(setTheme);
  }, []);

  /**
   * 供 app.tsx 在 __CORE_BASE_URL 确定后（loadAppInfo 完成后）主动调用，
   * 修复非默认端口用户初始化竞态：用正确端口重新读一次服务端主题。
   */
  const syncFromServer = () => {
    fetchThemeFromServer().then(setTheme);
  };

  /**
   * 切换深色 / 浅色主题。
   *
   * 同步写 localStorage，保证服务端不可用时重启后依然可从本地恢复。
   * 同时 POST 到服务端，让其他已打开的窗口（admin、analysis 等）
   * 下次初始化时读到一致的主题值。
   */
  const toggleTheme = () => {
    const next = theme === 'dark' ? 'light' : 'dark';
    setTheme(next);

    localStorage.setItem(STORAGE_KEY, next);
    saveThemeToServer(next);
  };

  /**
   * 重置为系统默认主题（双击切换按钮触发）。
   *
   * 清除 localStorage，确保服务端不可用时重启后跟随系统主题（而非卡在上次手动值）。
   */
  const resetTheme = () => {
    const sys = resolveTheme('auto');
    setTheme(sys);
  
    saveThemeToServer('auto');
  };

  return { theme, toggleTheme, resetTheme, syncFromServer };
}