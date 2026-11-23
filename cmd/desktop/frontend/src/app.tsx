/**
 * frontend/src/app.tsx
 * 登录窗口根组件 — Wails3 + Preact + TypeScript
 */
import { useEffect, useState } from 'preact/hooks';
import { Events } from '@wailsio/runtime';

import { useTheme } from './hooks/useTheme';
import { useToast } from './hooks/useToast';
import { useWailsReady } from './hooks/useWailsReady';

import { Header } from './components/Header';
import { KeySection } from './components/KeySection';
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

  // ── 监听托盘「开机自启 / 静默自启」切换事件，回查真实状态 ─────
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
      } catch { /* 平台不支持时静默忽略 */ }
    });
    return () => { unsub && unsub(); };
  }, [ready]);

  // ── 点击外部或按 Esc 自动关闭设置菜单 / 更新器弹窗 ─────────────
  useEffect(() => {
    if (!showSettings && !showUpdaterModal) return;
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
      }
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [showSettings, showUpdaterModal]);

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
      {/* 品牌主体：大 Logo */}
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
                <span class="bsm-title">静默启动</span>
                <span class="bsm-sub">自启时不显示窗口</span>
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

  // ── 右侧底栏：版本标签 ──
  const LpFooter = () => {
    if (!info) return <div class="lp-footer" />;
    return (
      <div class="lp-footer">
        <a
          class="ver-tag ver-gui"
          onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-free')}
          title={`GUI 版本：${info.guiVersion || 'dev'}  →  sinspired/subs-free`}
        >
          GUI&nbsp;{info.guiVersion || 'dev'}
        </a>
        <span class="ver-dot">·</span>
        <a
          class="ver-tag ver-core"
          onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro')}
          title={`内核 版本：${info.coreVersion || 'dev'}  →  sinspired/subs-check-pro`}
        >
          内核&nbsp;{info.originVersion || 'dev'}
        </a>
      </div>
    );
  };

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
            <LpFooter />
          </section>
        </div>
      )}

      {/* ── main ── */}
      {view === 'main' && info && (
        <div class="page split-page">
          {renderBrandPanel()}
          <section class="login-panel">
            <PanelToolbar portInfo={info} />
            <div class="login-content">
              <KeySection info={info} toast={toast} onSelectConfig={handleSelectConfig} />
            </div>
            <LpFooter />
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
            <LpFooter />
          </section>
        </div>
      )}

      <Toast msg={msg} visible={visible} />

      {showQuit && (
        <QuitDialog onClose={() => setShowQuit(false)} />
      )}

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
    </>
  );
}