import React from 'react';
import { gettext } from '../../utils/constants';
import ModalPortal from '../modal-portal';
import ConfirmDisconnectDingtalk from '../dialog/confirm-disconnect-dingtalk';
import { getSettingsPageOptions, getSettingsRoute } from './page-options';

class SocialLoginDintalk extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      isConfirmDialogOpen: false
    };
  }

  confirmDisconnect = () => {
    this.setState({
      isConfirmDialogOpen: true
    });
  };

  toggleDialog = () => {
    this.setState({
      isConfirmDialogOpen: !this.state.isConfirmDialogOpen
    });
  };

  render() {
    const pageOptions = getSettingsPageOptions();
    const csrfToken = pageOptions.csrfToken || '';
    const langCode = pageOptions.langCode || 'en';
    const socialConnectedDingtalk = Boolean(pageOptions.socialConnectedDingtalk);
    const socialNextPage = pageOptions.socialNextPage || '/';
    const connectUrl = getSettingsRoute('dingtalkConnect', 'dingtalk/connect/?next={next}', {
      next: socialNextPage
    });
    const disconnectUrl = getSettingsRoute('dingtalkDisconnect', 'dingtalk/disconnect/?next={next}', {
      next: socialNextPage
    });
    return (
      <React.Fragment>
        <div className="setting-item" id="social-auth">
          <h3 className="setting-item-heading">{gettext('Social Login')}</h3>
          <p className="mb-2">{langCode === 'zh-cn' ? '钉钉' : 'Dingtalk'}</p>
          {socialConnectedDingtalk ?
            <button className="btn btn-outline-primary" onClick={this.confirmDisconnect}>{gettext('Disconnect')}</button> :
            <a href={connectUrl} className="btn btn-outline-primary">{gettext('Connect')}</a>
          }
        </div>
        {this.state.isConfirmDialogOpen && (
          <ModalPortal>
            <ConfirmDisconnectDingtalk
              formActionURL={disconnectUrl}
              csrfToken={csrfToken}
              toggle={this.toggleDialog}
            />
          </ModalPortal>
        )}
      </React.Fragment>
    );
  }
}

export default SocialLoginDintalk;
