import React from 'react';
import { gettext } from '../utils/constants';
import { getLoginURL } from '../utils/auth-state';

function PermissionDeniedTip() {
  let reloginUrl = getLoginURL('required');
  let errorTip = gettext('Permission denied. Please try {placeholder-left}login again.{placeholder-right}');
  errorTip = errorTip.replace('{placeholder-left}', '<a class="action-link p-0" href=' + reloginUrl + '>');
  errorTip = errorTip.replace('{placeholder-right}', '</a>');
  return (
    <span className="error" dangerouslySetInnerHTML={{ __html: errorTip }}></span>
  );
}

export default PermissionDeniedTip;
