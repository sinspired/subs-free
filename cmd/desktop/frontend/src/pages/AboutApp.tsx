/**
 * frontend/src/AboutApp.tsx
 * 「关于」独立窗口 — 侘寂风分栏设计（800×600 优化版）
 *
 * 布局：左侧边栏 200px（Logo + 版本 + 垂直导航）
 *       右侧内容 600px（工具栏 + 面板 + 底栏）
 */
import { useEffect, useState } from 'preact/hooks';
import { useTheme } from '../hooks/useTheme';
import { useWailsReady } from '../hooks/useWailsReady';
import { GuiApp, AppInfo, UpdateInfo } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';
import { useToast } from '../hooks/useToast';
import { Toast } from '../components/Toast';
import { md2html } from '../utils/markdown';

async function openLink(url: string, windowSize: 'extraLarge' | 'large' | 'medium' | 'small' | 'tiny' | 'wide' = 'medium') {
  try {
    await GuiApp.OpenBrandURL(url, windowSize);
  } catch {
    window.open(url, '_blank');
  }
}

// ── 类型 ──────────────────────────────────────────────────────────
type Tab = 'intro' | 'features' | 'resources' | 'update';

// ── 静态数据 ──────────────────────────────────────────────────────
const NAV_ITEMS: { id: Tab; label: string; hint: string }[] = [
  { id: 'intro', label: '系统概览', hint: 'Overview' },
  { id: 'features', label: '核心特性', hint: 'Features' },
  { id: 'resources', label: '生态资源', hint: 'Resources' },
  { id: 'update', label: '检查更新', hint: 'Update' },

];

const FEATURES = [
  { emoji: '📱', label: '现代 Web UI 和 跨平台桌面客户端' },
  { emoji: '⚡', label: '自适应流水线高并发测试模式' },
  { emoji: '🔋', label: '极致内存调度，千万节点低内存占用' },
  { emoji: '🗺️', label: 'GeoDB 增强地理位置标签' },
  { emoji: '📡', label: 'ISP / 原生 IP 类型检测' },
  { emoji: '📦', label: '自动生成开箱即用 sing-box 订阅' },
  { emoji: '📦', label: '自动生成开箱即用 mihomo 订阅' },
  { emoji: '🎲', label: '智能乱序重排' },
  { emoji: '🕒', label: '历史可用节点缓存复用' },
  { emoji: '📊', label: '检测结果分析报告 & 位置与协议分布可视化' },
  { emoji: '🚦', label: '自动检测系统代理环境' },
  { emoji: '🧩', label: '深度集成 Sub-Store 前后端' },
  { emoji: '🔒', label: '内置文件分发，支持独立防盗码' },
  { emoji: '📣', label: '多渠道消息通知推送' },
  { emoji: '🚦', label: '自动检测系统代理环境' },
  { emoji: '🎁', label: '自动无缝版本更新' },
  { emoji: '✏️', label: '配置编辑器 & 自动补全' },
  { emoji: '🔗', label: '多种非标订阅格式超级解码' },
  { emoji: '6️⃣', label: '支持 IPv6 代理节点' },
  { emoji: '🛠️', label: '开源免费，社区驱动持续迭代' },
];

// 资源链接：主推（Telegram）+ 次要三项
const TG_CHANNEL = {
  title: 'Telegram 频道',
  desc: '版本更新通知',
  url: 'https://proxy.linkpc.dpdns.org/https://t.me/sinspired_ai',
};
const TG_GROUP = {
  title: 'Telegram 群组',
  desc: '技术交流 · Issue 反馈',
  url: 'https://proxy.linkpc.dpdns.org/https://t.me/subs_check_pro',
};

const SEC_LINKS = [
  {
    svgSrc: '/github.svg' as string | null,
    title: 'GUI 客户端仓库',
    desc: '获取源码、构建版本及主题资源',
    url: 'https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-free',
  },
  {
    svgSrc: '/github.svg' as string | null,
    title: '内核引擎仓库',
    desc: '查阅内核引擎源码，版本发布及官方 Docker 镜像',
    url: 'https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro',
  },
  {
    svgSrc: null,
    title: '官方知识库 Wiki',
    desc: '部署教程、订阅规则及高阶配置',
    url: 'https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro/wiki',
  },
];

