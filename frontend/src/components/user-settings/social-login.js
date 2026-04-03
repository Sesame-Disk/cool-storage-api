import React from 'react';
import { gettext } from '../../utils/constants';
import ModalPortal from '../modal-portal';
import ConfirmDisconnectWechat from '../dialog/confirm-disconnect-wechat';
import { getSettingsPageOptions, getSettingsRoute } from './page-options';

class SocialLogin extends React.Component {

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
    const socialConnected = Boolean(pageOptions.socialConnected);
    const socialNextPage = pageOptions.socialNextPage || '/';
    const connectUrl = getSettingsRoute('wechatWorkConnect', 'work-weixin/oauth-connect/?next={next}', {
      next: socialNextPage
    });
    const disconnectUrl = getSettingsRoute('wechatWorkDisconnect', 'work-weixin/oauth-disconnect/?next={next}', {
      next: socialNextPage
    });
    return (
      <React.Fragment>
        <div className="setting-item" id="social-auth">
          <h3 className="setting-item-heading">{gettext('Social Login')}</h3>
          <p className="mb-2">{langCode === 'zh-cn' ? '企业微信' : 'WeChat Work'}</p>
          {socialConnected ?
            <button className="btn btn-outline-primary" onClick={this.confirmDisconnect}>{gettext('Disconnect')}</button> :
            <a href={connectUrl} className="btn btn-outline-primary">{gettext('Connect')}</a>
          }
        </div>
        {this.state.isConfirmDialogOpen && (
          <ModalPortal>
            <ConfirmDisconnectWechat
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

export default SocialLogin;
