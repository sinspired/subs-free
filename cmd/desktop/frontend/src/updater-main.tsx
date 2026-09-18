import { render } from 'preact';
import { UpdaterApp } from './pages/UpdaterApp';

render(<UpdaterApp />, document.getElementById('app') as HTMLElement);