/**
 * frontend/src/components/QuitDialog.tsx
 */
import { GuiApp } from '../../bindings/github.com/sinspired/subs-free/cmd/desktop';

interface Props {
  onClose: () => void;
}

export function QuitDialog({ onClose }: Props) {
  async function handleMinimize() {
    onClose();
    await GuiApp.HideToTray();
  }

  async function handleQuit() {
    onClose();
    await GuiApp.QuitApp();
  }

  return (
    <div class="quit-overlay" onClick={onClose}>
      <div
        class="quit-dialog"
        onClick={(e: MouseEvent) => e.stopPropagation()}
      >
        <div class="quit-header">
          <div class="quit-icon-wrap">
            <div class="quit-icon">
              <img src="/logo.svg" alt="logo" class="quit-Dialog-icon" />
            </div>
          </div>

          <div class="quit-headings">
            <div class="quit-title">关闭程序</div>
            <div class="quit-desc">
              请选择关闭方式
            </div>
          </div>
        </div>

        <div class="quit-actions">
          <button
            class="quit-btn quit-btn-secondary"
            onClick={() => { void handleMinimize(); }}
          >
            <div class="quit-btn-icon">🗕</div>

            <div class="quit-btn-content">
              <div class="quit-btn-title">
                最小化到托盘
              </div>

              <div class="quit-btn-sub">
                后台继续运行检测任务
              </div>
            </div>
          </button>

          <button
            class="quit-btn quit-btn-danger"
            onClick={() => { void handleQuit(); }}
          >
            <div class="quit-btn-icon">✕</div>

            <div class="quit-btn-content">
              <div class="quit-btn-title">
                退出程序
              </div>

              <div class="quit-btn-sub">
                停止所有检测并退出
              </div>
            </div>
          </button>
        </div>

        <div class="quit-footer">
          <button class="quit-cancel" onClick={onClose}>
            取消
          </button>
        </div>
      </div>
    </div>
  );
}