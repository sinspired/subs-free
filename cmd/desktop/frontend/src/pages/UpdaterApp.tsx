/**
 * frontend/src/pages/UpdaterApp.tsx
 *
 * 参考旧版 updater.html 重构，保留其经过验证的布局策略：
 *
 *  ┌─ titlebar ─────────────────────────────────────────────┐
 *  │  hero（图标 · 标题 · ver-pill · 副标题）                │
 *  │  progress-container ← 始终占位，opacity 淡入/出          │
 *  │  notes ← flex:1，内容在 available→ready 阶段持续保留     │
 *  │  actions                                               │
 *  └────────────────────────────────────────────────────────┘
 *
 * 与旧版 TSX 的主要差异：
 *  1. progress-container 始终在 DOM 中，通过 `.visible` 切换 opacity，
 *     不再条件渲染，布局不发生跳变。
 *  2. checking 阶段也显示不定态进度条（与 HTML 参考版一致）。
 *  3. notes 内容在 available → downloading → verifying → installing → ready
 *     全流程中持续保留，用户下载时仍可阅读变更说明。
 *  4. notesHtml 独立为 state，仅在 check-started / up-to-date / error 时清空。
 *  5. handleRetry 调用 GuiApp.CheckForUpdates() 发起真实重检，同时
 *     立即清空 notesHtml / release，避免旧内容残留。
 *  6. ver-pill 在 available → ready 全流程中持续可见（release 未被清空时）。
 *  7. 按钮可见性完全对齐 HTML 参考版：
 *     checking    → [later] [close]
 *     available   → [install] / [skip] [later] [close]
 *     downloading → [later] [close]
 *     verifying   → (全隐藏，不可中断)
 *     installing  → (全隐藏，不可中断)
 *     ready       → [restart] / [close]
 *     up-to-date  → [close]
 *     error       → [retry] / [close]
 */
import { useEffect, useState } from 'preact/hooks';
import { Events } from '@wailsio/runtime';
import { GuiApp } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';
import { useWailsReady } from '../hooks/useWailsReady';
import { useTheme } from '../hooks/useTheme';
import { md2html } from '../utils/markdown';

// ── 状态机 ─────────────────────────────────────────────────────────
type Stage =
  | 'checking'
  | 'available'
  | 'up-to-date'
  | 'downloading'
  | 'verifying'
  | 'installing'
  | 'ready'
  | 'error';

interface ReleaseInfo {
  version?: string;
  currentVersion?: string;
  size?: number;
}

interface DownloadProgress {
  written: number;
  total: number;
  rate: number;
}

// ── 工具函数 ────────────────────────────────────────────────────────

function extractData(event: any): any {
  let d = event?.data ?? event ?? {};
  if (Array.isArray(d) && d.length > 0) d = d[0];
  if (typeof d === 'string') {
    try { d = JSON.parse(d); } catch { /* keep raw */ }
  }
  return d || {};
}

function pick<T = any>(obj: any, ...keys: string[]): T | undefined {
  for (const k of keys) {
    if (obj != null && obj[k] !== undefined && obj[k] !== null) return obj[k] as T;
  }
  return undefined;
}

