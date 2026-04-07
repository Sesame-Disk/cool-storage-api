import React from 'react';
import ReactDOM from 'react-dom';

import { AdminAccessDenied, BootstrapLoadError, getAdminDeniedProps, getBootstrapErrorProps, loadBootstrap } from '../../bootstrap/runtime-bootstrap';
import { isAuthenticated } from '../../utils/auth-state';

// Fast path: if there is no local session at all, skip the bootstrap fetch and
// render the "log in required" screen immediately. Avoids an unauthenticated
// round-trip and stops us from showing an ambiguous error when the real issue
// is "never logged in".
if (!isAuthenticated()) {
  if (typeof window.gettext !== 'function') {
    window.gettext = (message) => message;
  }
  ReactDOM.render(
    <AdminAccessDenied
      title={window.gettext('Authentication required')}
      message={window.gettext('Log in to continue to the system admin panel.')}
    />,
    document.getElementById('wrapper')
  );
} else {
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
}
