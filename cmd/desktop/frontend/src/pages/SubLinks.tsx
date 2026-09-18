/**
 * frontend/src/pages/SubLinks.tsx
 * 订阅链接独立窗口（500×420）
 *
 * 通过 Wails 资产代理（相对路径 /api/...）拉取订阅数据，
 * 展示可一键复制的订阅链接列表。
 * 与 KeySection 中的 fetchSubLinks 共享相同的请求逻辑。
 */
import { useEffect, useState } from 'preact/hooks';
import { useTheme } from '../hooks/useTheme';
import { useWailsReady } from '../hooks/useWailsReady';
import { GuiApp } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';

// 通过 userAgent 判断是否为 macOS
const isMac = /Macintosh|Mac OS X/i.test(navigator.userAgent);

// 根据 IP 段智能返回图标、标签和提示信息
function getIpMeta(ip: string) {
  if (ip === '127.0.0.1') {
    return {
      label: '本机',
      tooltip: '仅当前电脑自己可访问',
      icon: (
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="2" y="3" width="20" height="14" rx="2" />
          <line x1="8" y1="21" x2="16" y2="21" />
          <line x1="12" y1="17" x2="12" y2="21" />
        </svg>
      )
    };
  }
  if (ip.startsWith('192.168.')) {
    return {
      label: '局域网',
      tooltip: '同一路由器下的设备（如手机/平板）可访问',
      icon: (
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M5 12.55a11 11 0 0 1 14.08 0" />
          <path d="M1.42 9a16 16 0 0 1 21.16 0" />
          <path d="M8.53 16.11a6 6 0 0 1 6.95 0" />
          <line x1="12" y1="20" x2="12.01" y2="20" />
        </svg>
      )
    };
  }
  return {
    label: '虚拟/其他',
    tooltip: '通常为虚拟机(Hyper-V)、Docker或VPN虚拟网卡',
    // 💡 全新的 3D Box (容器/虚拟机) 图标，线条极其轻盈
    icon: (
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
        <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
        <line x1="12" y1="22.08" x2="12" y2="12" />
      </svg>
    )
  };
}

interface SubLink {
  key: string;
  label: string;
  url: string;
  /** 品牌图标路径（public/ 下的 SVG），无则显示默认链接图标 */
  icon?: string;
}

type Status = 'loading' | 'ready' | 'error';

