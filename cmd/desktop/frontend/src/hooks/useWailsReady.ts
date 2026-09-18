/**
 * frontend/src/hooks/useWailsReady.ts
 *
 * 等待 Wails3 运行时就绪。
 * Wails3 alpha 的 JS 桥接层在 window 上挂载完成前，
 * 调用任何绑定都会抛出异常。
 * 通过轮询 window._wails（运行时内部标志）或超时兜底来判断就绪。
 */
import { useEffect, useState } from 'preact/hooks';

const POLL_INTERVAL = 50;   // ms
const TIMEOUT_MS    = 6000; // 6s 超时

export function useWailsReady(): boolean {
  const [ready, setReady] = useState(false);

  useEffect(() => {
    // 检测 Wails3 运行时是否已完成初始化
    // 运行时在 window 上注入 `__wails_ipc__` / `window.chrome.webview` 等桥接对象
    function isWailsBridgeReady(): boolean {
      // Wails3 在 wailsRuntimeReady 全局变量上置 true
      if ((window as any).wailsRuntimeReady === true) return true;
      // 兼容：检测 wails 内部桥接（window.__wails 或 chrome.webview）
      if (typeof (window as any).__wails !== 'undefined') return true;
      // 降级：检测是否有 IPC 桥
      if (typeof (window as any).chrome?.webview !== 'undefined') return true;
      if (typeof (window as any).webkit?.messageHandlers !== 'undefined') return true;
      return false;
    }

    // 若已就绪（例如在 dev server 中通过 HTTP 访问时），直接置 true
    if (isWailsBridgeReady()) {
      setReady(true);
      return;
    }

    let elapsed = 0;
    const timer = setInterval(() => {
      elapsed += POLL_INTERVAL;
      if (isWailsBridgeReady()) {
        clearInterval(timer);
        setReady(true);
      } else if (elapsed >= TIMEOUT_MS) {
        clearInterval(timer);
        // 超时后仍设为 ready，让上层处理 API 调用失败
        console.warn('[useWailsReady] Wails bridge not detected after timeout, proceeding anyway.');
        setReady(true);
      }
    }, POLL_INTERVAL);

    return () => clearInterval(timer);
  }, []);

  return ready;
}