// ── 图标 ──────────────────────────────────────────────────────────
const SunIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <circle cx="12" cy="12" r="5" />
    <line x1="12" y1="1" x2="12" y2="3" /><line x1="12" y1="21" x2="12" y2="23" />
    <line x1="4.22" y1="4.22" x2="5.64" y2="5.64" />
    <line x1="18.36" y1="18.36" x2="19.78" y2="19.78" />
    <line x1="1" y1="12" x2="3" y2="12" /><line x1="21" y1="12" x2="23" y2="12" />
    <line x1="4.22" y1="19.78" x2="5.64" y2="18.36" />
    <line x1="18.36" y1="5.64" x2="19.78" y2="4.22" />
  </svg>
);

const MoonIcon = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
  </svg>
);

// Wiki 书本图标（内联，无需外部文件）
const WikiIcon = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
    <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
  </svg>
);

// 右上角跳转箭头
const ArrowIcon = () => (
  <svg class="aw-link-arrow" width="13" height="13" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <line x1="7" y1="17" x2="17" y2="7" />
    <polyline points="7 7 17 7 17 17" />
  </svg>
);

// 刷新图标（检查更新 / 重新检查共用）
const RefreshIcon = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <polyline points="23 4 23 10 17 10" />
    <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
  </svg>
);

