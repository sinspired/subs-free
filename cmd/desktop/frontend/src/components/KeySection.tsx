/**
 * frontend/src/components/KeySection.tsx
 */
import { useState, useRef, useEffect } from 'preact/hooks';
import { Browser } from '@wailsio/runtime'; // 引入 Wails3 Browser API
import { GuiApp } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';
import { AppInfo } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';

export interface DesktopCheckState {
  isChecking: boolean;
  stepName: string;
  available: number;
  progress: number;
  proxyCount: number;
  lastResult: string;
  nextCheckTime?: string;
  nextCheckUnix?: number;
  scheduleMode?: string;
}

export interface LastStats {
  time: string;
  duration: string;
  total: string;
  available: string;
  traffic: string;
}

interface Props {
  info: AppInfo;
  status: DesktopCheckState | null;
  lastStats: LastStats | null;
  finalizing: boolean;
  toast: (msg: string) => void;
  onSelectConfig: (path: string) => void;
  onApiKeySaved: (newKey: string) => void;
  onOpenLogs: () => void;
}

// ── 路径中间截断 Hook ───────────────────────────────────────────────────────
// 使用 canvas measureText 精确测量像素宽度，通过二分查找找到最大可显示字符数，
// 并在路径中间插入省略号，保留路径的头部（盘符/根目录）和尾部（文件名）。
// ResizeObserver 监听容器尺寸变化，自动重新计算。
function useTruncatedPath(path: string): {
  spanRef: ReturnType<typeof useRef<HTMLSpanElement | undefined>>;
  display: string;
} {
  const spanRef = useRef<HTMLSpanElement | undefined>(undefined);
  const [display, setDisplay] = useState(path);

  useEffect(() => {
    const el = spanRef.current;
    if (!el || !path) {
      setDisplay(path);
      return;
    }

    function compute() {
      const el2 = spanRef.current;
      if (!el2) return;

      const availW = el2.offsetWidth;
      if (availW <= 0) {
        setDisplay(path);
        return;
      }

      const style = window.getComputedStyle(el2);
      const font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;

      const canvas = document.createElement('canvas');
      const ctx = canvas.getContext('2d');
      if (!ctx) {
        setDisplay(path);
        return;
      }
      ctx.font = font;

      if (ctx.measureText(path).width <= availW) {
        setDisplay(path);
        return;
      }

      const ellipsis = '…';
      let lo = 0;
      let hi = path.length;

      while (hi - lo > 1) {
        const mid = (lo + hi) >> 1;
        const f = Math.ceil(mid / 2);
        const b = Math.floor(mid / 2);
        const candidate =
          path.slice(0, f) + ellipsis + (b > 0 ? path.slice(-b) : '');
        if (ctx.measureText(candidate).width <= availW) {
          lo = mid;
        } else {
          hi = mid;
        }
      }

      if (lo === 0) {
        setDisplay(ellipsis);
      } else {
        const f = Math.ceil(lo / 2);
        const b = Math.floor(lo / 2);
        setDisplay(
          path.slice(0, f) + ellipsis + (b > 0 ? path.slice(-b) : ''),
        );
      }
    }

    compute();

    const ro = new ResizeObserver(compute);
    ro.observe(el);
    return () => ro.disconnect();
  }, [path]);

  return { spanRef, display };
}

