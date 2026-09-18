/**
 * frontend/src/notch.tsx
 * 灵动岛窗口专属入口文件
 */
import { render } from 'preact';
import { NotchApp } from './pages/NotchApp';

render(<NotchApp />, document.getElementById('app')!);