import { render } from 'preact';
import { AboutApp } from './pages/AboutApp';

render(<AboutApp />, document.getElementById('app') as HTMLElement);