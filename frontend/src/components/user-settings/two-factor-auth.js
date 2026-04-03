import React from 'react';
import { gettext, siteRoot } from '../../utils/constants';

function getSettingsPageOptions() {
  return window.app?.pageOptions || {};
}

class TwoFactorAuthentication extends React.Component {

  constructor(props) {
    super(props);
  }

  renderEnabled = () => {
    const pageOptions = getSettingsPageOptions();
    const backupTokens = Array.isArray(pageOptions.backupTokens)
      ? pageOptions.backupTokens.length
      : (Number(pageOptions.backupTokens) || 0);
    return (
      <React.Fragment>
        <p className="mb-2">{gettext('Status: enabled')}</p>
        <a className="btn btn-outline-primary mb-4" href={`${siteRoot}profile/two_factor_authentication/disable/`}>
          {gettext('Disable Two-Factor Authentication')}</a>
        <p className="mb-2">
          {gettext('If you don\'t have any device with you, you can access your account using backup codes.')}
          {backupTokens === 1 ? gettext('You have only one backup code remaining.') :
            gettext('You have {num} backup codes remaining.').replace('{num}', backupTokens)}
        </p>
        <a href={`${siteRoot}profile/two_factor_authentication/backup/tokens/`}
          className="btn btn-outline-primary">{gettext('Show Codes')}</a>
      </React.Fragment>
    );
  };

  renderDisabled = () => {
    return (
      <React.Fragment>
        <p className="mb-2">{gettext('Two-factor authentication is not enabled for your account. Enable two-factor authentication for enhanced account security.')}</p>
        <a href={`${siteRoot}profile/two_factor_authentication/setup/`} className="btn btn-outline-primary">
          {gettext('Enable Two-Factor Authentication')}</a>
      </React.Fragment>
    );
  };

  render() {
    const pageOptions = getSettingsPageOptions();
    const defaultDevice = pageOptions.defaultDevice || '';
    return (
      <div className="setting-item" id="two-factor-auth">
        <h3 className="setting-item-heading">{gettext('Two-Factor Authentication')}</h3>
        {defaultDevice ? this.renderEnabled() : this.renderDisabled()}
      </div>
    );
  }
}

export default TwoFactorAuthentication;