function fmtBytes(n: number): string {
  if (!n || n <= 0) return '';
  if (n < 1024) return `${n} B`;
  if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1048576).toFixed(1)} MB`;
}

function emitEv(name: string) {
  Events.Emit(name);
}

// ── 图标 ───────────────────────────────────────────────────────────

/** checking / downloading / verifying / installing — 旋转 */
const IconSpin = () => (
  <svg class="upd-spin" width="26" height="26" viewBox="0 0 24 24"
    fill="none" stroke="currentColor" stroke-width="2"
    stroke-linecap="round" stroke-linejoin="round">
    <path d="M21 12a9 9 0 1 1-6.219-8.56" />
  </svg>
);

/** available — 向上箭头提示有新版 */
const IconArrowUp = () => (
  <svg width="26" height="26" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2"
    stroke-linecap="round" stroke-linejoin="round">
    <circle cx="12" cy="12" r="10" />
    <polyline points="16 12 12 8 8 12" />
    <line x1="12" y1="16" x2="12" y2="8" />
  </svg>
);

/** up-to-date / ready — 勾 */
const IconCheck = () => (
  <svg width="26" height="26" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2.5"
    stroke-linecap="round" stroke-linejoin="round">
    <polyline points="20 6 9 17 4 12" />
  </svg>
);

/** error */
const IconAlert = () => (
  <svg width="26" height="26" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2"
    stroke-linecap="round" stroke-linejoin="round">
    <circle cx="12" cy="12" r="10" />
    <line x1="12" y1="8" x2="12" y2="12" />
    <line x1="12" y1="16" x2="12.01" y2="16" />
  </svg>
);

/** 主按钮内嵌 spinner */
const BtnSpinner = () => <span class="upd-btn-spinner" />;

const IcoDownload = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2.2"
    stroke-linecap="round" stroke-linejoin="round">
    <polyline points="8 17 12 21 16 17" />
    <line x1="12" y1="12" x2="12" y2="21" />
    <path d="M20.88 18.09A5 5 0 0 0 18 9h-1.26A8 8 0 1 0 3 16.29" />
  </svg>
);

const IcoRestart = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2.2"
    stroke-linecap="round" stroke-linejoin="round">
    <polyline points="23 4 23 10 17 10" />
    <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
  </svg>
);

// ── 主组件 ─────────────────────────────────────────────────────────
export function UpdaterApp() {
  const ready = useWailsReady();
  useTheme();

  const [stage, setStage] = useState<Stage>('checking');
  const [release, setRelease] = useState<ReleaseInfo>({});
  /**
   * notesHtml 独立于 release，确保 available → ready 阶段内容持续展示：
   *  - 置空：check-started / up-to-date / error
   *  - 赋值：update-available
   *  - 保持：downloading / verifying / installing / ready
   */
  const [notesHtml, setNotesHtml] = useState('');
  const [progress, setProgress] = useState<DownloadProgress>({ written: 0, total: 0, rate: 0 });
  const [errMsg, setErrMsg] = useState('');
  const [busy, setBusy] = useState(false);

  // ── 事件订阅 ──────────────────────────────────────────────────────
  useEffect(() => {
    if (!ready) return;
    const unsubs: Array<() => void> = [];
    const on = (name: string, fn: (e: any) => void) => {
      const off = Events.On(name, fn);
      if (off) unsubs.push(off);
    };

    on('wails:updater:check-started', () => {
      setRelease({});
      setNotesHtml('');
      setBusy(false);
      setStage('checking');
    });

    on('wails:updater:update-available', (e) => {
      const d = extractData(e);
      const rawNotes: string =
        pick(d, 'notes', 'Notes', 'body', 'Body', 'releaseNotes', 'ReleaseNotes') ?? '';
      setRelease({
        version: pick(d, 'version', 'Version'),
        currentVersion: pick(d, 'currentVersion', 'CurrentVersion'),
        size: pick(d?.artifact ?? d?.Artifact, 'size', 'Size'),
      });
      setNotesHtml(md2html(rawNotes));
      setBusy(false);
      setStage('available');
    });

    const onNoUpdate = (e: any) => {
      const d = extractData(e);
      const cur = pick<string>(d, 'currentVersion', 'CurrentVersion');
      if (cur) setRelease(r => ({ ...r, currentVersion: cur }));
      setNotesHtml('');
      setStage('up-to-date');
    };
    on('wails:updater:no-update', onNoUpdate);
    on('wails:updater:no-update-available', onNoUpdate);

    // EventMeta：只补充 currentVersion，不切换 stage
    on('wails:updater:meta', (e) => {
      const d = extractData(e);
      const cur = pick<string>(d, 'currentVersion', 'CurrentVersion');
      if (cur) setRelease(r => ({ ...r, currentVersion: cur }));
    });

    on('wails:updater:download-started', () => {
      setProgress({ written: 0, total: 0, rate: 0 });
      setStage('downloading');
    });

    on('wails:updater:download-progress', (e) => {
      const d = extractData(e);
      setProgress({
        written: pick<number>(d, 'written', 'Written') ?? 0,
        total: pick<number>(d, 'total', 'Total') ?? 0,
        rate: pick<number>(d, 'rate', 'Rate') ?? 0,
      });
      setStage('downloading');
    });

    on('wails:updater:verifying', () => setStage('verifying'));
    on('wails:updater:installing', () => setStage('installing'));

    const onReady = () => { setBusy(false); setStage('ready'); };
    on('wails:updater:update-ready', onReady);
    on('wails:updater:ready-to-install', onReady);

    on('wails:updater:error', (e) => {
      const d = extractData(e);
      let msg = typeof d === 'string' ? d : (d.message ?? d.Message ?? '') as string;
      const st = d.stage ?? d.Stage;
      if (st && msg) msg = `[${st}] ${msg}`;
      setErrMsg(msg || '未知错误');
      setNotesHtml('');
      setBusy(false);
      setStage('error');
    });

    // 握手：通知宿主页面已就绪，触发一次状态回放
    emitEv('wails:updater:window:ready');
    return () => unsubs.forEach(off => off());
  }, [ready]);

  // ── 用户操作 ──────────────────────────────────────────────────────

  function handleInstall() {
    setBusy(true);
    emitEv('wails:updater:user:install');
  }
  function handleRestart() {
    setBusy(true);
    emitEv('wails:updater:user:restart');
  }
  /** 重试：立即清空旧数据并发起真实的新一轮检查。 */
  function handleRetry() {
    setRelease({});
    setNotesHtml('');
    setBusy(false);
    setStage('checking');
    GuiApp.CheckForUpdates();
  }
  function handleSkip() { emitEv('wails:updater:user:skip'); }
  function handleBackground() { emitEv('wails:updater:user:background'); }
  function handleRemind() { emitEv('wails:updater:user:remind'); }
  function handleClose() { emitEv('wails:updater:user:cancel'); }

  /** 拦截 notes 内链接，用内置无地址栏窗口打开外链。 */
  function handleNotesClick(e: MouseEvent) {
    const a = (e.target as HTMLElement).closest('a[href]') as HTMLAnchorElement | null;
    if (!a) return;
    const url = a.href;
    if (!/^https?:\/\//.test(url)) return;
    e.preventDefault();
    GuiApp.OpenBrandURL(url, 'medium').catch(() => window.open(url, '_blank'));
  }

  // ── 派生展示数据 ──────────────────────────────────────────────────
  const ver = release.version ? `v${release.version.replace(/^v/, '')}` : '';
  const curVer = release.currentVersion ? `v${release.currentVersion.replace(/^v/, '')}` : '';

  // 图标变体
  const iconKind: 'spin' | 'update' | 'check' | 'error' =
    stage === 'up-to-date' || stage === 'ready' ? 'check' :
      stage === 'error' ? 'error' :
        stage === 'available' ? 'update' :
          'spin'; // checking / downloading / verifying / installing

  // Hero 文案
  const [heroTitle, heroSub] = ((): [string, string] => {
    switch (stage) {
      case 'checking': return ['正在检查更新…', '请稍候'];
      case 'available': {
        const parts = [ver, release.size ? fmtBytes(release.size) : ''].filter(Boolean);
        return ['发现新版本', parts.join('  ·  ')];
      }
      case 'up-to-date': return ['已是最新版本', curVer ? `当前版本  ${curVer}` : '无需更新'];
      case 'downloading': {
        const { written, total, rate } = progress;
        const rateStr = rate > 0 ? `${fmtBytes(rate)}/s` : '';
        const sizeStr = total > 0
          ? `${fmtBytes(written)} / ${fmtBytes(total)}`
          : fmtBytes(written);
        return ['正在下载更新…', [sizeStr, rateStr].filter(Boolean).join('  ·  ')];
      }
      case 'verifying': return ['正在验证文件…', '校验完整性，请稍候'];
      case 'installing': return ['正在安装更新…', '准备替换旧版本'];
      case 'ready': return ['更新已就绪', '重启即可应用新版本'];
      case 'error': return ['更新失败', errMsg];
    }
  })();

  // ver-pill：available 起出现，downloading/ready 持续显示（release 未清空）
  const showVerPill = !!ver && !!curVer;

  // progress-container：checking / downloading / verifying / installing 可见
  const progressVisible =
    stage === 'checking' ||
    stage === 'downloading' ||
    stage === 'verifying' ||
    stage === 'installing';

  // 进度百分比（null = 不定态）
  const progressPct =
    stage === 'downloading' && progress.total > 0
      ? Math.min(100, (progress.written / progress.total) * 100)
      : null;

  // 按钮可见性（完全对齐 HTML 参考版）
  const show = {
    install: stage === 'available',
    restart: stage === 'ready',
    retry: stage === 'error',
    skip: stage === 'available',
    later: stage === 'checking' || stage === 'available' || stage === 'downloading',
    close: stage !== 'verifying' && stage !== 'installing',
  } as const;

  return (
    <div class="upd-root">
      {/* 拖拽标题栏 */}
      <div class="upd-titlebar" />

      {/* ── Hero ── */}
      <div class="upd-hero">
        <div class={`upd-icon-wrap${iconKind === 'check' ? ' is-success' :
          iconKind === 'error' ? ' is-error' : ''
          }`}>
          {iconKind === 'spin' && <IconSpin />}
          {iconKind === 'update' && <IconArrowUp />}
          {iconKind === 'check' && <IconCheck />}
          {iconKind === 'error' && <IconAlert />}
        </div>

        <p class="upd-hero-title">{heroTitle}</p>

        {showVerPill && (
          <div class="upd-ver-pill">
            <span class="cur">{curVer}</span>
            <span class="arr">→</span>
            <span class="nxt">{ver}</span>
          </div>
        )}

        <p class="upd-hero-sub">{heroSub}</p>
      </div>

      {/* ── 进度条（始终占位，opacity 淡入/出，不影响 notes 高度） ── */}
      <div class={`upd-progress-container${progressVisible ? ' visible' : ''}`}>
        <div class="upd-progress-track">
          <div
            class={`upd-progress-fill${progressPct === null ? ' indeterminate' : ''}`}
            style={progressPct !== null ? { width: `${progressPct.toFixed(1)}%` } : undefined}
          />
        </div>
        <span class="upd-progress-label">
          {progressPct !== null ? `${Math.round(progressPct)}%` : ''}
        </span>
      </div>

      {/* ── Release Notes（始终在 DOM，flex:1，内容在下载流程中持续展示） ── */}
      <div class="upd-notes">
        {notesHtml
          ? (
            <div
              class="upd-notes-body"
              dangerouslySetInnerHTML={{ __html: notesHtml }}
              onClick={handleNotesClick}
            />
          )
          : <p class="upd-notes-placeholder">
            {stage === 'up-to-date' ? '当前已是最新版本，无需更新。' : '暂无发布说明'}
          </p>
        }
      </div>

      {/* ── Actions ── */}
      <div class="upd-actions">

        {/* 主操作按钮 */}
        {show.install && (
          <button class="upd-btn-primary" onClick={handleInstall} disabled={busy}>
            {busy
              ? <><BtnSpinner />&nbsp;下载中…</>
              : <><IcoDownload />下载并安装</>
            }
          </button>
        )}

        {show.restart && (
          <button class="upd-btn-primary" onClick={handleRestart} disabled={busy}>
            {busy
              ? <><BtnSpinner />&nbsp;重启中…</>
              : <><IcoRestart />重启并应用更新</>
            }
          </button>
        )}

        {show.retry && (
          <button class="upd-btn-primary" onClick={handleRetry}>
            重试
          </button>
        )}

        {/* 次级操作行 */}
        {(show.skip || show.later || show.close) && (
          <div class="upd-btn-row">
            {show.skip && <button class="upd-btn-ghost" onClick={handleSkip}>跳过此版本</button>}
            {show.later && (
              <button class="upd-btn-ghost" onClick={
                stage === 'downloading' ? handleBackground : handleRemind
              }>
                {stage === 'downloading' ? '后台下载' : '稍后提醒'}
              </button>
            )}
            {show.close && <button class="upd-btn-ghost" onClick={handleClose}>关闭</button>}
          </div>
        )}
      </div>
    </div>
  );
}