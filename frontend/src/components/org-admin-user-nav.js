import React from 'react';
import PropTypes from 'prop-types';
import { Link } from '@gatsbyjs/reach-router';
import { normalizeEmailRouteParam } from '../utils/email-route';
import { siteRoot, gettext } from '../utils/constants';

const propTypes = {
  email: PropTypes.string.isRequired,
  currentItem: PropTypes.string.isRequired,
  manageInAccountsURL: PropTypes.string,
};

class OrgAdminUserNav extends React.Component {

  render() {
    const { email, currentItem, manageInAccountsURL } = this.props;
    const urlBase = `${siteRoot}org/useradmin/info/${encodeURIComponent(normalizeEmailRouteParam(email))}/`;
    return (
      <div className="cur-view-path org-admin-user-nav">
        <ul className="nav">
          <li className="nav-item">
            <Link to={urlBase} className={`nav-link${currentItem === 'profile' ? ' active' : ''}`}>{gettext('Profile')}</Link>
          </li>
          <li className="nav-item">
            <Link to={`${urlBase}repos/`} className={`nav-link${currentItem === 'owned-repos' ? ' active' : ''}`}>{gettext('Owned Libraries')}</Link>
          </li>
          <li className="nav-item">
            <Link to={`${urlBase}shared-repos/`} className={`nav-link${currentItem === 'shared-repos' ? ' active' : ''}`}>{gettext('Shared Libraries')}</Link>
          </li>
          {manageInAccountsURL && (
            <li className="nav-item">
              <a href={manageInAccountsURL} className="nav-link" target="_blank" rel="noopener noreferrer">
                <i className="fas fa-external-link-alt mr-1"></i>{gettext('Manage in Accounts')}
              </a>
            </li>
          )}
        </ul>
      </div>
    );
  }
}

OrgAdminUserNav.propTypes = propTypes;

export default OrgAdminUserNav;
