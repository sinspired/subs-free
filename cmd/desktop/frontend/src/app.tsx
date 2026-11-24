/**
 * frontend/src/app.tsx
 * 桌面主界面根组件 — Wails3 + Preact + TypeScript
 */
import { useEffect, useState, useRef } from 'preact/hooks';
import { Events } from '@wailsio/runtime';

import { useTheme } from './hooks/useTheme';
import { useToast } from './hooks/useToast';
import { useWailsReady } from './hooks/useWailsReady';

import { Header } from './components/Header';
import { KeySection, DesktopCheckState, LastStats } from './components/KeySection';
import { PortConflict } from './components/PortConflict';
import { PasswordConfirm } from './components/PasswordConfirm';
import { Toast } from './components/Toast';
import { QuitDialog } from './components/QuitDialog';

import { GuiApp } from '../bindings/github.com/sinspired/subs-free/cmd/desktop';
import { AppInfo } from '../bindings/github.com/sinspired/subs-free/cmd/desktop';
import { Notifier } from '../bindings/github.com/sinspired/subs-free/cmd/desktop';

// UI 状态机：每个状态对应一个独立视图
type View = 'loading' | 'error' | 'portConflict' | 'main' | 'password' | 'about';

// 在 Wails 无地址栏窗口中打开链接（不唤起系统浏览器）。
// 若 Go 调用失败（极少情况），降级到系统浏览器作为兜底。
async function openLink(
  url: string,
  windowSize: 'extraLarge' | 'large' | 'medium' | 'small' | 'tiny' | 'wide' = 'medium',
) {
  try {
    await GuiApp.OpenBrandURL(url, windowSize);
  } catch {
    window.open(url, '_blank');
  }
}

