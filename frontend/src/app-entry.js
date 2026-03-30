import './services/css.css';

import { loadBootstrap } from './bootstrap/runtime-bootstrap';

loadBootstrap('app').finally(() => {
    import('./app');
});