export function SubLinks() {
  useTheme();
  const wailsReady = useWailsReady();

  const [status, setStatus] = useState<Status>('loading');
  const [errorMsg, setErrorMsg] = useState('');
  const [links, setLinks] = useState<SubLink[]>([]);
  // key → 是否刚刚复制成功（用于短暂高亮）
  const [copiedKey, setCopiedKey] = useState<string | null>(null);

  // 可切换的访问地址列表（127.0.0.1 + 各网卡的局域网 IPv4），及当前选中项
  const [hosts, setHosts] = useState<string[]>(['127.0.0.1']);
  const [selectedHost, setSelectedHost] = useState('127.0.0.1');

  // 缓存生成链接所需的信息，切换 host 时无需重新请求接口
  const [linkCtx, setLinkCtx] = useState<{
    subStorePort: string;
    path: string;
    oldVer: string;
    latestVer: string;
  } | null>(null);

  useEffect(() => {
    if (!wailsReady) return;
    load();
  }, [wailsReady]);

  // host 切换时，仅用缓存的上下文信息重建链接列表，避免重复请求
  useEffect(() => {
    if (!linkCtx) return;
    setLinks(buildLinks(selectedHost, linkCtx));
  }, [selectedHost, linkCtx]);

  function buildLinks(
    host: string,
    ctx: { subStorePort: string; path: string; oldVer: string; latestVer: string },
  ): SubLink[] {
    const subBase = `http://${host}:${ctx.subStorePort}`;
    const { path, oldVer, latestVer } = ctx;
    return [
      { key: 'common', label: '通用订阅', url: `${subBase}${path}/download/sub` },
      { key: 'v2ray', label: 'V2Ray 订阅', url: `${subBase}${path}/download/sub?target=V2Ray`, icon: '/v2ray.png' },
      { key: 'mihomo', label: 'Mihomo 订阅', url: `${subBase}${path}/api/file/mihomo`, icon: '/mihomo.png' },
      { key: 'singbox-old', label: `singbox-${oldVer} 订阅`, url: `${subBase}${path}/api/file/singbox-${oldVer}`, icon: '/singbox.png' },
      { key: 'singbox-latest', label: `singbox-${latestVer} 订阅`, url: `${subBase}${path}/api/file/singbox-${latestVer}`, icon: '/singbox.png' },
      { key: 'shadowrocket', label: 'Shadowrocket 订阅', url: `${subBase}${path}/download/sub?target=ShadowRocket`, icon: '/shadowrocket.png' },
    ];
  }

  async function load() {
    setStatus('loading');
    setErrorMsg('');
    try {
      const info = await GuiApp.GetAppInfo();

      const headers: Record<string, string> = { 'X-API-Key': info.apiKey };

      // 1. 检查 Sub-Store 是否运行
      const statusRes = await fetch('/api/status', { headers }).catch(() => null);
      if (!statusRes?.ok) throw new Error('获取状态失败，请检查服务是否运行');
      const statusData = await statusRes.json();
      if (!statusData?.isSubStoreRunning)
        throw new Error('Sub-Store 服务未运行，请在配置中启用');

      // 2. 获取 singbox 版本
      const vRes = await fetch('/api/singbox-versions', { headers }).catch(() => null);
      if (!vRes?.ok) throw new Error('获取 singbox 版本失败');
      const vData = await vRes.json();

      // 3. 校验 sub-store-path
      if (!info.subStorePath)
        throw new Error('请先在 config.yaml 中设置 sub-store-path');

      const path = `/${info.subStorePath}`;

      // 组装可切换的访问地址：本机回环地址始终排第一，
      // 其后追加后端探测到的所有局域网 IPv4（去重）。
      const detected = (info.localIPs ?? []).filter(ip => ip && ip !== '127.0.0.1');
      const hostList = ['127.0.0.1', ...Array.from(new Set(detected))];
      setHosts(hostList);
      // 若之前选中的地址在新列表中不存在（如切换配置后网卡变化），回退到第一个
      setSelectedHost(prev => (hostList.includes(prev) ? prev : hostList[0]));

      const ctx = {
        subStorePort: info.subStorePort,
        path,
        oldVer: vData.old,
        latestVer: vData.latest,
      };
      setLinkCtx(ctx);
      setLinks(buildLinks(hostList.includes(selectedHost) ? selectedHost : hostList[0], ctx));
      setStatus('ready');
    } catch (e: any) {
      setErrorMsg(e?.message ?? '获取订阅链接失败');
      setStatus('error');
    }
  }

  async function copyLink(item: SubLink) {
    try {
      await navigator.clipboard.writeText(item.url);
      setCopiedKey(item.key);
      setTimeout(() => setCopiedKey(k => (k === item.key ? null : k)), 1500);
    } catch {
      // 剪贴板失败时静默忽略，UI 无变化
    }
  }

  return (
    <div class="sl-root">
      {/* ── 标题栏（动态注入 macOS 专属 class） ── */}
      <div class={`sl-titlebar ${isMac ? 'is-mac' : ''}`}>
        <svg class="sl-titlebar-icon" width="14" height="14" viewBox="0 0 24 24"
          fill="none" stroke="currentColor" stroke-width="2"
          stroke-linecap="round" stroke-linejoin="round">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
        </svg>
        <span class="sl-titlebar-title">订阅链接</span>
        <span class="sl-titlebar-sub">点击复制订阅链接</span>
      </div>

      {/* ── 访问地址切换胶囊（本机存在多个可用地址时才显示）── */}
      {status === 'ready' && hosts.length > 1 && (
        <div class="sl-host-switch">
          <div class="sl-tabs-container">
            {hosts.map(host => {
              const meta = getIpMeta(host);
              const isActive = host === selectedHost;
              return (
                <button
                  key={host}
                  class={`sl-tab${isActive ? ' active' : ''}`}
                  onClick={() => setSelectedHost(host)}
                  title={meta.tooltip}
                >
                  {meta.icon}
                  <span class="sl-tab-ip">{host}</span>
                  <span class="sl-tab-label">{meta.label}</span>
                </button>
              );
            })}
          </div>
        </div>
      )}

      {/* ── 内容区 ── */}
      <div class="sl-body">
        {status === 'loading' && (
          <div class="sl-status">
            <svg class="sl-spinner" width="15" height="15" viewBox="0 0 24 24"
              fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 12a9 9 0 1 1-6.219-8.56" />
            </svg>
            正在获取订阅链接…
          </div>
        )}

        {status === 'error' && (
          <div class="sl-status error">
            <span class="sl-error-icon">⚠️</span>
            <span>{errorMsg}</span>
            <button
              style="margin-top:6px;padding:4px 12px;font-size:11px;cursor:pointer;border:1px solid var(--border);border-radius:4px;background:var(--bg);color:var(--fg);"
              onClick={load}
            >
              重试
            </button>
          </div>
        )}

        {status === 'ready' && links.map((item, i) => {
          const copied = copiedKey === item.key;
          return (
            <div
              key={item.key}
              class={`sl-item${copied ? ' copied' : ''}`}
              style={`animation-delay:${i * 35}ms`}
              onClick={() => copyLink(item)}
              title={item.url}
            >
              {/* 图标：复制后统一显示对勾，否则品牌图标或默认链接图标 */}
              <div class="sl-item-icon">
                {copied ? (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="2.5"
                    stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="20 6 9 17 4 12" />
                  </svg>
                ) : item.icon ? (
                  <img src={item.icon} class="sl-brand-icon" alt="" aria-hidden="true" />
                ) : (
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="2"
                    stroke-linecap="round" stroke-linejoin="round">
                    <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
                    <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
                  </svg>
                )}
              </div>

              <div class="sl-item-info">
                <div class="sl-item-label">{item.label}</div>
                <div class="sl-item-url">{item.url}</div>
              </div>

              <div class="sl-item-action">
                <span class="sl-copy-label">{copied ? '已复制' : ''}</span>
                {!copied && (
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none"
                    stroke="currentColor" stroke-width="2">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                  </svg>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* ── 底部提示 ── */}
      {status === 'ready' && (
        <div class="sl-footer">
          请在局域网或本机代理客户端中使用，远程使用请使用公网域名访问 WebUI
        </div>
      )}
    </div>
  );
}