/**
 * frontend/src/components/PortConflict.tsx
 *
 * 端口冲突解决界面。
 *
 * 流程：
 *   1. 首次渲染 → 自动对冲突端口调用 ValidatePort() 检测
 *   2. 输入变化 → debounce 300ms → ValidatePort()（格式 + 占用双检）
 *   3. 全部 ok → 按钮可点 → SetPorts() → CompleteInit() → onFixed()
 *
 * 字段显示规则：
 *   - 仅 HTTP 冲突   → 单列，只显示 HTTP 字段
 *   - 仅 Sub 冲突    → 单列，只显示 Sub-Store 字段
 *   - 两者均冲突     → 双列，两字段并排
 */
import { useEffect, useRef, useState } from 'preact/hooks';
import { GuiApp } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';
import { AppInfo } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';

interface Props {
  info: AppInfo;
  toast: (msg: string) => void;
  onFixed: (newInfo: AppInfo) => void;
}

type PortStatus = 'idle' | 'checking' | 'ok' | 'error';

interface FieldState {
  value: string;
  status: PortStatus;
  errMsg: string;
}

function initField(port: string): FieldState {
  return { value: port, status: 'idle', errMsg: '' };
}

export function PortConflict({ info, toast, onFixed }: Props) {
  // 根据后端实际冲突标志决定显示哪些字段
  const showHTTP = !!info.portConflictHTTP;
  const showSub = !!(info.portConflictSubStore && info.subStorePort);
  const showBoth = showHTTP && showSub;

  const subtitle = showBoth
    ? '以下两个端口均被占用，请各自修改后启动'
    : showHTTP
      ? 'HTTP 监听端口已被占用，请修改后启动'
      : 'Sub-Store 端口已被占用，请修改后启动';

  const [http, setHttp] = useState<FieldState>(initField(info.listenPort || '8199'));
  const [sub, setSub] = useState<FieldState>(initField(info.subStorePort || ''));
  const [applying, setApplying] = useState(false);

  const httpTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const subTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // 所有可见字段均通过校验时才允许提交
  const canApply =
    !applying &&
    (!showHTTP || http.status === 'ok') &&
    (!showSub || sub.status === 'ok');

  // ── 实时校验（debounce 300ms）────────────────────────────────
  function scheduleCheck(
    value: string,
    setField: (s: FieldState) => void,
    timerRef: { current: ReturnType<typeof setTimeout> | null },
  ) {
    if (timerRef.current) clearTimeout(timerRef.current);
    const v = value.trim();
    if (!v) {
      setField({ value, status: 'idle', errMsg: '' });
      return;
    }
    setField({ value, status: 'checking', errMsg: '' });
    timerRef.current = setTimeout(async () => {
      try {
        const err = await GuiApp.ValidatePort(v);
        setField({ value, status: err ? 'error' : 'ok', errMsg: err ?? '' });
      } catch {
        setField({ value, status: 'error', errMsg: '检测失败，请重试' });
      }
    }, 300);
  }

  // 首次渲染自动触发各冲突字段的校验
  useEffect(() => {
    if (showHTTP) scheduleCheck(http.value, setHttp, httpTimer);
    if (showSub) scheduleCheck(sub.value, setSub, subTimer);
  }, []);

  // ── 应用端口并启动服务 ────────────────────────────────────────
  async function applyPorts() {
    setApplying(true);

    // 未冲突的端口保持原值不变
    const newHTTP = showHTTP ? http.value.trim() : (info.listenPort || '');
    const newSub = showSub ? sub.value.trim() : (info.subStorePort || '');

    try {
      await GuiApp.SetPorts(newHTTP, newSub);
    } catch (e: any) {
      toast('❌ ' + (e?.message || '端口设置失败'));
      setApplying(false);
      return;
    }

    // pendingInit 在此视图下必为 true，直接调用 CompleteInit()
    try {
      await GuiApp.CompleteInit();
    } catch (e: any) {
      toast('❌ 服务启动失败：' + (e?.message || '未知错误'));
      setApplying(false);
      return;
    }

    let newInfo: AppInfo;
    try {
      newInfo = await GuiApp.GetAppInfo();
    } catch {
      toast('获取应用信息失败');
      setApplying(false);
      return;
    }

    setApplying(false);
    onFixed(newInfo);
  }

  // ── 渲染 ─────────────────────────────────────────────────────
  return (
    <div class={`pc-root${showBoth ? ' pc-root--wide' : ''}`}>

      {/* 标题区 */}
      <div class="pc-header">
        <div class="pc-icon">
          <svg width="26" height="26" viewBox="0 0 24 24" fill="none"
            stroke="currentColor" stroke-width="1.8"
            stroke-linecap="round" stroke-linejoin="round">
            <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            <line x1="12" y1="9" x2="12" y2="13" />
            <line x1="12" y1="17" x2="12.01" y2="17" />
          </svg>
        </div>
        <div>
          <h2 class="pc-title">端口冲突</h2>
          <p class="pc-subtitle">{subtitle}</p>
        </div>
      </div>

      {/* 端口字段区 */}
      <div class={`pc-fields${showBoth ? ' pc-fields--row' : ''}`}>

        {showHTTP && (
          <div class={`pc-field${http.status === 'error' ? ' has-error' : ''}${http.status === 'ok' ? ' has-ok' : ''}`}>
            <div class="pc-field-top">
              <label class="pc-label">
                HTTP
                <span class={`pc-conflict-tag${http.status === 'ok' ? ' ok' : ''}`}>
                  {http.status === 'checking' ? '检测中…' : http.status === 'ok' ? '可用' : '冲突'}
                </span>
              </label>
            </div>
            <input
              class="pc-input"
              type="number"
              min="1024" max="65535"
              value={http.value}
              disabled={applying}
              onInput={e => scheduleCheck((e.target as HTMLInputElement).value, setHttp, httpTimer)}
              placeholder="1024 – 65535"
            />
            {http.status === 'error' && <p class="pc-errmsg">{http.errMsg}</p>}
            <p class="pc-hint">订阅转换 Web 服务</p>
          </div>
        )}

        {showSub && (
          <div class={`pc-field${sub.status === 'error' ? ' has-error' : ''}${sub.status === 'ok' ? ' has-ok' : ''}`}>
            <div class="pc-field-top">
              <label class="pc-label">
                Sub-Store
                <span class={`pc-conflict-tag${sub.status === 'ok' ? ' ok' : ''}`}>
                  {sub.status === 'checking' ? '检测中…' : sub.status === 'ok' ? '可用' : '冲突'}
                </span>
              </label>
            </div>
            <input
              class="pc-input"
              type="number"
              min="1024" max="65535"
              value={sub.value}
              disabled={applying}
              onInput={e => scheduleCheck((e.target as HTMLInputElement).value, setSub, subTimer)}
              placeholder="1024 – 65535"
            />
            {sub.status === 'error' && <p class="pc-errmsg">{sub.errMsg}</p>}
            <p class="pc-hint">Sub-Store 管理服务</p>
          </div>
        )}
      </div>

      {/* 摘要状态 */}
      <div class="pc-summary">
        {applying
          ? <span class="pc-summary-wait">正在启动服务，请稍候…</span>
          : canApply
            ? <span class="pc-summary-ok">✓ 端口可用，点击下方按钮启动</span>
            : <span class="pc-summary-hint">请修改端口直到显示「✓ 可用」</span>}
      </div>

      {/* 应用按钮 */}
      <button class="pc-apply-btn" onClick={applyPorts} disabled={!canApply}>
        {applying
          ? <><span class="spinner-sm" />正在启动…</>
          : '应用端口并启动服务'}
      </button>

    </div>
  );
}