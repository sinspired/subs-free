import { JSX } from 'preact';
import { Window } from '@wailsio/runtime';

interface Props {
  theme: string;
  toggleTheme: () => void;
  onRequestClose: () => void;
}

export function Header({ theme, toggleTheme, onRequestClose }: Props): JSX.Element {
  const isDark = theme === 'dark';

  return (
    <div class="hdr">
      {/* 窗口控制按钮（无边框模式）*/}
      <div class="win-controls">
        <button
          class="wc-btn wc-close"
          onClick={onRequestClose}
          title="关闭"
          aria-label="关闭"
        />
        <button
          class="wc-btn wc-minimize"
          onClick={() => Window.Minimise()}
          title="最小化"
          aria-label="最小化"
        />
        <button
          class="wc-btn wc-maximize"
          onClick={() => Window.ToggleMaximise()}
          title="最大化"
          aria-label="最大化"
        />
      </div>

      {/* Logo */}
      <svg class="logo-icon" viewBox="0 0 36 36" fill="none" xmlns="http://www.w3.org/2000/svg">
        <circle cx="18" cy="18" r="18" fill="var(--accent)" />
        <path d="M10 18h16M18 10v16" stroke="#fff" stroke-width="2.5" stroke-linecap="round" />
        <circle cx="18" cy="18" r="6" stroke="#fff" stroke-width="2" />
      </svg>

      {/* 文本 */}
      <div class="hdr-text">
        <div class="name">
          Subs Check <span class="pro">Pro⁺</span>
        </div>
        <div class="sub">桌面版</div>
      </div>

      {/* 主题切换按钮 */}
      <button class="icon-btn theme-btn" onClick={toggleTheme} title="切换主题">
        {!isDark && (
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none"
            stroke="currentColor" stroke-width="2">
            <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
          </svg>
        )}
        {isDark && (
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none"
            stroke="currentColor" stroke-width="2">
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
}
