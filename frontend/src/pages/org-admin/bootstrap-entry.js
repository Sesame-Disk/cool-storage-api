import React from 'react';
import ReactDOM from 'react-dom';

import { AdminAccessDenied, BootstrapLoadError, getAdminDeniedProps, getBootstrapErrorProps, loadBootstrap } from '../../bootstrap/runtime-bootstrap';

loadBootstrap('org').then((data) => {
  const deniedProps = getAdminDeniedProps(data, 'org');
  if (deniedProps) {
    ReactDOM.render(
      <AdminAccessDenied {...deniedProps} />,
      document.getElementById('wrapper')
    );
    return;
  }

  const bootstrapErrorProps = getBootstrapErrorProps(data);
  if (bootstrapErrorProps) {
    ReactDOM.render(
      <BootstrapLoadError {...bootstrapErrorProps} />,
      document.getElementById('wrapper')
    );
    return;
  }

  return import('./index');
});
