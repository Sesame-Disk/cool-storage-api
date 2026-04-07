import React from 'react';
import ReactDOM from 'react-dom';

import { AdminAccessDenied, BootstrapLoadError, getAdminDeniedProps, getBootstrapErrorProps, loadBootstrap } from '../../bootstrap/runtime-bootstrap';
import { isAuthenticated } from '../../utils/auth-state';

// Fast path: skip the bootstrap fetch if there is no local session. See the
// sys-admin bootstrap-entry for the rationale.
if (!isAuthenticated()) {
  if (typeof window.gettext !== 'function') {
    window.gettext = (message) => message;
  }
  ReactDOM.render(
    <AdminAccessDenied
      title={window.gettext('Authentication required')}
      message={window.gettext('Log in to continue to the organization admin panel.')}
    />,
    document.getElementById('wrapper')
  );
} else {
  loadBootstrap('org').then((data) => {
    const bootstrapErrorProps = getBootstrapErrorProps(data);
    if (bootstrapErrorProps) {
      ReactDOM.render(
        <BootstrapLoadError {...bootstrapErrorProps} />,
        document.getElementById('wrapper')
      );
      return;
    }

    const deniedProps = getAdminDeniedProps(data, 'org');
    if (!deniedProps) {
      return import('./index');
    }

    ReactDOM.render(
      <AdminAccessDenied {...deniedProps} />,
      document.getElementById('wrapper')
    );
  });
}
