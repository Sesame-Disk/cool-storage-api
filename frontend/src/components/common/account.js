import React, { Component } from 'react';
import PropTypes from 'prop-types';
import ReactDOM from 'react-dom';
import { Utils } from '../../utils/utils';
import { seafileAPI } from '../../utils/seafile-api';
import { clearAuth } from '../../utils/auth-state';
import { siteRoot, isPro, gettext, appAvatarURL, enableSSOToThirdpartWebsite } from '../../utils/constants';
import toaster from '../toast';
import UpgradeEntry from './upgrade-entry';

const isCurrentOrgOwner = () => window.app?.pageOptions?.isOrgOwner === true;

const propTypes = {
  isAdminPanel: PropTypes.bool,
};

class Account extends Component {
  constructor(props) {
    super(props);
    this.state = {
      showInfo: false,
      userName: '',
      contactEmail: '',
      quotaUsage: '',
      quotaTotal: '',
      trafficUpload: '',
      trafficDownload: '',
      hasTrafficQuota: false,
      isStaff: false,
      isOrgStaff: false,
      usageRate: '',
      enableSubscription: false,
    };
    this.isFirstMounted = true;
  }

  componentDidUpdate(prevProps) {
    this.handleProps();
  }

  getContainer = () => {
    return ReactDOM.findDOMNode(this);
  };

  handleProps = () => {
    if (this.state.showInfo) {
      this.addEvents();
    } else {
      this.removeEvents();
    }
  };

  addEvents = () => {
    ['click', 'touchstart', 'keyup'].forEach(event =>
      document.addEventListener(event, this.handleDocumentClick, true)
    );
  };

  removeEvents = () => {
    ['click', 'touchstart', 'keyup'].forEach(event =>
      document.removeEventListener(event, this.handleDocumentClick, true)
    );
  };

  handleDocumentClick = (e) => {
    if (e && (e.which === 3 || (e.type === 'keyup' && e.which !== Utils.keyCodes.tab))) return;
    const container = this.getContainer();

    if (container.contains(e.target) && container !== e.target && (e.type !== 'keyup' || e.which === Utils.keyCodes.tab)) {
      return;
    }

    this.setState({
      showInfo: !this.state.showInfo,
    });
  };

  onClickAccount = (e) => {
    e.preventDefault();
    if (this.isFirstMounted) {
      seafileAPI.getAccountInfo().then(resp => {
        const storage = resp.data.storage || {};
        const storageQuota = typeof storage.quota === 'number' ? storage.quota : resp.data.total;
        const storageUsed = typeof storage.used === 'number' ? storage.used : resp.data.usage;
        const storagePercent = typeof storage.percent === 'number'
          ? `${Math.min(storage.percent, 100)}%`
          : resp.data.space_usage;
        const uploadQuota = resp.data.traffic_upload_quota;
        const uploadUsed = resp.data.traffic_upload_used || 0;
        const downloadQuota = resp.data.traffic_download_quota;
        const downloadUsed = resp.data.traffic_download_used || 0;
        this.setState({
          userName: resp.data.name,
          contactEmail: resp.data.email,
          usageRate: storagePercent,
          quotaUsage: Utils.bytesToSize(storageUsed || 0),
          quotaTotal: storageQuota > 0 ? Utils.bytesToSize(storageQuota) : gettext('Unlimited'),
          trafficUpload: `${Utils.bytesToSize(uploadUsed)} / ${uploadQuota > 0 ? Utils.bytesToSize(uploadQuota) : 'Unlimited'}`,
          trafficDownload: `${Utils.bytesToSize(downloadUsed)} / ${downloadQuota > 0 ? Utils.bytesToSize(downloadQuota) : 'Unlimited'}`,
          hasTrafficQuota: uploadQuota > 0 || downloadQuota > 0 || uploadUsed > 0 || downloadUsed > 0,
          isStaff: resp.data.is_staff,
          isInstAdmin: resp.data.is_inst_admin,
          isOrgStaff: resp.data.is_org_staff === 1 ? true : false,
          showInfo: !this.state.showInfo,
          enableSubscription: resp.data.enable_subscription,
        });
      }).catch(error => {
        let errMessage = Utils.getErrorMsg(error);
        toaster.danger(errMessage);
      });
      this.isFirstMounted = false;
    } else {
      this.setState({ showInfo: !this.state.showInfo });
    }
  };