export function KeySection({
  info,
  status,
  lastStats,
  finalizing,
  toast,
  onSelectConfig,
  onApiKeySaved,
  onOpenLogs,
}: Props) {
  const [keyShown, setKeyShown] = useState(false);
  const [launching, setLaunching] = useState(false);
  const [actionInFlight, setActionInFlight] = useState(false);

  // API Key 行内编辑状态
  const [editingKey, setEditingKey] = useState(false);
  const [keyDraft, setKeyDraft] = useState('');
  const [keySaving, setKeySaving] = useState(false);
  const [keySaved, setKeySaved] = useState(false);
  const keyInputRef = useRef<HTMLInputElement>(null);

  const { spanRef: pathRef, display: pathDisplay } = useTruncatedPath(
    info.configPath || '',
  );

  const currentKey = info.apiKey;
  const keyChanged = editingKey && keyDraft.trim() !== '' && keyDraft.trim() !== currentKey;
  const isChecking = !!status?.isChecking;
  const progressPercent = status?.proxyCount
    ? Math.min(100, (status.progress / status.proxyCount) * 100)
    : 0;

  useEffect(() => {
    if (editingKey) {
      keyInputRef.current?.focus();
      keyInputRef.current?.select();
    }
  }, [editingKey]);

  const toggleKey = () => {
    if (editingKey) return;
    setKeyShown(v => !v);
  };

  async function copyKey() {
    const val = editingKey ? keyDraft : currentKey;
    try {
      await navigator.clipboard.writeText(val);
      toast('已复制密钥');
    } catch {
      toast('复制失败，请手动复制');
    }
  }

  function startEditKey() {
    if (keySaving) return;
    setKeyDraft(currentKey);
    setKeyShown(true);
    setEditingKey(true);
  }

  function cancelEditKey() {
    setEditingKey(false);
    setKeyDraft(currentKey);
    setKeyShown(false);
  }

  async function saveApiKey() {
    if (keySaving) return;
    const newKey = keyDraft.trim();
    if (!newKey || newKey === currentKey) {
      cancelEditKey();
      return;
    }

    setKeySaving(true);
    try {
      const rawRes = await GuiApp.APIProxy('GET', '/api/config', '');
      const getObj = JSON.parse(rawRes) as { status: number; body: string };
      if (getObj.status >= 300) throw new Error('无法读取配置文件');

      let bodyObj: any = {};
      try { bodyObj = JSON.parse(getObj.body); } catch { bodyObj = getObj.body; }
      const rawYaml = typeof bodyObj === 'string'
        ? bodyObj
        : (bodyObj?.content || bodyObj?.config || '');
      if (!rawYaml) throw new Error('配置内容为空');

      const apiKeyLineRe = /^(\s*api-key\s*:\s*)(['"]?)([^'"\r\n#]*?)\2(\s*(?:#.*)?)$/m;
      if (!apiKeyLineRe.test(rawYaml)) {
        throw new Error('未在配置文件中找到 api-key 字段');
      }
      const newYaml = rawYaml.replace(
        apiKeyLineRe,
        (_m: string, prefix: string, quote: string, _old: string, suffix: string) =>
          `${prefix}${quote}${newKey}${quote}${suffix}`,
      );

      const postRaw = await GuiApp.APIProxy(
        'POST',
        '/api/config',
        JSON.stringify({ content: newYaml }),
      );
      const postObj = JSON.parse(postRaw) as { status: number; body: string };
      if (postObj.status >= 300) throw new Error(postObj.body || '保存失败');

      try { await GuiApp.MarkAPIKeyManual(); } catch { /* 忽略 */ }

      onApiKeySaved(newKey);
      setEditingKey(false);
      setKeyShown(false);
      setKeySaved(true);
      setTimeout(() => setKeySaved(false), 1200);
      toast('API 密钥已更新');
    } catch (e: any) {
      toast('密钥更新失败: ' + (e?.message ?? String(e)));
    } finally {
      setKeySaving(false);
    }
  }

  function onKeyEditIconClick() {
    if (!editingKey) return startEditKey();
    if (keyChanged) return saveApiKey();
    cancelEditKey();
  }

  function onKeyInputKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (keyChanged) saveApiKey();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      cancelEditKey();
    }
  }

  async function toggleCheck() {
    if (actionInFlight) return;
    setActionInFlight(true);
    try {
      const path = isChecking ? '/api/force-close' : '/api/trigger-check';
      const raw = await GuiApp.APIProxy('POST', path, '');
      const res = JSON.parse(raw) as { status: number; body: string };
      if (res.status < 300) {
        toast(isChecking ? '已发送停止指令' : '已启动检测任务');
      } else {
        toast(isChecking ? '停止检测失败' : '启动检测失败');
      }
    } catch (e: any) {
      toast('操作失败: ' + (e?.message ?? ''));
    } finally {
      setActionInFlight(false);
    }
  }

  async function handleSelectConfig() {
    let path: string;
    try {
      path = await GuiApp.OpenConfigFile();
    } catch (e: any) {
      toast('打开文件对话框失败: ' + (e?.message ?? '未知错误'));
      return;
    }
    if (!path) return;
    onSelectConfig(path);
  }

  async function enterWebUI() {
    if (launching) return;
    setLaunching(true);
    try {
      await GuiApp.EnterWebUI();
      setTimeout(() => setLaunching(false), 300);
    } catch (e: any) {
      toast('进入管理界面失败: ' + (e?.message ?? ''));
      setLaunching(false);
    }
  }

  async function openSubStore() {
    try {
      await GuiApp.OpenSubStoreWindow();
    } catch (e: any) {
      toast('打开订阅管理失败: ' + (e?.message ?? ''));
    }
  }

  async function openInternalPage(path: string, title: string, size: string = 'medium') {
    const theme = document.documentElement.getAttribute('data-theme') || 'light';
    const separator = path.includes('?') ? '&' : '?';
    const pathWithTheme = path + separator + 'theme=' + theme;
    try {
      await GuiApp.OpenInternalPage(pathWithTheme, title, size);
    } catch (e: any) {
      toast(`打开 ${title} 失败: ` + (e?.message ?? ''));
    }
  }

  /** 打开订阅链接独立窗口 */
  async function openSubLinksWindow() {
    try {
      await GuiApp.OpenSubLinksWindow();
    } catch (e: any) {
      toast('打开订阅链接失败: ' + (e?.message ?? ''));
    }
  }

  // 在系统浏览器中打开 WebUI 方法
  async function openWebUIInBrowser() {
    const port = info.listenPort || '8199';
    const url = `http://localhost:${port}/admin`;
    try {
      await Browser.OpenURL(url);
    } catch {
      // 如果 Wails API 失败，降级使用原生 window.open
      window.open(url, '_blank');
    }
  }

  return (
    <div id="keySection" class="key-section-flex">
      {/* ── 中间：回归原本干净的密钥与路径 ── */}
      <div class="key-body">
        <div class="desk-key-header">
          <span class="label" style="margin-bottom:0;">API 密钥</span>
          <div class="desk-key-tags">
            {info.isFirstRun && (
              <span class="desk-inline-tag is-info" title={`已创建默认配置文件：${info.configPath || 'config/config.yaml'}`}>
                已创建默认配置
              </span>
            )}
            {info.keyIsRandom && (
              <span
                class="desk-inline-tag is-warn"
                title="Web 访问建议固定密钥，点击可立即编辑"
                onClick={startEditKey}
              >
                随机密钥 · 建议固定
              </span>
            )}
          </div>
        </div>

        <div class={`key-wrap${editingKey ? ' editing' : ''}${keySaved ? ' just-saved' : ''}`}>
          {editingKey ? (
            <input
              ref={keyInputRef}
              class="key-text key-text-input"
              type={keyShown ? 'text' : 'password'}
              value={keyDraft}
              disabled={keySaving}
              onInput={e => setKeyDraft((e.target as HTMLInputElement).value)}
              onKeyDown={onKeyInputKeyDown}
              onBlur={cancelEditKey}
            />
          ) : (
            <span
              class={`key-text${keyShown ? '' : ' blur'}`}
              onClick={toggleKey}
              title="点击显示/隐藏"
            >
              {currentKey}
            </span>
          )}

          <button
            class={`icon-btn${keyChanged ? ' btn-save-active' : ''}`}
            onMouseDown={e => e.preventDefault()}
            onClick={onKeyEditIconClick}
            disabled={keySaving}
            title={editingKey ? (keyChanged ? '保存密钥' : '取消编辑') : '修改 API 密钥'}
          >
            {keyChanged ? (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" />
                <polyline points="17 21 17 13 7 13 7 21" />
                <polyline points="7 3 7 8 15 8" />
              </svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <path d="M12 20h9" />
                <path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z" />
              </svg>
            )}
          </button>

          <button
            class="icon-btn"
            onMouseDown={e => e.preventDefault()}
            onClick={() => setKeyShown(v => !v)}
            title="显示/隐藏"
          >
            {!keyShown ? (
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2">
                <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8
                  a18.45 18.45 0 0 1 5.06-5.94" />
                <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8
                  a18.5 18.5 0 0 1-2.16 3.19" />
                <line x1="1" y1="1" x2="23" y2="23" />
              </svg>
            ) : (
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2">
                <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                <circle cx="12" cy="12" r="3" />
              </svg>
            )}
          </button>

          <button
            class="icon-btn"
            onMouseDown={e => e.preventDefault()}
            onClick={copyKey}
            title="复制密钥"
          >
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none"
              stroke="currentColor" stroke-width="2">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
            </svg>
          </button>
        </div>

        {info.configPath && (
          <div class="cfg-path-row">
            <svg class="cfg-path-icon" width="11" height="11" viewBox="0 0 24 24" fill="none"
              stroke="currentColor" stroke-width="2" stroke-linecap="round">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
              <polyline points="14 2 14 8 20 8" />
            </svg>
            <span class="cfg-path-text" ref={pathRef as any} title={info.configPath}>
              {pathDisplay}
            </span>
            <button class="icon-btn cfg-path-btn" onClick={handleSelectConfig} title="选择其他配置文件">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2.5" stroke-linecap="round">
                <line x1="12" y1="5" x2="12" y2="19" />
                <line x1="5" y1="12" x2="19" y2="12" />
              </svg>
            </button>
          </div>
        )}
      </div>

      {/* ── 下方：快捷图标 + 靠下的主按钮行（进度嵌入按钮背景）── */}
      <div class="enter-spacer">
        <div class="quick-btn-area">
          <div class="btn-quick-group">
            <button
              class="btn-quick"
              onClick={openWebUIInBrowser}
              title={`在系统浏览器中打开 WebUI (端口 ${info.listenPort})`}
              disabled={launching}
            >
              <svg class="sidebar-icon" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                <circle cx="12" cy="12" r="10" />
                <line x1="2" y1="12" x2="22" y2="12" />
                <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
              </svg>
            </button>
            {/* Sub-Store 订阅管理按钮 */}
            {info.subStorePort && (
              <button
                class="btn-quick"
                onClick={openSubStore}
                title={`打开 Sub-Store 订阅管理 (端口 ${info.subStorePort})`}
              >
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 108 108" class="sidebar-icon" fill="currentColor">
                  <path d="M12.6 35C8.2 21.8 21 8.5 34.3 12.5c3.4 1 8.2 4.9 15.2 11.8l10.2 10.3-2.8 2.8-2.8 2.8-10-9.9c-8.2-8.2-10.7-9.9-14.2-9.9-9.2 0-12.5 10.6-5.4 17.4l3.8 3.8-2.8 3-2.8 3-4.2-4.1c-2.3-2.2-4.9-6-5.6-8.4h-.2z" />
                  <path d="M48.1 46.5l-7.4 7.6 3.8 3.8 3.8 3.8-2.8 2.8-2.8 3-6.7-6.8-6.8-6.7 6.4-6.4c3.4-3.4 6.7-6.4 7.2-6.4s2 1.8 5.6 5.2zM59.7 46.5l7.4 7.6-3.8 3.8-3.8 3.8 2.8 2.8 2.8 3 6.7-6.8 6.8-6.7-6.4-6.4c-3.4-3.4-6.7-6.4-7.2-6.4s-2 1.8-5.6 5.2zM24.4 70.4c-4.5 5.2-5 10.8-1.3 14.6 4 4 10.3 3.4 14.8-1.3l3.8-3.8 3 2.8 3 2.8-4.1 4.2c-8 8.2-18.4 8.8-26 1-7.7-7.6-7.4-17.5.9-26l4-4.2 3 2.8 2.8 2.7-3.8 4.4zM83.6 37.6c4.5-5.2 5-10.8 1.3-14.6-4-4-10.3-3.4-14.8 1.3l-3.8 3.8-3-2.8-3-2.8 4.1-4.2c8-8.2 18.4-8.8 26-1 7.7 7.6 7.4 17.5-.9 26l-4 4.2-3-2.8-2.8-2.7 3.8-4.4z" />
                  <path d="M95.4 73c4.4 13.3-8.4 26.5-21.6 22.5-3.4-1-8.2-4.9-15.2-11.8L48.4 73.4l2.8-2.8 2.8-2.8 10 9.9c8.2 8.2 10.7 9.9 14.2 9.9 9.2 0 12.5-10.6 5.4-17.4l-3.8-3.8 2.8-3 2.8-3 4.2 4.1c2.3 2.2 4.9 6 5.6 8.4z" />
                </svg>
              </button>
            )}

            {/* 内置文件 */}
            <button
              class="btn-quick"
              onClick={() => openInternalPage('/files', '内置文件', 'window-file')}
              title="内置文件管理"
              disabled={launching}
            >
              <svg class="sidebar-icon" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">
                <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
              </svg>
            </button>

            {/* 节点分析报告 */}
            <button
              class="btn-quick"
              onClick={() => openInternalPage('/analysis', '节点分析报告', 'wide')}
              title="节点分析报告"
              disabled={launching}
            >
              <svg class="sidebar-icon" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">
                <line x1="18" y1="20" x2="18" y2="10" />
                <line x1="12" y1="20" x2="12" y2="4" />
                <line x1="6" y1="20" x2="6" y2="14" />
                <line x1="2" y1="20" x2="22" y2="20" />
              </svg>
            </button>

            {/* 订阅链接（仅配置了 Sub-Store 端口时显示） */}
            {info.subStorePort && (
              <button
                class="btn-quick"
                onClick={openSubLinksWindow}
                title="订阅链接"
                disabled={launching}
              >
                <svg class="sidebar-icon" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none"
                  stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
                  <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
                </svg>
              </button>
            )}

            <button
              class="btn-quick"
              onClick={onOpenLogs}
              title="查看实时运行日志"
              disabled={launching}
            >
              <svg class="sidebar-icon" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none"
                stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">
                <polyline points="4 17 10 11 4 5" />
                <line x1="12" y1="19" x2="20" y2="19" />
              </svg>
            </button>
          </div>
        </div>

        <div class="enter-action-row">
          <button
            class={`btn-circle-action ${isChecking ? 'btn-stop' : 'btn-play'}`}
            onClick={toggleCheck}
            disabled={actionInFlight}
            title={isChecking ? '停止当前检测' : '立即开始检测'}
          >
            {isChecking ? (
              <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor">
                <rect x="6" y="6" width="12" height="12" rx="2" />
              </svg>
            ) : (
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" style="margin-left:2px;">
                <path d="M8 5v14l11-7z" />
              </svg>
            )}
          </button>

          <button
            class={`btn-enter${isChecking ? ' is-checking' : ''}`}
            onClick={enterWebUI}
            disabled={launching}
            title={isChecking ? `${status?.stepName || '检测中'} ${Math.round(progressPercent)}%` : '进入管理界面'}
          >
            {isChecking && (
              <span class="btn-enter-progress" style={{ width: `${progressPercent}%` }} />
            )}
            <span class="btn-enter-label">
              {launching ? '正在进入…' : '进入管理界面 →'}
            </span>
          </button>

          <button
            class="btn-circle-action btn-about"
            onClick={() => GuiApp.OpenAboutWindow()}
            title="关于 Subs Free"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
              stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <circle cx="12" cy="12" r="10" />
              <line x1="12" y1="16" x2="12" y2="12" />
              <line x1="12" y1="8" x2="12.01" y2="8" />
            </svg>
          </button>
        </div>
      </div>

      {/* ── 分割线下方：4 项单行指标（与左侧 .brand-links 精确水平对齐）── */}
      <div class="desk-quiet-footer">
        {isChecking ? (
          <div class="desk-pipe-grid">
            <div class="desk-pipe-cell">
              <span class="desk-pv is-accent">{status?.available || 0}</span>
              <span class="desk-pl">可用</span>
            </div>
            <div class="desk-pipe-cell">
              <span class="desk-pv">{status?.progress || 0}</span>
              <span class="desk-pl">已测</span>
            </div>
            <div class="desk-pipe-cell">
              <span class="desk-pv">{status?.proxyCount || 0}</span>
              <span class="desk-pl">总数</span>
            </div>
            <div class="desk-pipe-cell">
              <span class="desk-pv">{Math.round(progressPercent)}%</span>
              <span class="desk-pl">进度</span>
            </div>
          </div>
        ) : lastStats ? (
          <div class="desk-pipe-grid">
            <div class="desk-pipe-cell" title="可用节点">
              <span class="desk-pv">{lastStats.available}</span>
              <span class="desk-pl">可用</span>
            </div>
            <div class="desk-pipe-cell" title="检测数量">
              <span class="desk-pv">{lastStats.total}</span>
              <span class="desk-pl">节点</span>
            </div>
            <div class="desk-pipe-cell" title="检测耗时">
              <span class="desk-pv">{lastStats.duration}</span>
            </div>
            <div class="desk-pipe-cell" title="消耗流量">
              <span class="desk-pv">{lastStats.traffic}</span>
            </div>
          </div>
        ) : (
          <div class="desk-pipe-empty">
            {status?.lastResult || '尚无检测记录 · 点击左侧开始按钮即可检测'}
          </div>
        )}
      </div>
    </div>
  );
}