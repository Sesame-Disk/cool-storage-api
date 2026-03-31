import React from 'react';
import { siteRoot, gettext } from '../../utils/constants';

function clearAuthStorage() {
  localStorage.removeItem('sesamefs_auth_token');
  for (const key of Object.keys(localStorage)) {
    if (key.startsWith('custom_permissions_')) {
      localStorage.removeItem(key);
    }
  }
}

export default function Logout() {
  return (
    <a className="logout-icon" href={`${siteRoot}accounts/logout/`} title={gettext('Log out')} onClick={clearAuthStorage}>
      <i className="sf3-font sf3-font-logout" style={{fontSize: '24px'}}></i>
    </a>
  );
}
