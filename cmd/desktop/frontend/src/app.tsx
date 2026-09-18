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
async function openLink(url: string, windowSize: 'extraLarge' | 'large' | 'medium' | 'small' | 'tiny' | 'wide' = 'medium') {
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
  const [autostartEnabled, setAutostart] = useState(false);
  // 当 Wails updater 检测到新版本时为 true（按钮高亮提示）
  const [updateAvailable, setUpdateAvailable] = useState(false);
  const [updateVersion, setUpdateVersion] = useState('');

  // ── Wails 就绪后立即拉取应用信息 ──────────────────────────────
  useEffect(() => {
    if (!ready) return;
    loadAppInfo();
    // 实时查询开机自启状态（不依赖 GetAppInfo 缓存值，确保与托盘菜单一致）
    GuiApp.GetAutoStartEnabled()
      .then(enabled => setAutostart(enabled))
      .catch(() => { /* 平台不支持时静默忽略 */ });
  }, [ready]);


  // ── 监听"窗口关闭"事件 ────────────────────────────────────────
  useEffect(() => {
    if (!ready) return;
    const unsub = Events.On('window:close-requested', () => {
      setShowQuit(true);
    });
    return () => { unsub && unsub(); };
  }, [ready]);

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

    // 新增：监听 Go 端发来的检查结果 toast（已最新 / 出错 / 检查不可用）
    const unsubToast = Events.On('gui:update:toast', (event) => {
      const text = (event?.data as string) ?? '';
      if (text) toast(text);
    });

    // 新增：监听后台静默检查（启动时 / 托盘每 4 小时一次）完成后推送的状态。
    // 之前只在组件挂载时 GetUpdateStatus() 读一次缓存值，若此时后台检查
    // （经代理访问 GitHub，耗时不定）还没跑完，就会读到"无更新"的初始值，
    // 且之后再也不会刷新，导致徽标一直不出现。订阅这个事件后，无论检查
    // 何时完成，前端都能收到最新结果。
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

  // ── 监听托盘「开机自启」切换事件，回查系统真实状态后同步按钮 ──────────
  // 注意：不直接使用事件 payload（Go bool 经 Wails v3 序列化后在 JS 侧
  // 可能被包裹为 [false]，Boolean([false]) === true，导致「关闭」失效）。
  // 改为事件仅作触发信号，收到后立即向 Go 回查真实状态，彻底规避此问题。
  useEffect(() => {
    if (!ready) return;
    const unsub = Events.On('autostart:changed', async () => {
      try {
        const actual = await GuiApp.GetAutoStartEnabled();
        setAutostart(actual);
      } catch { /* 平台不支持时静默忽略 */ }
    });
    return () => { unsub && unsub(); };
  }, [ready]);

  async function loadAppInfo() {
    setView('loading');
    try {
      const data = await GuiApp.GetAppInfo();

      // 尽早设置，让 useTheme 能拿到正确端口
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
    // // 端口已变更，更新全局 base URL，避免 useTheme 等钩子继续使用旧端口
    (window as any).__CORE_BASE_URL = `http://127.0.0.1:${newInfo.listenPort}`;
    syncFromServer();
    setInfo(newInfo);
    setView('main');
  }

  function handleSelectConfig(path: string) { setCfgPath(path); setView('password'); }
  function handlePasswordDone(newInfo: AppInfo | null) {
    if (newInfo) {
      // 新配置可能使用不同端口，必须更新 base URL，避免 useTheme 等钩子用旧端口访问
      (window as any).__CORE_BASE_URL = `http://127.0.0.1:${newInfo.listenPort}`;
      syncFromServer();
      setInfo(newInfo);
    }
    setView('main');
  }
  function handlePasswordBack() { setView('main'); }
  function handlePasswordReselect(path: string) { setCfgPath(path); /* 保持 password 视图，只更新路径 */ }
  const requestClose = () => setShowQuit(true);

  async function handleToggleAutostart() {
    const next = !autostartEnabled;
    try {
      await GuiApp.SetAutoStart(next);
      // 回查确保前端与系统状态一致（托盘菜单下次点击时也会读取系统状态）
      const actual = await GuiApp.GetAutoStartEnabled();
      setAutostart(actual);
      toast(actual ? '已设置开机自启' : '已取消开机自启');
      Notifier.SendOSNotification(actual ? '已设置开机自启' : '已取消开机自启', '');
    } catch (e: any) {
      toast('设置失败：' + (e?.message ?? '功能暂不可用'));
    }
  }

  async function handleCheckForUpdates() {
    try {
      await GuiApp.CheckForUpdates();
    } catch (e: any) {
      toast('检查更新失败：' + (e?.message ?? ''));
    }
  }

  // ── 左侧品牌面板（分栏视图专用）────────────────────────────────
  const BrandPanel = () => (
    <aside class="brand-panel">
      {/* 品牌主体：大 Logo */}
      <div class="brand-body">
        <img src="/logo.svg" alt="logo" class="brand-icon" />
      </div>

      {/* ── 底部工具行：开机自启 | 竖线 | github | tg | docker | 竖线 | 🔄 检查更新 */}
      <nav class="brand-links">
        {/* 开机自启图标按钮 */}
        <button
          class={`brand-autostart${autostartEnabled ? ' active' : ''}`}
          onClick={handleToggleAutostart}
          title={autostartEnabled ? '开机自启：已开启（点击关闭）' : '开机自启：已关闭（点击开启）'}
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
            stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <path d="M18.36 6.64a9 9 0 1 1-12.73 0" />
            <line x1="12" y1="2" x2="12" y2="12" />
          </svg>
        </button>

        {/* 竖线分割 */}
        <span class="brand-sep" />

        {/* 社交链接 */}
        <a class="brand-link" onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro')} title="GitHub 仓库，欢迎 Star 和提 Issue">
          <img src="/github.svg" alt="GitHub" class="brand-social-icon" />
        </a>
        <a class="brand-link" onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://t.me/subs_check_pro', 'tiny')} title="Telegram 群组，建议加入以获取最新动态和使用帮助">
          <img src="/telegram.svg" alt="Telegram" class="brand-social-icon" />
        </a>
        <a class="brand-link" onClick={() => openLink('https://hub.docker.com/r/sinspired/subs-check-pro')} title="Docker Hub 仓库，提供官方镜像">
          <img src="/docker.svg" alt="Docker" class="brand-social-icon" />
        </a>

        {/* 竖线分割 */}
        {/* <span class="brand-sep" /> */}

        {/* 🔄 检查更新按钮（替换原关于按钮位置） */}
        <button
          class={`brand-autostart${updateAvailable ? ' update-available' : ''}`}
          title={updateAvailable ? `有可用更新${updateVersion ? ` (${updateVersion})` : ''}，点击安装` : '检查更新'}
          onClick={handleCheckForUpdates}
        >
          <svg viewBox="0 0 1024 1024" xmlns="http://www.w3.org/2000/svg" width="15" height="15"><path d="M520.533 460.8l-179.2 170.667h358.4L520.533 460.8zm-52.906 170.65v204.817H290.133c-122.47 0-221.866-103.766-221.866-231.63 0-105.13 67.003-193.62 158.856-222.344C275.046 267.861 384.65 187.733 512 187.733s236.954 80.128 284.877 194.56C888.73 410.54 955.733 499.49 955.733 604.638c0 127.863-99.396 231.629-221.866 231.629H556.373V631.45" fill="currentColor" /></svg>
        </button>
      </nav>
    </aside>
  );

  // ── 右侧顶部工具栏：左侧端口状态 + 拖拽区 + 右侧主题切换 ──
  const PanelToolbar = ({ portInfo }: { portInfo?: AppInfo | null }) => (
    <div class="lp-toolbar">
      {/* 左侧：端口状态（有 info 时显示） */}
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

      {/* 拖拽区（弹性填充） */}
      <div class="lp-drag-area" />

      {/* 右侧：主题切换 */}
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

  // ── 右侧底栏：ⓘ 关于按钮 + 版本标签，水平居中，与左侧 brand-links 垂直对齐 ──
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

      {/* ── portConflict — 左右分栏布局（与 main/password 视图保持一致的窗口感）── */}
      {view === 'portConflict' && info && (
        <div class="page split-page">
          <BrandPanel />
          <section class="login-panel">
            <PanelToolbar portInfo={null} />
            <div class="login-content login-content--conflict">
              <PortConflict info={info} toast={toast} onFixed={handlePortsFixed} />
            </div>
            <LpFooter />
          </section>
        </div>
      )}

      {/* ── main — 左右分栏布局 ── */}
      {view === 'main' && info && (
        <div class="page split-page">
          <BrandPanel />
          <section class="login-panel">
            <PanelToolbar portInfo={info} />
            <div class="login-content">
              <KeySection info={info} toast={toast} onSelectConfig={handleSelectConfig} />
            </div>
            <LpFooter />
          </section>
        </div>
      )}

      {/* ── password — 左右分栏布局 ── */}
      {view === 'password' && (
        <div class="page split-page">
          <BrandPanel />
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
    </>
  );
}