// ── 实时日志轻量解析 ──────────────────────────────────────────────
const LOG_NOISE_RE = /\[WebView2\]|received DATA after END_STREAM/;
const LOG_ANSI_RE = /\x1b\[[\d;]*m/g;
const LOG_TS_RE = /^\[?((?:\d{4}[-/])?\d{2}[-/]\d{2}[T\s]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)\]?/;
const LOG_LVL_RE = /^\s*\[?(INF|INFO|WRN|WARN|ERR|ERROR|DBG|DEBUG)\]?(?=\s|$)/;
const LOG_BODY_TOKEN_RE = /(https?:\/\/[^\s"'\\<>]+)|(^|\s)([a-zA-Z0-9_\u4e00-\u9fa5.\-[\]]+)=/g;

type LogLevelKind = 'info' | 'warn' | 'err' | 'dbg';

interface ParsedLogLine {
  time: string;
  level: string;
  levelKind?: LogLevelKind;
  tokens: Array<{ type: 'text' | 'key' | 'url'; value: string }>;
}

function normalizeLogLevel(raw: string): { label: string; kind: LogLevelKind } {
  switch (raw) {
    case 'ERR':
    case 'ERROR':
      return { label: 'ERR', kind: 'err' };
    case 'WRN':
    case 'WARN':
      return { label: 'WRN', kind: 'warn' };
    case 'DBG':
    case 'DEBUG':
      return { label: 'DBG', kind: 'dbg' };
    default:
      return { label: 'INF', kind: 'info' };
  }
}

function tokenizeLogBody(body: string): ParsedLogLine['tokens'] {
  const tokens: ParsedLogLine['tokens'] = [];
  let lastIndex = 0;
  LOG_BODY_TOKEN_RE.lastIndex = 0;

  let m: RegExpExecArray | null;
  while ((m = LOG_BODY_TOKEN_RE.exec(body)) !== null) {
    const [full, url, space, key] = m;
    if (m.index > lastIndex) {
      tokens.push({ type: 'text', value: body.slice(lastIndex, m.index) });
    }
    if (url) {
      tokens.push({ type: 'url', value: url });
    } else if (key) {
      if (space) tokens.push({ type: 'text', value: space });
      tokens.push({ type: 'key', value: `${key}=` });
    } else {
      tokens.push({ type: 'text', value: full });
    }
    lastIndex = m.index + full.length;
  }

  if (lastIndex < body.length) {
    tokens.push({ type: 'text', value: body.slice(lastIndex) });
  }
  return tokens;
}

function parseLogLine(line: string): ParsedLogLine {
  let rest = line.replace(LOG_ANSI_RE, '');
  let time = '';

  const tsMatch = rest.match(LOG_TS_RE);
  if (tsMatch) {
    time = tsMatch[1];
    rest = rest.slice(tsMatch[0].length);
  }

  let level = '';
  let levelKind: LogLevelKind | undefined;
  const lvlMatch = rest.match(LOG_LVL_RE);
  if (lvlMatch) {
    const norm = normalizeLogLevel(lvlMatch[1]);
    level = norm.label;
    levelKind = norm.kind;
    rest = rest.slice(lvlMatch[0].length).trimStart();
  } else {
    rest = rest.trimStart();
  }

  return {
    time,
    level,
    levelKind,
    tokens: tokenizeLogBody(rest),
  };
}

export function App() {
  const ready = useWailsReady();
  const { theme, toggleTheme, syncFromServer } = useTheme();
  const { msg, visible, toast } = useToast();
  const isDark = theme === 'dark';

  const [view, setView] = useState<View>('loading');
  const [info, setInfo] = useState<AppInfo | null>(null);
  const [errMsg, setErrMsg] = useState('');
  const [cfgPath, setCfgPath] = useState('');
  const [showQuit, setShowQuit] = useState(false);

  // 自启与设置菜单状态
  const [autostartEnabled, setAutostart] = useState(false);
  const [silentAutostart, setSilentAutostart] = useState(false);
  const [showSettings, setShowSettings] = useState(false);

  // 更新状态
  const [updateAvailable, setUpdateAvailable] = useState(false);
  const [updateVersion, setUpdateVersion] = useState('');

  // 更新器设置弹窗状态
  const [showUpdaterModal, setShowUpdaterModal] = useState(false);
  const [updaterPrerelease, setUpdaterPrerelease] = useState(false);
  const [updaterToken, setUpdaterToken] = useState('');
  const [updaterDraftPre, setUpdaterDraftPre] = useState(false);
  const [updaterDraftToken, setUpdaterDraftToken] = useState('');
  const [hasBackendToken, setHasBackendToken] = useState(false);
  const [effectiveSource, setEffectiveSource] = useState<'frontend' | 'backend' | 'none'>('none');
  const [showUpdaterToken, setShowUpdaterToken] = useState(false);
  const [savingUpdater, setSavingUpdater] = useState(false);

  // 主界面检测状态看板与快捷日志状态
  const [checkStatus, setCheckStatus] = useState<DesktopCheckState | null>(null);
  const [lastStats, setLastStats] = useState<LastStats | null>(null);
  const [finalizing, setFinalizing] = useState(false);
  const [showLogModal, setShowLogModal] = useState(false);
  const [logLines, setLogLines] = useState<string[]>([]);
  const logBodyRef = useRef<HTMLDivElement>(null);

  const [logAutoFollow, setLogAutoFollow] = useState(true);
  const [logPaused, setLogPaused] = useState(false);
  const [hasNewLogs, setHasNewLogs] = useState(false);

  const logLinesRef = useRef<string[]>([]); // ← 补上这一行
  const logAutoFollowRef = useRef(true);
  const logPausedRef = useRef(false);
  const isAutoScrollingRef = useRef(false);
  const pendingLogsRef = useRef<string[] | null>(null);

  logLinesRef.current = logLines;
  logAutoFollowRef.current = logAutoFollow;
  logPausedRef.current = logPaused;

  async function syncUpdaterSettings() {
    try {
      const s = await GuiApp.GetUpdaterSettings();
      setUpdaterToken(s.token || '');
      setUpdaterPrerelease(!!s.prerelease);
      setHasBackendToken(!!s.hasBackendToken);
      setEffectiveSource((s.effectiveSource as 'frontend' | 'backend' | 'none') || 'none');
      return s;
    } catch {
      return null;
    }
  }

  async function fetchLastCheckStats() {
    try {
      const raw = await GuiApp.APIProxy('GET', '/api/analysis-report', '');
      const res = JSON.parse(raw) as { status: number; body: string };
      if (res.status >= 300 || !res.body) return;
      const bodyObj = JSON.parse(res.body);
      const yaml: string = bodyObj?.report || '';
      if (!yaml) return;

      const duration = yaml.match(/check_duration_raw:\s*(\d+)/)?.[1];
      const total = yaml.match(/check_count_raw:\s*(\d+)/)?.[1];
      const available = yaml.match(/alive_count:\s*(\d+)/)?.[1];
      const traffic = yaml.match(/check_traffic_total:\s*(.+)/)?.[1];
      let time = yaml.match(/check_time_raw:\s*['"]?(.*?)['"]?(?:\n|$)/)?.[1];

      if (time) {
        const d = new Date(time.replace(' ', 'T'));
        if (!isNaN(d.getTime())) {
          time = `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
        }
      }
      if (total && available) {
        setLastStats({
          time: time || '-',
          duration: duration
            ? (parseInt(duration) >= 60 ? `${Math.floor(parseInt(duration) / 60)} 分` : `${duration} 秒`)
            : '0 秒',
          total: parseInt(total) >= 10000 ? (parseInt(total) / 10000).toFixed(1) + '万' : total,
          available,
          traffic: traffic || '-',
        });
      }
    } catch { /* 忽略 */ }
  }

  async function fetchLogsOnce(forceApply = false) {
    try {
      const raw = await GuiApp.APIProxy('GET', '/api/logs', '');
      const res = JSON.parse(raw) as { status: number; body: string };
      if (res.status >= 300 || !res.body) return;
      const parsed = JSON.parse(res.body);
      const lines: string[] = Array.isArray(parsed?.logs)
        ? parsed.logs.map(String)
        : typeof parsed?.logs === 'string'
          ? parsed.logs.split('\n')
          : [];
      const next = lines
        .map(l => l.replace(LOG_ANSI_RE, ''))
        .filter(l => l.trim() !== '' && !LOG_NOISE_RE.test(l))
        .slice(-150);

      // 内容无变化时直接跳过，避免无意义重渲染
      const cur = logLinesRef.current;
      const isSame = cur.length === next.length && cur.every((v, i) => v === next[i]);
      if (isSame) return;

      // 用户正在日志区划词选中文本时，暂缓刷新列表
      const sel = window.getSelection();
      const isSelectingInLogs =
        !!sel &&
        !sel.isCollapsed &&
        !!logBodyRef.current &&
        logBodyRef.current.contains(sel.anchorNode);

      if (!forceApply && (!logAutoFollowRef.current || logPausedRef.current || isSelectingInLogs)) {
        pendingLogsRef.current = next;
        setHasNewLogs(true);
        return;
      }

      pendingLogsRef.current = null;
      setHasNewLogs(false);
      setLogLines(next);
    } catch { /* 忽略 */ }
  }

  function scrollToLogBottom() {
    if (pendingLogsRef.current) {
      setLogLines(pendingLogsRef.current);
      pendingLogsRef.current = null;
    }
    setHasNewLogs(false);
    setLogPaused(false);
    setLogAutoFollow(true);
    logPausedRef.current = false;
    logAutoFollowRef.current = true;

    requestAnimationFrame(() => {
      const el = logBodyRef.current;
      if (!el) return;
      isAutoScrollingRef.current = true;
      el.scrollTop = el.scrollHeight;
      requestAnimationFrame(() => {
        isAutoScrollingRef.current = false;
      });
    });
  }

  function handleLogScroll() {
    const el = logBodyRef.current;
    if (!el || isAutoScrollingRef.current) return;

    // 留 24px 容差，兼容 Windows 125%/150% 缩放下的小数像素偏差
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight <= 24;
    if (atBottom && !logAutoFollowRef.current) {
      setLogAutoFollow(true);
      logAutoFollowRef.current = true;
      if (pendingLogsRef.current) {
        setLogLines(pendingLogsRef.current);
        pendingLogsRef.current = null;
        setHasNewLogs(false);
      }
    } else if (!atBottom && logAutoFollowRef.current) {
      setLogAutoFollow(false);
      logAutoFollowRef.current = false;
    }
  }

  // ── Wails 就绪后立即拉取应用信息与偏好设置 ─────────────────────
  useEffect(() => {
    if (!ready) return;
    loadAppInfo();
    syncUpdaterSettings();

    GuiApp.GetAutoStartEnabled()
      .then(enabled => setAutostart(enabled))
      .catch(() => { /* 平台不支持时静默忽略 */ });

    GuiApp.GetSilentAutoStartEnabled()
      .then(silent => setSilentAutostart(silent))
      .catch(() => { /* 静默忽略 */ });
  }, [ready]);

  // ── 监听"窗口关闭"事件 ────────────────────────────────────────
  useEffect(() => {
    if (!ready) return;
    const unsub = Events.On('window:close-requested', () => {
      setShowQuit(true);
    });
    return () => { unsub && unsub(); };
  }, [ready]);

  // ── 监听更新状态事件 ──────────────────────────────────────────
  useEffect(() => {
    if (!ready) return;

    const syncUpdateState = async () => {
      try {
        const st = await GuiApp.GetUpdateStatus();
        setUpdateAvailable(!!st.available);
        setUpdateVersion(st.version || '');
      } catch {
        // 忽略
      }
    };

    syncUpdateState();

    const unsubAvailable = Events.On('wails:updater:update-available', (event) => {
      const rel: any = event?.data ?? {};
      setUpdateAvailable(true);
      setUpdateVersion(rel.version ?? rel.Version ?? '');
    });

    const unsubNoUpdate = Events.On('wails:updater:no-update', () => {
      setUpdateAvailable(false);
      setUpdateVersion('');
    });

    const unsubReady = Events.On('wails:updater:update-ready', (event) => {
      const rel: any = event?.data ?? {};
      setUpdateAvailable(true);
      setUpdateVersion(rel.version ?? rel.Version ?? '');
    });

    const unsubToast = Events.On('gui:update:toast', (event) => {
      const text = (event?.data as string) ?? '';
      if (text) toast(text);
    });

    const unsubStatusChanged = Events.On('gui:update:status-changed', (event) => {
      const st: any = event?.data ?? {};
      setUpdateAvailable(!!(st.available ?? st.Available));
      setUpdateVersion((st.version ?? st.Version) || '');
    });

    return () => {
      unsubAvailable && unsubAvailable();
      unsubNoUpdate && unsubNoUpdate();
      unsubReady && unsubReady();
      unsubToast && unsubToast();
      unsubStatusChanged && unsubStatusChanged();
    };
  }, [ready]);

  // ── 监听托盘「开机自启 / 静默自启」切换事件 ───────────────────
  useEffect(() => {
    if (!ready) return;
    const unsub = Events.On('autostart:changed', async () => {
      try {
        const [actual, silent] = await Promise.all([
          GuiApp.GetAutoStartEnabled(),
          GuiApp.GetSilentAutoStartEnabled(),
        ]);
        setAutostart(actual);
        setSilentAutostart(silent);
      } catch { /* 忽略 */ }
    });
    return () => { unsub && unsub(); };
  }, [ready]);

  // ── 主界面检测状态轮询（窗口隐藏/最小化时自动暂停）─────────────
  useEffect(() => {
    if (!ready || view !== 'main') return;
    let timer: number | undefined;

    const poll = async () => {
      if (document.hidden) return;
      try {
        const next = await GuiApp.GetCheckState();
        setCheckStatus(prev => {
          if (prev?.isChecking && !next.isChecking) {
            setFinalizing(true);
            fetchLastCheckStats().finally(() => setFinalizing(false));
          }
          return next;
        });
      } catch { /* 忽略 */ }
    };

    poll();
    fetchLastCheckStats();
    timer = window.setInterval(poll, 1000);

    const onVis = () => {
      if (!document.hidden) poll();
    };
    document.addEventListener('visibilitychange', onVis);

    return () => {
      if (timer) window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVis);
    };
  }, [ready, view]);

  // ── 快捷日志弹窗打开时才轮询日志，关闭即销毁定时器 ────────────
  useEffect(() => {
    if (!showLogModal) return;
    setLogAutoFollow(true);
    setLogPaused(false);
    setHasNewLogs(false);
    pendingLogsRef.current = null;
    logAutoFollowRef.current = true;
    logPausedRef.current = false;

    fetchLogsOnce(true);
    const timer = window.setInterval(() => {
      if (!document.hidden && !logPausedRef.current) {
        fetchLogsOnce();
      }
    }, 1500);
    return () => window.clearInterval(timer);
  }, [showLogModal]);

  useEffect(() => {
    if (!showLogModal || !logAutoFollowRef.current || !logBodyRef.current) return;
    const el = logBodyRef.current;
    isAutoScrollingRef.current = true;
    el.scrollTop = el.scrollHeight;
    requestAnimationFrame(() => {
      isAutoScrollingRef.current = false;
    });
  }, [logLines, showLogModal]);

  // ── 点击外部或按 Esc 自动关闭设置菜单 / 弹窗 ──────────────────
  useEffect(() => {
    if (!showSettings && !showUpdaterModal && !showLogModal) return;
    const onDown = (e: MouseEvent) => {
      if (!showSettings) return;
      const t = e.target as HTMLElement | null;
      if (t?.closest('.brand-settings-menu') || t?.closest('.brand-settings-btn')) return;
      setShowSettings(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setShowSettings(false);
        setShowUpdaterModal(false);
        setShowLogModal(false);
      }
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [showSettings, showUpdaterModal, showLogModal]);

  async function loadAppInfo() {
    setView('loading');
    try {
      const data = await GuiApp.GetAppInfo();

      (window as any).__CORE_BASE_URL = `http://127.0.0.1:${data.listenPort}`;
      syncFromServer();

      setInfo(data);
      if (data.portConflictHTTP || data.portConflictSubStore) {
        setView('portConflict');
      } else {
        setView('main');
      }
    } catch (e: any) {
      setErrMsg(e?.message ?? '未知错误');
      setView('error');
    }
  }

  function handlePortsFixed(newInfo: AppInfo) {
    (window as any).__CORE_BASE_URL = `http://127.0.0.1:${newInfo.listenPort}`;
    syncFromServer();
    syncUpdaterSettings();
    setLastStats(null);
    setInfo(newInfo);
    setView('main');
  }

  function handleSelectConfig(path: string) {
    setCfgPath(path);
    setView('password');
  }

  function handlePasswordDone(newInfo: AppInfo | null) {
    if (newInfo) {
      (window as any).__CORE_BASE_URL = `http://127.0.0.1:${newInfo.listenPort}`;
      syncFromServer();
      syncUpdaterSettings();
      setLastStats(null);
      setInfo(newInfo);
    }
    setView('main');
  }

  function handlePasswordBack() { setView('main'); }
  function handlePasswordReselect(path: string) { setCfgPath(path); }
  const requestClose = () => setShowQuit(true);

  async function handleToggleAutostart() {
    const next = !autostartEnabled;
    try {
      await GuiApp.SetAutoStart(next);
      const actual = await GuiApp.GetAutoStartEnabled();
      setAutostart(actual);
      toast(actual ? '已设置开机自启' : '已取消开机自启');
      Notifier.SendOSNotification(actual ? '已设置开机自启' : '已取消开机自启', '');
    } catch (e: any) {
      toast('设置失败：' + (e?.message ?? '功能暂不可用'));
    }
  }

  async function handleToggleSilentAutostart() {
    const next = !silentAutostart;
    try {
      await GuiApp.SetSilentAutoStart(next);
      const actual = await GuiApp.GetSilentAutoStartEnabled();
      setSilentAutostart(actual);
      toast(actual ? '已开启静默自启' : '已关闭静默自启');
    } catch (e: any) {
      toast('设置失败：' + (e?.message ?? '功能暂不可用'));
    }
  }

  async function openUpdaterModal() {
    setShowSettings(false);
    const s = await syncUpdaterSettings();
    setUpdaterDraftToken(s ? (s.token || '') : updaterToken);
    setUpdaterDraftPre(s ? !!s.prerelease : updaterPrerelease);
    setShowUpdaterToken(false);
    setShowUpdaterModal(true);
  }

  async function handleSaveUpdaterSettings(checkNow = false) {
    if (savingUpdater) return;
    setSavingUpdater(true);
    try {
      const s = await GuiApp.SaveUpdaterSettings(updaterDraftToken, updaterDraftPre);
      setUpdaterToken(s.token || '');
      setUpdaterPrerelease(!!s.prerelease);
      setHasBackendToken(!!s.hasBackendToken);
      setEffectiveSource((s.effectiveSource as 'frontend' | 'backend' | 'none') || 'none');
      setShowUpdaterModal(false);
      toast('更新器设置已保存');
      if (checkNow) {
        await GuiApp.CheckForUpdates();
      }
    } catch (e: any) {
      toast('保存失败：' + (e?.message ?? '未知错误'));
    } finally {
      setSavingUpdater(false);
    }
  }

  async function handleCheckForUpdates() {
    try {
      await GuiApp.CheckForUpdates();
    } catch (e: any) {
      toast('检查更新失败：' + (e?.message ?? ''));
    }
  }

  // ── 左侧品牌面板（函数式渲染，避免切换菜单时重挂载 Logo）──────
  const renderBrandPanel = () => (
    <aside class="brand-panel">
      {/* 品牌主体：大 Logo + 品牌下方版本标签 */}
      <div class="brand-body">
        <img src="/logo.svg" alt="logo" class="brand-icon" />
      </div>

      {/* 选项弹出菜单 */}
      {showSettings && (
        <div class="brand-settings-menu">
          <div class="bsm-item" onClick={handleToggleAutostart}>
            <div class="bsm-text">
              <span class="bsm-title">开机自启</span>
              <span class="bsm-sub">登录系统后自动启动</span>
            </div>
            <span class={`bsm-switch${autostartEnabled ? ' on' : ''}`} />
          </div>

          {autostartEnabled && (
            <div class="bsm-item bsm-sub-item" onClick={handleToggleSilentAutostart}>
              <div class="bsm-text">
                <span class="bsm-title">静默自启</span>
                <span class="bsm-sub">自启时隐藏窗口进托盘</span>
              </div>
              <span class={`bsm-switch${silentAutostart ? ' on' : ''}`} />
            </div>
          )}

          <div class="bsm-divider" />

          <button class="bsm-action" onClick={openUpdaterModal}>
            <div class="bsm-text">
              <span class="bsm-title">更新器设置</span>
              <span class="bsm-sub">
                {updaterPrerelease ? '预览版' : '稳定版'} ·{' '}
                {effectiveSource === 'frontend'
                  ? '前端 Token'
                  : effectiveSource === 'backend'
                    ? '后端 Token'
                    : '未设 Token'}
              </span>
            </div>
            <span class="bsm-arrow">›</span>
          </button>

          <div class="bsm-divider" />

          <button
            class="bsm-action"
            onClick={() => {
              setShowSettings(false);
              GuiApp.HideToTray();
            }}
          >
            <div class="bsm-text">
              <span class="bsm-title">后台运行</span>
              <span class="bsm-sub">关闭界面，仅留托盘与内核</span>
            </div>
          </button>
        </div>
      )}
      {/* 左栏分割线上方：居中静音展示当前状态与检测时间 */}
      {view === 'main' && (
        <div class="brand-status-block">
          <div class={`brand-status-pill${checkStatus?.isChecking || finalizing ? ' is-active' : ''}`}>
            <span class={`desk-status-dot${checkStatus?.isChecking || finalizing ? ' checking' : ''}`} />
            <span class="brand-status-text">
              {checkStatus?.isChecking
                ? (checkStatus.stepName || '检测中')
                : finalizing
                  ? '正在整理结果…'
                  : '空闲'}
            </span>
          </div>
          <div class="brand-status-sub">
            {lastStats ? `最后 ${lastStats.time}` : (checkStatus?.lastResult || '尚无检测记录')}
            {checkStatus?.nextCheckTime ? ` · 下次 ${checkStatus.nextCheckTime}` : ''}
          </div>
        </div>
      )}


      {/* ── 底部工具行：设置 | github tg docker | 检查更新 */}
      <nav class="brand-links">
        {/* 设置菜单按钮 */}
        <button
          class={`brand-autostart brand-settings-btn${showSettings ? ' open' : ''}${autostartEnabled ? ' active' : ''}`}
          onClick={() => setShowSettings(v => !v)}
          title="启动、更新器与后台选项"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
            stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
            <circle cx="12" cy="12" r="3" />
          </svg>
        </button>

        {/* 竖线分割 */}
        <span class="brand-sep" />

        {/* 社交链接 */}
        <a
          class="brand-link"
          onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro')}
          title="GitHub 仓库，欢迎 Star 和提 Issue"
        >
          <img src="/github.svg" alt="GitHub" class="brand-social-icon" />
        </a>
        <a
          class="brand-link"
          onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://t.me/subs_check_pro', 'tiny')}
          title="Telegram 群组，建议加入以获取最新动态和使用帮助"
        >
          <img src="/telegram.svg" alt="Telegram" class="brand-social-icon" />
        </a>
        <a
          class="brand-link"
          onClick={() => openLink('https://hub.docker.com/r/sinspired/subs-check-pro')}
          title="Docker Hub 仓库，提供官方镜像"
        >
          <img src="/docker.svg" alt="Docker" class="brand-social-icon" />
        </a>

        {/* 检查更新按钮 */}
        <button
          class={`brand-autostart${updateAvailable ? ' update-available' : ''}`}
          title={updateAvailable ? `有可用更新${updateVersion ? ` (${updateVersion})` : ''}，点击安装` : '检查更新'}
          onClick={handleCheckForUpdates}
        >
          <svg viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg" width="15" height="15">
            <path d="M520.533 460.8l-179.2 170.667h358.4L520.533 460.8zm-52.906 170.65v204.817H290.133c-122.47 0-221.866-103.766-221.866-231.63 0-105.13 67.003-193.62 158.856-222.344C275.046 267.861 384.65 187.733 512 187.733s236.954 80.128 284.877 194.56C888.73 410.54 955.733 499.49 955.733 604.638c0 127.863-99.396 231.629-221.866 231.629H556.373V631.45" fill="currentColor" />
          </svg>
        </button>
      </nav>
    </aside>
  );

  // ── 右侧顶部工具栏：左侧端口状态 + 拖拽区 + 右侧主题切换 ──
  const PanelToolbar = ({ portInfo }: { portInfo?: AppInfo | null }) => (
    <div class="lp-toolbar">
      {portInfo && (
        <div class="lp-ports">
          <span class="port-badge">
            <span class="port-dot" />
            <span class="port-badge-lbl">HTTP</span>
            <span class="port-badge-val">{portInfo.listenPort || '8199'}</span>
          </span>
          {portInfo.subStorePort && (
            <span class="port-badge">
              <span class="port-dot" />
              <span class="port-badge-lbl">Sub-Store</span>
              <span class="port-badge-val">{portInfo.subStorePort}</span>
            </span>
          )}
        </div>
      )}

      <div class="lp-drag-area" />

      <button class="icon-btn theme-btn" onClick={toggleTheme} title="切换主题">
        {!isDark ? (
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none"
            stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
          </svg>
        ) : (
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none"
            stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <circle cx="12" cy="12" r="5" />
            <line x1="12" y1="1" x2="12" y2="3" />
            <line x1="12" y1="21" x2="12" y2="23" />
            <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
            <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
            <line x1="1" y1="12" x2="3" y2="12" />
            <line x1="21" y1="12" x2="23" y2="12" />
            <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
            <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
          </svg>
        )}
      </button>
    </div>
  );

  return (
    <>
      {/* ── loading ── */}
      {view === 'loading' && (
        <div class="page">
          <div class="card">
            <Header theme={theme} toggleTheme={toggleTheme} onRequestClose={requestClose} />
            <div class="state-box">
              <div class="spinner" />
              <span>正在加载应用信息…</span>
            </div>
          </div>
        </div>
      )}

      {/* ── error ── */}
      {view === 'error' && (
        <div class="page">
          <div class="card">
            <Header theme={theme} toggleTheme={toggleTheme} onRequestClose={requestClose} />
            <div class="state-box" style="color:var(--warn)">
              ⚠️ 初始化失败：{errMsg}
            </div>
          </div>
        </div>
      )}

      {/* ── portConflict ── */}
      {view === 'portConflict' && info && (
        <div class="page split-page">
          {renderBrandPanel()}
          <section class="login-panel">
            <PanelToolbar portInfo={null} />
            <div class="login-content login-content--conflict">
              <PortConflict info={info} toast={toast} onFixed={handlePortsFixed} />
            </div>
          </section>
        </div>
      )}

      {/* ── main（App 主界面）── */}
      {view === 'main' && info && (
        <div class="page split-page">
          {renderBrandPanel()}
          <section class="login-panel">
            <PanelToolbar portInfo={info} />
            <div class="login-content">
              <KeySection
                info={info}
                status={checkStatus}
                lastStats={lastStats}
                finalizing={finalizing}
                toast={toast}
                onSelectConfig={handleSelectConfig}
                onApiKeySaved={newKey =>
                  setInfo(prev => (prev ? { ...prev, apiKey: newKey, keyIsRandom: false } : prev))
                }
                onOpenLogs={() => setShowLogModal(true)}
              />
            </div>
          </section>
        </div>
      )}

      {/* ── password ── */}
      {view === 'password' && (
        <div class="page split-page">
          {renderBrandPanel()}
          <section class="login-panel">
            <PanelToolbar portInfo={info} />
            <div class="login-content">
              <PasswordConfirm
                cfgPath={cfgPath}
                toast={toast}
                onDone={handlePasswordDone}
                onBack={handlePasswordBack}
                onReselect={handlePasswordReselect}
              />
            </div>
          </section>
        </div>
      )}

      <Toast msg={msg} visible={visible} />

      {showQuit && (
        <QuitDialog onClose={() => setShowQuit(false)} />
      )}

      {/* ── 更新器设置弹窗 ── */}
      {showUpdaterModal && (
        <div
          class="quit-overlay"
          onClick={e => {
            if (e.target === e.currentTarget) setShowUpdaterModal(false);
          }}
        >
          <div class="quit-dialog upd-dialog">
            <div class="upd-header">
              <span class="upd-title">更新器设置</span>
              <span class="upd-desc">配置 GUI 检查更新通道与 GitHub Token</span>
            </div>

            <div class="upd-body">
              <div class="upd-row" onClick={() => setUpdaterDraftPre(v => !v)}>
                <div class="bsm-text">
                  <span class="bsm-title">检测预览版</span>
                  <span class="bsm-sub">包含 alpha / beta / rc 预发布版本</span>
                </div>
                <span class={`bsm-switch${updaterDraftPre ? ' on' : ''}`} />
              </div>

              <div class="upd-field">
                <div class="upd-field-head">
                  <span class="bsm-title">GitHub Token</span>
                  <span class="upd-source">
                    {updaterDraftToken.trim()
                      ? '将使用前端设置（优先）'
                      : hasBackendToken
                        ? '留空将使用后端 config.yaml'
                        : '留空则匿名请求（易受限流）'}
                  </span>
                </div>
                <div class="upd-input-wrap">
                  <input
                    class="upd-input"
                    type={showUpdaterToken ? 'text' : 'password'}
                    value={updaterDraftToken}
                    onInput={e => setUpdaterDraftToken((e.target as HTMLInputElement).value)}
                    placeholder={hasBackendToken ? '已继承后端 Token，输入可覆盖' : 'ghp_... / github_pat_...'}
                    spellcheck={false}
                    autocomplete="off"
                  />
                  <button
                    type="button"
                    class="icon-btn upd-eye-btn"
                    onClick={() => setShowUpdaterToken(v => !v)}
                    title={showUpdaterToken ? '隐藏 Token' : '显示 Token'}
                  >
                    {showUpdaterToken ? '隐藏' : '显示'}
                  </button>
                  {updaterDraftToken && (
                    <button
                      type="button"
                      class="icon-btn upd-eye-btn"
                      onClick={() => setUpdaterDraftToken('')}
                      title="清空前端覆盖（回退后端或匿名）"
                    >
                      清空
                    </button>
                  )}
                </div>
              </div>
            </div>

            <div class="upd-footer">
              <button
                class="quit-cancel"
                onClick={() => setShowUpdaterModal(false)}
                disabled={savingUpdater}
              >
                取消
              </button>
              <div class="upd-footer-right">
                <button
                  class="upd-btn-secondary"
                  onClick={() => handleSaveUpdaterSettings(true)}
                  disabled={savingUpdater}
                >
                  保存并检查
                </button>
                <button
                  class="btn-small"
                  onClick={() => handleSaveUpdaterSettings(false)}
                  disabled={savingUpdater}
                >
                  {savingUpdater ? '保存中…' : '保存'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* ── 快捷实时日志弹窗 ── */}
      {showLogModal && (
        <div
          class="quit-overlay"
          onClick={e => {
            if (e.target === e.currentTarget) setShowLogModal(false);
          }}
        >
          <div class="quit-dialog desk-log-dialog">
            <div class="upd-header desk-log-header">
              <div>
                <span class="upd-title">实时日志</span>
                <span class="upd-desc" style="margin-left:8px;">最近 {logLines.length} 行</span>
              </div>
              <div class="desk-log-actions">
                <button
                  class={`icon-btn upd-eye-btn${logPaused ? ' is-paused' : ''}`}
                  onClick={() => {
                    const next = !logPaused;
                    setLogPaused(next);
                    logPausedRef.current = next;
                    if (!next) fetchLogsOnce(logAutoFollowRef.current);
                  }}
                  title={logPaused ? '恢复自动刷新' : '暂停自动刷新'}
                >
                  {logPaused ? '继续' : '暂停'}
                </button>
                <button class="icon-btn upd-eye-btn" onClick={() => fetchLogsOnce(true)} title="立即刷新">
                  刷新
                </button>
                <button
                  class="icon-btn upd-eye-btn"
                  onClick={async () => {
                    await GuiApp.APIProxy('POST', '/api/logs/clear', '');
                    pendingLogsRef.current = null;
                    setHasNewLogs(false);
                    setLogLines([]);
                    toast('日志已清空');
                  }}
                  title="清空日志"
                >
                  清空
                </button>
                <button
                  class="icon-btn upd-eye-btn"
                  onClick={async () => {
                    try {
                      await navigator.clipboard.writeText(logLines.join('\n'));
                      toast('已复制日志');
                    } catch {
                      toast('复制失败');
                    }
                  }}
                >
                  复制
                </button>
                <button class="icon-btn upd-eye-btn" onClick={() => setShowLogModal(false)}>关闭</button>
              </div>
            </div>

            <div class="desk-log-wrap">
              <div class="desk-log-body" ref={logBodyRef} onScroll={handleLogScroll}>
                {logLines.length === 0 ? (
                  <div class="desk-log-empty">暂无日志记录</div>
                ) : (
                  logLines.map((line, idx) => {
                    const item = parseLogLine(line);
                    return (
                      <div key={idx} class="desk-log-line">
                        {item.time && <span class="desk-log-time">{item.time}</span>}
                        {item.level && (
                          <span class={`desk-log-lvl is-${item.levelKind}`}>{item.level}</span>
                        )}
                        <span class="desk-log-msg">
                          {item.tokens.map((tok, ti) =>
                            tok.type === 'url' ? (
                              <span key={ti} class="desk-log-url">{tok.value}</span>
                            ) : tok.type === 'key' ? (
                              <span key={ti} class="desk-log-key">{tok.value}</span>
                            ) : (
                              tok.value
                            )
                          )}
                        </span>
                      </div>
                    );
                  })
                )}
              </div>

              {!logAutoFollow && (
                <button
                  type="button"
                  class={`desk-log-jump-btn${hasNewLogs ? ' has-new' : ''}`}
                  onClick={scrollToLogBottom}
                  title="跳到最新日志并恢复自动跟随"
                >
                  {hasNewLogs && <span class="desk-log-new-dot" />}
                  <span>{hasNewLogs ? '有新日志 ↓' : '回到底部 ↓'}</span>
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </>
  );
}