  renderMenu = () => {
    let data;
    const { isStaff, isOrgStaff, isInstAdmin } = this.state;

    if (this.props.isAdminPanel) {
      if (isStaff) {
        data = {
          url: siteRoot,
          text: gettext('Exit System Admin')
        };
      } else if (isOrgStaff) {
        data = {
          url: siteRoot,
          text: gettext('Exit Organization Admin')
        };
      } else if (isInstAdmin) {
        data = {
          url: siteRoot,
          text: gettext('Exit Institution Admin')
        };
      }

    } else {
      if (isStaff) {
        data = {
          url: `${siteRoot}sys/info/`,
          text: gettext('System Admin')
        };
      } else if (isOrgStaff) {
        data = {
          url: `${siteRoot}org/info/`,
          text: gettext('Organization Admin')
        };
      } else if (isPro && isInstAdmin) {
        data = {
          url: `${siteRoot}inst/useradmin/`,
          text: gettext('Institution Admin')
        };
      }
    }

    return data && <a href={data.url} title={data.text} className="item">{data.text}</a>;
  };

  renderAvatar = () => {
    return (<img src={appAvatarURL} width="36" height="36" className="avatar" alt={gettext('Avatar')} />);
  };

  render() {
    return (
      <div id="account">
        <button type="button" id="my-info" onClick={this.onClickAccount} className="account-toggle no-deco d-none d-md-block border-0 bg-transparent p-0" aria-label={gettext('View profile and more')}>
          {this.renderAvatar()}
        </button>
        <span className="account-toggle sf2-icon-more mobile-icon d-md-none" aria-label={gettext('View profile and more')} onClick={this.onClickAccount}></span>
        <div id="user-info-popup" className={`account-popup sf-popover ${this.state.showInfo ? '' : 'hide'}`}>
          <div className="outer-caret up-outer-caret">
            <div className="inner-caret"></div>
          </div>
          <div className="sf-popover-con">
            <div className="item o-hidden">
              {this.renderAvatar()}
              <div className="txt">{this.state.userName}</div>
            </div>
            <div id="space-traffic">
              <div className="item">
                <p>{gettext('Used:')}{' '}{this.state.quotaUsage} / {this.state.quotaTotal}</p>
                <div id="quota-bar"><span id="quota-usage" className="usage" style={{ width: this.state.usageRate }}></span></div>
              </div>
              {this.state.hasTrafficQuota &&
                <div className="item pt-2">
                  <p className="mb-1">{gettext('Monthly Upload Traffic')}{' '}{this.state.trafficUpload}</p>
                  <p className="mb-0">{gettext('Monthly Download Traffic')}{' '}{this.state.trafficDownload}</p>
                </div>
              }
            </div>
            <UpgradeEntry isOrgStaff={this.state.isOrgStaff} />
            <a href={siteRoot + 'profile/'} className="item">{gettext('Settings')}</a>
            {(this.state.enableSubscription && isCurrentOrgOwner()) && <a href={siteRoot + 'org/subscription/'} className="item">{gettext('Subscription')}</a>}
            {this.renderMenu()}
            {enableSSOToThirdpartWebsite && <a href={siteRoot + 'sso-to-thirdpart/'} className="item">{gettext('Customer Portal')}</a>}
            <a href={siteRoot + 'accounts/logout/'} className="item" onClick={clearAuth}>{gettext('Log out')}</a>
          </div>
        </div>
      </div>
    );
  }
}

Account.defaultProps = {
  isAdminPanel: false
};

Account.propTypes = propTypes;

export default Account;
