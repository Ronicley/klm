import React from 'react';
import ReactDOM from 'react-dom/client';
import '@fontsource/inter/400.css';
import '@fontsource/inter/500.css';
import '@fontsource/inter/600.css';
import '@fontsource/inter/700.css';
import './design-system/tokens.css';
import './design-system/styles.css';
import './styles.css';
import { App } from './App';
import { IS_DESKTOP, IS_MOBILE_HOST } from './platform';
import './features/workspace/mobile-workspace.css';
import './web-compat';

document.documentElement.classList.toggle('desktop-client', IS_DESKTOP);
document.documentElement.classList.toggle('mobile-client', IS_MOBILE_HOST);

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode><App /></React.StrictMode>,
);
