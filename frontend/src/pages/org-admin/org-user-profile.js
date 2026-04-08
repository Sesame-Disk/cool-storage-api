import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { seafileAPI } from '../../utils/seafile-api';
import { accountsOrgUserManagementURL, gettext, orgID, orgName } from '../../utils/constants';
import { ACCOUNTS_ORG_USER_ACTIONS, ACCOUNTS_ORG_USER_VIEWS, buildAccountsOrgUserManagementURL } from '../../utils/accounts-org-user-management';
import { Utils } from '../../utils/utils';
import Loading from '../../components/loading';
import OrgAdminUserNav from '../../components/org-admin-user-nav';
import SetOrgUserName from '../../components/dialog/set-org-user-name';
import SetOrgUserContactEmail from '../../components/dialog/set-org-user-contact-email';
import SetOrgUserQuota from '../../components/dialog/set-org-user-quota';
import MainPanelTopbar from './main-panel-topbar';

import '../../css/org-admin-user.css';

class OrgUserProfile extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      userWritesDisabled: false,
      accountsOrgManagementURL: accountsOrgUserManagementURL,
    };
  }

  componentDidMount() {
    const email = this.props.email;
    seafileAPI.orgAdminGetOrgUserInfo(orgID, email).then((res) => {
      this.setState(Object.assign({
        loading: false
      }, res.data));
    }).catch((error) => {
      this.setState({
        loading: false,
        errorMsg: Utils.getErrorMsg(error, true) // true: show login tip if 403
      });
    });
    seafileAPI.orgAdminGetOrgInfo().then((res) => {
      this.setState({
        userWritesDisabled: !!res.data.org_user_writes_disabled,
        accountsOrgManagementURL: res.data.accounts_org_user_management_url || accountsOrgUserManagementURL,
      });
    }).catch(() => {
      this.setState({
        userWritesDisabled: false,
        accountsOrgManagementURL: accountsOrgUserManagementURL,
      });
    });
  }

  updateName = (name) => {
    this.setState({
      name: name
    });
  };

  updateContactEmail = (contactEmail) => {
    this.setState({
      contact_email: contactEmail
    });
  };

  updateQuota = (quota) => {
    this.setState(quota);
  };

  render() {
    const email = this.props.email;
    const manageInAccountsURL = buildAccountsOrgUserManagementURL(this.state.accountsOrgManagementURL, {
      view: ACCOUNTS_ORG_USER_VIEWS.USER,
      action: ACCOUNTS_ORG_USER_ACTIONS.MANAGE_USER,
      userEmail: email,
    });

    return (
      <Fragment>
        <MainPanelTopbar children={manageInAccountsURL ? (
          <a href={manageInAccountsURL} className="btn btn-secondary operation-item" target="_blank" rel="noopener noreferrer" title={gettext('Manage in Accounts')}>
            <i className="fas fa-external-link-alt text-secondary mr-1"></i>{gettext('Manage in Accounts')}
          </a>
        ) : null} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <OrgAdminUserNav email={this.props.email} currentItem='profile' manageInAccountsURL={manageInAccountsURL} />
            <div className="cur-view-content">
              <Content
                data={this.state}
                canManageIdentity={!this.state.userWritesDisabled}
                accountsManageUserURL={manageInAccountsURL}
                accountsEditNameURL={buildAccountsOrgUserManagementURL(this.state.accountsOrgManagementURL, {
                  view: ACCOUNTS_ORG_USER_VIEWS.USER,
                  action: ACCOUNTS_ORG_USER_ACTIONS.EDIT_NAME,
                  userEmail: email,
                })}
                accountsEditContactEmailURL={buildAccountsOrgUserManagementURL(this.state.accountsOrgManagementURL, {
                  view: ACCOUNTS_ORG_USER_VIEWS.USER,
                  action: ACCOUNTS_ORG_USER_ACTIONS.EDIT_CONTACT_EMAIL,
                  userEmail: email,
                })}
                updateName={this.updateName}
                updateContactEmail={this.updateContactEmail}
                updateQuota={this.updateQuota}
              />
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

class Content extends Component {

  constructor(props) {
    super(props);
    this.state = {
      isSetNameDialogOpen: false,
      isSetContactEmailDialogOpen: false,
      isSetQuotaDialogOpen: false
    };
  }

  toggleSetNameDialog = () => {
    this.setState({
      isSetNameDialogOpen: !this.state.isSetNameDialogOpen
    });
  };

  toggleSetContactEmailDialog = () => {
    this.setState({
      isSetContactEmailDialogOpen: !this.state.isSetContactEmailDialogOpen
    });
  };

  toggleSetQuotaDialog = () => {
    this.setState({
      isSetQuotaDialogOpen: !this.state.isSetQuotaDialogOpen
    });
  };

  render() {
    const {
      loading, errorMsg,
      avatar_url, email, contact_email,
      name, quota_total, quota_usage,
      traffic_upload_quota, traffic_download_quota,
      org_storage_quota, org_traffic_quota, org_traffic_upload_quota, org_traffic_download_quota,
    } = this.props.data;
    const {
      canManageIdentity,
      accountsEditNameURL,
      accountsEditContactEmailURL,
      accountsManageUserURL,
    } = this.props;
    const { isSetNameDialogOpen, isSetContactEmailDialogOpen, isSetQuotaDialogOpen } = this.state;

    // Effective quota: use user's individual quota, fall back to org per-direction, then org combined.
    const effectiveStorageQuota = quota_total > 0 ? quota_total : org_storage_quota;
    const effectiveUploadQuota = traffic_upload_quota > 0 ? traffic_upload_quota : (org_traffic_upload_quota > 0 ? org_traffic_upload_quota : org_traffic_quota);
    const effectiveDownloadQuota = traffic_download_quota > 0 ? traffic_download_quota : (org_traffic_download_quota > 0 ? org_traffic_download_quota : org_traffic_quota);

    const formatQuota = (effective, individual, emptyLabel) => {
      if (effective > 0) {
        const text = Utils.bytesToSize(effective);
        return individual > 0 ? text : `${text} (${gettext('inherited')})`;
      }
      return emptyLabel;
    };

    if (loading) {
      return <Loading />;
    }
    if (errorMsg) {
      return <p className="error text-center">{errorMsg}</p>;
    }

    return (
      <Fragment>
        <dl>
          <dt>{gettext('Avatar')}</dt>
          <dd>
            <img src={avatar_url} width="48" height="48" className="rounded" alt="" />
          </dd>

          <dt>ID</dt>
          <dd>{email}</dd>

          <dt>{gettext('Name')}</dt>
          <dd>
            {name || '--'}
            {canManageIdentity ? (
              <span title={gettext('Edit')} className="attr-action-icon fa fa-pencil-alt" onClick={this.toggleSetNameDialog}></span>
            ) : accountsEditNameURL ? (
              <a href={accountsEditNameURL} target="_blank" rel="noopener noreferrer" title={gettext('Edit in Accounts')} className="attr-action-icon fas fa-external-link-alt"></a>
            ) : null}
          </dd>

          <dt>{gettext('Contact Email')}</dt>
          <dd>
            {contact_email || '--'}
            {canManageIdentity ? (
              <span title={gettext('Edit')} className="attr-action-icon fa fa-pencil-alt" onClick={this.toggleSetContactEmailDialog}></span>
            ) : accountsEditContactEmailURL ? (
              <a href={accountsEditContactEmailURL} target="_blank" rel="noopener noreferrer" title={gettext('Edit in Accounts')} className="attr-action-icon fas fa-external-link-alt"></a>
            ) : null}
          </dd>

          <dt>{gettext('Organization')}</dt>
          <dd>
            {orgName}
            {accountsManageUserURL && (
              <a href={accountsManageUserURL} target="_blank" rel="noopener noreferrer" title={gettext('Manage in Accounts')} className="attr-action-icon fas fa-external-link-alt"></a>
            )}
          </dd>

          <dt>{gettext('Space Used / Quota')}</dt>
          <dd>
            {`${Utils.bytesToSize(quota_usage)}${effectiveStorageQuota > 0 ? ' / ' + Utils.bytesToSize(effectiveStorageQuota) + (quota_total <= 0 ? ` (${gettext('inherited')})` : '') : ''}`}
            <span title={gettext('Edit')} className="attr-action-icon fa fa-pencil-alt" onClick={this.toggleSetQuotaDialog}></span>
          </dd>

          <dt>{gettext('Monthly Upload Quota')}</dt>
          <dd>
            {formatQuota(effectiveUploadQuota, traffic_upload_quota, gettext('No limit'))}
            <span title={gettext('Edit')} className="attr-action-icon fa fa-pencil-alt" onClick={this.toggleSetQuotaDialog}></span>
          </dd>

          <dt>{gettext('Monthly Download Quota')}</dt>
          <dd>
            {formatQuota(effectiveDownloadQuota, traffic_download_quota, gettext('No limit'))}
            <span title={gettext('Edit')} className="attr-action-icon fa fa-pencil-alt" onClick={this.toggleSetQuotaDialog}></span>
          </dd>
        </dl>
        {isSetNameDialogOpen &&
          <SetOrgUserName
            orgID={orgID}
            email={email}
            name={name}
            updateName={this.props.updateName}
            toggleDialog={this.toggleSetNameDialog}
          />
        }
        {isSetContactEmailDialogOpen &&
          <SetOrgUserContactEmail
            orgID={orgID}
            email={email}
            contactEmail={contact_email}
            updateContactEmail={this.props.updateContactEmail}
            toggleDialog={this.toggleSetContactEmailDialog}
          />
        }
        {isSetQuotaDialogOpen &&
          <SetOrgUserQuota
            orgID={orgID}
            email={email}
            quotaTotal={quota_total}
            trafficUploadQuota={traffic_upload_quota}
            trafficDownloadQuota={traffic_download_quota}
            orgStorageQuota={org_storage_quota}
            orgTrafficQuota={org_traffic_quota}
            orgTrafficUploadQuota={org_traffic_upload_quota}
            orgTrafficDownloadQuota={org_traffic_download_quota}
            updateQuota={this.props.updateQuota}
            toggleDialog={this.toggleSetQuotaDialog}
          />
        }
      </Fragment>
    );
  }
}

Content.propTypes = {
  accountsEditContactEmailURL: PropTypes.string,
  accountsEditNameURL: PropTypes.string,
  accountsManageUserURL: PropTypes.string,
  canManageIdentity: PropTypes.bool,
  data: PropTypes.object.isRequired,
  updateName: PropTypes.func.isRequired,
  updateContactEmail: PropTypes.func.isRequired,
  updateQuota: PropTypes.func.isRequired,
};

OrgUserProfile.propTypes = {
  email: PropTypes.string,
};

export default OrgUserProfile;
