import React from 'react';
import ReactDOM from 'react-dom';

import { AdminAccessDenied, BootstrapLoadError, getAdminDeniedProps, getBootstrapErrorProps, loadBootstrap } from '../../bootstrap/runtime-bootstrap';

loadBootstrap('sys').then((data) => {
    const bootstrapErrorProps = getBootstrapErrorProps(data);
    if (bootstrapErrorProps) {
        ReactDOM.render(
            <BootstrapLoadError {...bootstrapErrorProps} />,
            document.getElementById('wrapper')
        );
        return;
    }

    const deniedProps = getAdminDeniedProps(data, 'sys');
    if (!deniedProps) {
        return import('./index');
    }

    ReactDOM.render(
        <AdminAccessDenied {...deniedProps} />,
        document.getElementById('wrapper')
    );
});