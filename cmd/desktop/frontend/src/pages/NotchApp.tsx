import { useEffect, useState } from 'preact/hooks';
// 从 GuiApp 的绑定文件中引入包装好的方法
import { GuiApp } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';

export function NotchApp() {
  const [state, setState] = useState({
    step: '准备中...',
    percent: 0,
    isDone: false
  });

  useEffect(() => {
    const timer = setInterval(async () => {
      try {
        // 调用 GuiApp 中的统一状态 API
        const st = await GuiApp.GetCheckState();

        if (st.isChecking) {
          let percent = 0;
          if (st.proxyCount > 0) {
            percent = (st.progress / st.proxyCount) * 100;
          } else if (st.stepName === '保存中') {
            percent = 100;
          }
          setState({ step: st.stepName || '进度', percent, isDone: false });
        } else {
          // 检测结束，拉取最终结果
          setState({
            step: st.lastResult || '检测完成',
            percent: 100,
            isDone: true
          });
        }
      } catch (e) {
        // 出现网络错误或取消时不中断循环
      }
    }, 500);

    return () => clearInterval(timer);
  }, []);

  return (
    <div style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: state.isDone ? 'center' : 'space-between',
      height: '100vh',
      width: '100vw',
      padding: '0 24px',
      boxSizing: 'border-box',
      background: '#000',
      color: '#fff',
      fontFamily: 'system-ui, -apple-system, sans-serif',
      fontSize: '13.5px',
      fontWeight: 500,
      userSelect: 'none',
      overflow: 'hidden'
    }}>
      {state.isDone ? (
        // ── 状态：已完成（展示最终结果） ──
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px', color: '#10b981' }}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="20 6 9 17 4 12"></polyline>
          </svg>
          <span style={{ fontWeight: 600 }}>{state.step}</span>
        </div>
      ) : (
        // ── 状态：检测中（左边状态，右边百分比） ──
        <>
          <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            {/* 转圈动画 */}
            <div class="spinner-sm" style={{ width: '12px', height: '12px', borderTopColor: '#fff', borderColor: 'rgba(255,255,255,0.2)' }}></div>
            <span style={{ opacity: 0.9 }}>{state.step}</span>
          </div>
          <div style={{
            color: '#0ea5a0',
            fontVariantNumeric: 'tabular-nums',
            fontWeight: 600,
            letterSpacing: '0.5px'
          }}>
            {state.percent.toFixed(1)}%
          </div>
        </>
      )}
    </div>
  );
}