// ── 组件 ──────────────────────────────────────────────────────────
export function AboutApp() {
  const ready = useWailsReady();
  const { theme, toggleTheme } = useTheme();
  const isDark = theme === 'dark';
  const [info, setInfo] = useState<AppInfo | null>(null);
  const [activeTab, setActiveTab] = useState<Tab>('intro');

  // ── 更新检查状态 ────────────────────────────────────────────────
  // idle: 初始状态  checking: 查询中  done: 查询完成
  const [updateStatus, setUpdateStatus] = useState<'idle' | 'checking' | 'done'>('idle');
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);

  const { msg, visible, toast } = useToast();

  useEffect(() => {
    if (!ready) return;
    GuiApp.GetAppInfo().then(setInfo).catch(() => { });
  }, [ready]);

  // 离开 update tab 时清空旧结果，避免下次进来看到过期数据
  useEffect(() => {
    if (activeTab !== 'update') {
      setUpdateStatus('idle');
      setUpdateInfo(null);
    }
  }, [activeTab]);

  const guiVer = info?.guiVersion || 'dev';
  const coreVer = info?.coreVersion || 'dev';

  function switchTab(tab: Tab) {
    if (tab !== activeTab) setActiveTab(tab);
  }

  /** 调用 Go binding 检查更新，结果内联显示在「检查更新」面板内 */
  async function checkUpdate() {
    setUpdateStatus('checking');
    setUpdateInfo(null);
    try {
      const result = await GuiApp.GetUpdateInfo();
      setUpdateInfo(result);
    } catch (e) {
      setUpdateInfo({
        hasUpdate: false,
        latestVersion: '',
        currentVersion: guiVer,
        releaseNotes: '',
        downloadURL: '',
        error: String(e),
        publishDate: '',
        platform: '',
        arch: '',
        filetype: '',
        assetSize: ''
      });
    } finally {
      setUpdateStatus('done');
    }
  }

  /**
   * 拦截 Release Notes 区域内的链接点击。
   * marked 渲染后的 Markdown 中可能包含 <a href="..."> 外链，
   * 统一通过 openLink() 在内置浏览器窗口中打开，
   * 避免在「关于」窗口内发生页面跳转。
   */
  function handleNotesClick(e: MouseEvent) {
    const target = e.target as HTMLElement;
    const anchor = target.closest('a[href]') as HTMLAnchorElement | null;
    if (!anchor) return;
    const url = anchor.href;
    if (!/^https?:\/\//.test(url)) return;
    e.preventDefault();
    openLink(url);
  }

  return (
    <div class="aw-root">

      {/* ══════════════════════════════════════════
          左侧边栏
          ══════════════════════════════════════════ */}
      <aside class="aw-sidebar">

        {/* Mac 标题栏高度留白（拖拽区） */}
        <div class="aw-sidebar-titlebar" />

        {/* Logo + 应用名 */}
        <div class="aw-brand">
          <img src="/logo.svg" alt="Logo" class="aw-logo" />
          <div class="aw-app-name">
            Subs Check <span class="pro">PRO⁺</span>
          </div>
          <div class="aw-app-sub">高并发代理质量检测终端</div>
        </div>

        {/* 版本块 */}
        <div class="aw-ver-block">
          <div
            class="aw-ver-row"
            onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-free')}
            title="打开 GUI 仓库"
          >
            <span class="aw-ver-dot gui-dot" />
            <span class="aw-ver-label">GUI</span>
            <span class="aw-ver-val accent">{guiVer}</span>
          </div>
          <div class="aw-ver-divider" />
          <div
            class="aw-ver-row"
            onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro')}
            title="打开 内核 仓库"
          >
            <span class="aw-ver-dot core-dot" />
            <span class="aw-ver-label">Core</span>
            <span class="aw-ver-val">{coreVer}</span>
          </div>
        </div>

        {/* 垂直导航 */}
        <nav class="aw-nav">
          {NAV_ITEMS.map(({ id, label, hint }) => (
            <button
              key={id}
              class={`aw-nav-item ${activeTab === id ? 'active' : ''}`}
              onClick={() => switchTab(id)}
            >
              <span class="aw-nav-label">{label}</span>
              <span class="aw-nav-hint">{hint}</span>
            </button>
          ))}
        </nav>

        {/* 底部版权 */}
        <div class="aw-sidebar-footer">
          © 2026{' '}
          <span
            style="cursor:pointer; text-decoration:underline; text-underline-offset:2px;"
            onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired')}
          >
            Sinspired
          </span>
          <span>&nbsp;·&nbsp;</span>
          <span
            style="cursor:pointer; text-decoration:underline; text-underline-offset:2px;"
            onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://www.gnu.org/licenses/gpl-3.0.html')}
          >
            GPL-3.0 License
          </span>
        </div>

      </aside>

      {/* ══════════════════════════════════════════
          右侧内容区
          ══════════════════════════════════════════ */}
      <div class="aw-content">

        {/* 工具栏：拖拽 + 主题切换 */}
        <div class="aw-content-toolbar">
          <div class="aw-drag-area" />
          <button class="icon-btn theme-btn" onClick={toggleTheme} title="切换主题">
            {isDark ? <SunIcon /> : <MoonIcon />}
          </button>
        </div>

        {/* 面板容器 */}
        <main class="aw-main">

          {/* ── 概览 ── */}
          {activeTab === 'intro' && (
            <div class="aw-panel" key="intro">

              {/* 引言：从 README 精炼 */}
              <p class="aw-intro-lead">
                基于 <strong>Wails v3</strong> 现代化框架构建，为底层引擎提供系统级原生适配。支持 <strong>定时检测</strong> 任务，自动生成 <strong>mihomo</strong> 与 <strong>sing-box</strong> 订阅，一键复制订阅链接。
              </p>

              {/* 双栏架构 */}
              <div class="aw-arch-grid">
                <div class="aw-arch-col">
                  <h3><span class="dot gui" />桌面客户端</h3>
                  <ul>
                    <li>Win / Mac / Linux 原生渲染</li>
                    <li>系统托盘与 OS 生命周期集成</li>
                    <li>沉浸式侘寂风与深色模式</li>
                    <li>零配置即刻启动与沙盒化驻留</li>
                    <li>Wails v3 跨平台框架</li>
                    <li>React + TypeScript 前端</li>
                    <li>系统级原生窗口适配</li>
                  </ul>
                </div>
                <div class="aw-arch-col">
                  <h3><span class="dot core" />高性能内核</h3>
                  <ul>
                    <li>自适应流水线高并发引擎</li>
                    <li>支持千万级节点池</li>
                    <li>低内存占用</li>
                    <li>现代 WebUI 管理界面</li>
                    <li>Docker 容器部署支持</li>
                    <li>自动无缝版本更新</li>
                    <li>支持 IPv6 代理节点</li>
                  </ul>
                </div>
              </div>

              {/* 本地服务快捷入口（复用 quickref 样式，动态端口） */}
              {(() => {
                // 动态端口，降级 8199
                const port = info?.listenPort || '8199';

                // 外链 SVG：右上角箭头（统一复用）
                const IconExternal = () => (
                  <svg class="aw-qr-action" width="11" height="11" viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
                    <polyline points="15 3 21 3 21 9" />
                    <line x1="10" y1="14" x2="21" y2="3" />
                  </svg>
                );
                return (
                  <div class="aw-quickref">
                    <div class="aw-qr-group">本地服务</div>

                    <div class="aw-qr-item" onClick={() => openLink(`http://localhost:${port}/admin`)}>
                      <svg class="aw-qr-icon" width="12" height="12" viewBox="0 0 24 24" fill="none"
                        stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <rect x="3" y="3" width="7" height="7" /><rect x="14" y="3" width="7" height="7" />
                        <rect x="14" y="14" width="7" height="7" /><rect x="3" y="14" width="7" height="7" />
                      </svg>
                      <span class="aw-qr-label">管理界面</span>
                      <span class="aw-qr-val">localhost:{port}/admin</span>
                      <IconExternal />
                    </div>

                    <div class="aw-qr-item" onClick={() => openLink(`http://localhost:${port}/analysis`)}>
                      <svg class="aw-qr-icon" width="12" height="12" viewBox="0 0 24 24" fill="none"
                        stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <line x1="18" y1="20" x2="18" y2="10" />
                        <line x1="12" y1="20" x2="12" y2="4" />
                        <line x1="6" y1="20" x2="6" y2="14" />
                      </svg>
                      <span class="aw-qr-label">分析报告</span>
                      <span class="aw-qr-val">localhost:{port}/analysis</span>
                      <IconExternal />
                    </div>

                    <div class="aw-qr-item" onClick={() => openLink(`http://localhost:${port}/files`, 'small')}>
                      <svg class="aw-qr-icon" width="12" height="12" viewBox="0 0 24 24" fill="none"
                        stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                      </svg>
                      <span class="aw-qr-label">文件服务</span>
                      <span class="aw-qr-val">localhost:{port}/files</span>
                      <IconExternal />
                    </div>
                  </div>
                );
              })()}

            </div>
          )}

          {/* ── 特性 ── */}
          {activeTab === 'features' && (
            <div class="aw-panel" key="features">
              <div class="aw-features-grid">
                {FEATURES.map(({ emoji, label }) => (
                  <div class="aw-feature-item" key={label}>
                    <span class="aw-feature-emoji">{emoji}</span>
                    <span class="aw-feature-label">{label}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* ── 资源 ── */}
          {activeTab === 'resources' && (
            <div class="aw-panel" key="resources">
              <div class="aw-res-layout">
                <div class="aw-res-primary">
                  {/* ── Telegram 频道 ── */}
                  < div class="aw-link-card aw-featured" onClick={() => openLink(TG_CHANNEL.url, 'tiny')}>
                    <div class="aw-link-icon-wrap aw-featured-icon">
                      <img src="/telegram.svg" class="aw-link-svg" alt="Telegram" />
                    </div>
                    <div class="aw-link-body">
                      <strong class="aw-link-title">{TG_CHANNEL.title}</strong>
                      <span class="aw-link-desc">{TG_CHANNEL.desc}</span>
                    </div>
                    <ArrowIcon />
                  </div>

                  {/* ── Telegram 群组 ── */}
                  < div class="aw-link-card aw-featured" onClick={() => openLink(TG_GROUP.url, 'tiny')}>
                    <div class="aw-link-icon-wrap aw-featured-icon">
                      <img src="/telegram.svg" class="aw-link-svg" alt="Telegram" />
                    </div>
                    <div class="aw-link-body">
                      <strong class="aw-link-title">{TG_GROUP.title}</strong>
                      <span class="aw-link-desc">{TG_GROUP.desc}</span>
                    </div>
                    <ArrowIcon />
                  </div>
                </div>

                {/* ── 次要三项（等宽并排） ── */}
                <div class="aw-res-secondary">
                  {SEC_LINKS.map(({ svgSrc, title, desc, url }) => (
                    <div class="aw-link-card aw-compact" key={url} onClick={() => openLink(url)}>
                      <div class="aw-link-icon-wrap">
                        {svgSrc
                          ? <img src={svgSrc} class="aw-link-svg" alt={title} />
                          : <WikiIcon />
                        }
                      </div>
                      <strong class="aw-link-title">{title}</strong>
                      <span class="aw-link-desc">{desc}</span>
                      <ArrowIcon />
                    </div>
                  ))}
                </div>

                {/* ── 快速参考区 ── */}
                {(() => {
                  // 外链 SVG：右上角箭头（统一复用）
                  const IconExternal = () => (
                    <svg class="aw-qr-action" width="11" height="11" viewBox="0 0 24 24" fill="none"
                      stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
                      <polyline points="15 3 21 3 21 9" />
                      <line x1="10" y1="14" x2="21" y2="3" />
                    </svg>
                  );

                  return (
                    <div class="aw-quickref">
                      {/* ── 部署 & 资源 ── */}
                      <div class="aw-qr-group">部署</div>

                      {/* Docker：点击复制命令 */}
                      <div class="aw-qr-item"
                        onClick={() =>
                          navigator.clipboard
                            .writeText('docker pull sinspired/subs-check-pro')
                            .then(() => toast('已复制 Docker 命令'))
                        }>

                        <svg class="aw-qr-icon"
                          width="800px"
                          height="800px"
                          viewBox="0 0 15 15"
                          fill="currentColor"
                          xmlns="http://www.w3.org/2000/svg"
                        >
                          <path
                            d="M0.5 5.5V5H0V5.5H0.5ZM2.5 3.5V3H2V3.5H2.5ZM6.5 1.5V1H6V1.5H6.5ZM8.5 1.5H9V1H8.5V1.5ZM12.5 7.5H12V8H12.5V7.5ZM1 7.5V5.5H0V7.5H1ZM3 7.5V3.5H2V7.5H3ZM2.5 4H8.5V3H2.5V4ZM8 3.5V7.5H9V3.5H8ZM5 7.5V3.5H4V7.5H5ZM7 7.5V1.5H6V7.5H7ZM6.5 2H8.5V1H6.5V2ZM8 1.5V3.5H9V1.5H8ZM13.7361 10H15V9H13.7361V10ZM10 5V5.5H11V5H10ZM12 6.5V7.5H13V6.5H12ZM12.5 8H13.5V7H12.5V8ZM14 8.5V9.5H15V8.5H14ZM13.5 8C13.7761 8 14 8.22386 14 8.5H15C15 7.67157 14.3284 7 13.5 7V8ZM11.5 6C11.7761 6 12 6.22386 12 6.5H13C13 5.67157 12.3284 5 11.5 5V6ZM3 10H4V9H3V10ZM8.5 7H0.5V8H8.5V7ZM0 7.5V8.5H1V7.5H0ZM5.5 14H6.02786V13H5.5V14ZM6.02786 14C8.51265 14 10.8164 12.8096 12.2585 10.8496L11.4531 10.257C10.1974 11.9636 8.19126 13 6.02786 13V14ZM0 8.5C0 11.5376 2.46243 14 5.5 14V13C3.01472 13 1 10.9853 1 8.5H0ZM0.5 6H11.5V5H0.5V6ZM10 5.5C10 6.32843 9.32843 7 8.5 7V8C9.88071 8 11 6.88071 11 5.5H10ZM13.7361 9C12.7762 9 11.9673 9.55817 11.4531 10.257L12.2585 10.8496C12.6423 10.3281 13.1808 10 13.7361 10V9Z"
                            fill="currentColor"
                          />
                        </svg>
                        <span class="aw-qr-label">Docker</span>
                        <span class="aw-qr-val aw-qr-cmd">docker pull sinspired/subs-check-pro</span>
                        {/* 复制图标 */}
                        <svg class="aw-qr-action" width="11" height="11" viewBox="0 0 24 24" fill="none"
                          stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                        </svg>
                      </div>
                      {/* ── 资源 ── */}
                      <div class="aw-qr-group">资源</div>
                      {/* Docker Hub */}
                      <div class="aw-qr-item"
                        onClick={() => openLink('https://hub.docker.com/r/sinspired/subs-check-pro')}>
                        <svg class="aw-qr-icon" width="12" height="12" viewBox="0 0 24 24" fill="none"
                          stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 2 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
                          <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
                          <line x1="12" y1="22.08" x2="12" y2="12" />
                        </svg>
                        <span class="aw-qr-label">DockerHub</span>
                        <span class="aw-qr-val">sinspired/subs-check-pro</span>
                        <IconExternal />
                      </div>

                      {/* 通知渠道配置 */}
                      <div class="aw-qr-item"
                        onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-check-pro/wiki/Notifications')}>
                        <svg class="aw-qr-icon" width="12" height="12" viewBox="0 0 24 24" fill="none"
                          stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9" />
                          <path d="M13.73 21a2 2 0 0 1-3.46 0" />
                        </svg>
                        <span class="aw-qr-label">通知配置</span>
                        <span class="aw-qr-val">…/wiki/Notifications</span>
                        <IconExternal />
                      </div>

                      {/* 自建 GitHub 加速代理 */}
                      <div class="aw-qr-item"
                        onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/CF-Proxy')}>
                        <svg class="aw-qr-icon" width="12" height="12" viewBox="0 0 24 24" fill="none"
                          stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <circle cx="12" cy="12" r="10" />
                          <line x1="2" y1="12" x2="22" y2="12" />
                          <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
                        </svg>
                        <span class="aw-qr-label">CF 加速</span>
                        <span class="aw-qr-val">sinspired/CF-Proxy</span>
                        <IconExternal />
                      </div>

                    </div>
                  );
                })()}
              </div>
            </div>
          )}

          {/* ── 检查更新 ── */}
          {activeTab === 'update' && (
            <div class="aw-panel aw-update-panel" key="update">

              {/* 当前版本信息卡 */}
              <div class="aw-update-ver-card">
                <div class="aw-update-ver-row">
                  <span class="aw-update-ver-dot gui-dot" />
                  <span class="aw-update-ver-label">桌面客户端</span>
                  <span class="aw-update-ver-val">{guiVer}</span>
                </div>
                <div class="aw-update-ver-divider" />
                <div class="aw-update-ver-row">
                  <span class="aw-update-ver-dot core-dot" />
                  <span class="aw-update-ver-label">内核引擎</span>
                  <span class="aw-update-ver-val">{coreVer}</span>
                </div>
              </div>

              {/* 检查按钮：三态文案 idle / checking / done */}
              <button
                class={`aw-update-check-btn ${updateStatus === 'checking' ? 'checking' : ''}`}
                onClick={checkUpdate}
                disabled={updateStatus === 'checking'}
              >
                {updateStatus === 'checking' ? (
                  <>
                    <span class="aw-update-spinner" />
                    检查中…
                  </>
                ) : updateStatus === 'done' ? (
                  <>
                    <RefreshIcon />
                    重新检查
                  </>
                ) : (
                  <>
                    <RefreshIcon />
                    检查更新
                  </>
                )}
              </button>

              {/* 结果区：仅在 done 状态时显示 */}
              {updateStatus === 'done' && updateInfo && (
                <div class={`aw-update-result ${updateInfo.error ? 'error' : updateInfo.hasUpdate ? 'has-update' : 'up-to-date'}`}>

                  {/* 错误状态 */}
                  {updateInfo.error ? (
                    <div class="aw-update-state-row">
                      <svg width="16" height="16" viewBox="0 0 24 24" fill="none"
                        stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <circle cx="12" cy="12" r="10" />
                        <line x1="12" y1="8" x2="12" y2="12" />
                        <line x1="12" y1="16" x2="12.01" y2="16" />
                      </svg>
                      <span>检查失败：{updateInfo.error}</span>
                    </div>

                  ) : updateInfo.hasUpdate ? (
                    /* 有新版本 */
                    <>
                      <div class="aw-update-state-row has-update">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none"
                          stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                          <polyline points="8 17 12 21 16 17" />
                          <line x1="12" y1="12" x2="12" y2="21" />
                          <path d="M20.88 18.09A5 5 0 0 0 18 9h-1.26A8 8 0 1 0 3 16.29" />
                        </svg>
                        <span>
                          发现新版本&nbsp;
                          <strong>{updateInfo.latestVersion}</strong>
                          &nbsp;（当前&nbsp;{updateInfo.currentVersion}）
                        </span>
                      </div>

                      {/* 详情卡片：头部铭牌 + 更新日志主体 */}
                      {(updateInfo.publishDate || updateInfo.releaseNotes) && (
                        <div class="aw-update-details-card">

                          {/* 头部：上方圆角，下方无圆角 */}
                          <div class="aw-update-meta-header">
                            <div class="aw-update-meta-item">
                              <span class="aw-update-meta-label">DATE</span>
                              <span class="aw-update-meta-val">{updateInfo.publishDate || 'Unknown'}</span>
                            </div>
                            <div class="aw-update-meta-divider" />
                            <div className="aw-update-meta-item">
                              <span className="aw-update-meta-label">PLATFORM</span>
                              <span className="aw-update-meta-val">{updateInfo.platform}</span>
                            </div>

                            <div className="aw-update-meta-item">
                              <span className="aw-update-meta-label">ARCH</span>
                              <span className="aw-update-meta-val">{updateInfo.arch}</span>
                            </div>

                            <div className="aw-update-meta-item">
                              <span className="aw-update-meta-label">TYPE</span>
                              <span className="aw-update-meta-val">
                                {updateInfo.filetype && (
                                  <span className="aw-update-meta-ext">
                                    {updateInfo.filetype.replace('.', '')}
                                  </span>
                                )}
                              </span>
                            </div>

                            <div class="aw-update-meta-divider" />
                            <div class="aw-update-meta-item">
                              <span class="aw-update-meta-label">SIZE</span>
                              <span class="aw-update-meta-val">{updateInfo.assetSize || 'N/A'}</span>
                            </div>
                          </div>

                          {/* 主体：上方无圆角，下方圆角 */}
                          {updateInfo.releaseNotes && (
                            <div
                              class="aw-update-notes"
                              dangerouslySetInnerHTML={{ __html: md2html(updateInfo.releaseNotes) }}
                              onClick={handleNotesClick}
                            />
                          )}
                        </div>
                      )}

                      {/* 按钮操作区：两端对齐 */}
                      <div class="aw-update-actions">
                        {/* 下载按钮：通过 ghproxy.net 加速 */}
                        <button
                          class="aw-update-dl-btn"
                          onClick={() => openLink(updateInfo.downloadURL, 'medium')}
                        >
                          <svg width="13" height="13" viewBox="0 0 24 24" fill="none"
                            stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                            <polyline points="7 10 12 15 17 10" />
                            <line x1="12" y1="15" x2="12" y2="3" />
                          </svg>
                          前往下载&nbsp;{updateInfo.latestVersion}
                        </button>

                        {/* 自动安装按钮（通过 Wails 内置 updater，下载已走 ghproxy） */}
                        <button
                          class="aw-update-auto-btn"
                          onClick={() => GuiApp.CheckForUpdates()}
                        >
                          <svg width="13" height="13" viewBox="0 0 24 24" fill="none"
                            stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                            <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
                          </svg>
                          自动下载并安装
                        </button>
                      </div>
                    </>

                  ) : (
                    /* 已是最新 */
                    <div class="aw-update-state-row up-to-date">
                      <svg width="16" height="16" viewBox="0 0 24 24" fill="none"
                        stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <polyline points="20 6 9 17 4 12" />
                      </svg>
                      <span>已是最新版本&nbsp;<strong>{updateInfo.currentVersion}</strong></span>
                    </div>
                  )}
                </div>
              )}

              {/* 底部说明 */}
              <p class="aw-update-note">
                · 「前往下载」将在应用内窗口打开 Release 页面，通过 ghproxy.net 加速访问。<br />
                · 「自动下载并安装」使用 Wails 内置更新器，下载同样经过代理加速，安装后需重启。
              </p>

            </div>
          )}

        </main>

        {/* 底栏 */}
        <footer class="aw-content-footer">
          <span
            style="cursor:pointer; text-decoration:underline; text-underline-offset:2px;"
            onClick={() => openLink('https://proxy.linkpc.dpdns.org/https://github.com/sinspired/subs-free')}
          >
            GitHub 仓库
          </span>
          &nbsp;·&nbsp;仅供学习与调试研究，请遵守相关法律法规
        </footer>
      </div >

      <Toast msg={msg} visible={visible} />
    </div >
  );
}