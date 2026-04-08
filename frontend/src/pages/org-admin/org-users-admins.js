import React, { Component, Fragment } from 'react';
import Nav from './org-users-nav';
import OrgAdminList from './org-admin-list';
import MainPanelTopbar from './main-panel-topbar';
import AddOrgAdminDialog from '../../components/dialog/org-add-admin-dialog';
import ModalPortal from '../../components/modal-portal';
import toaster from '../../components/toast';
import { seafileAPI } from '../../utils/seafile-api';
import OrgUserInfo from '../../models/org-user';
import { accountsOrgUserManagementURL, gettext, orgID } from '../../utils/constants';
import { ACCOUNTS_ORG_USER_ACTIONS, ACCOUNTS_ORG_USER_VIEWS, buildAccountsOrgUserManagementURL } from '../../utils/accounts-org-user-management';
import { Utils } from '../../utils/utils';

class OrgUsers extends Component {

  constructor(props) {
    super(props);
    this.state = {
      orgAdminUsers: [],
      isShowAddOrgAdminDialog: false,
      userWritesDisabled: false,
      accountsOrgManagementURL: accountsOrgUserManagementURL,
    };
  }

  componentDidMount() {
    seafileAPI.orgAdminGetOrgInfo().then((res) => {
      this.setState({
        userWritesDisabled: !!res.data.org_user_writes_disabled,
        accountsOrgManagementURL: res.data.accounts_org_user_management_url || accountsOrgUserManagementURL,
      });
    }).catch(() => {
      this.setState({ userWritesDisabled: false });
    });
  }

  toggleAddOrgAdmin = () => {
    this.setState({ isShowAddOrgAdminDialog: !this.state.isShowAddOrgAdminDialog });
  };

  initOrgAdmin = () => {
    seafileAPI.orgAdminListOrgUsers(orgID, true).then(res => {
      let userList = res.data.user_list.map(item => {
        return new OrgUserInfo(item);
      });
      this.setState({ orgAdminUsers: userList });
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  toggleOrgAdminDelete = (email, username) => {
    seafileAPI.orgAdminDeleteOrgUser(orgID, email).then(res => {
      this.setState({
        orgAdminUsers: this.state.orgAdminUsers.filter(item => item.email !== email)
      });
      let msg = gettext('Deleted user %s');
      msg = msg.replace('%s', username);
      toaster.success(msg);
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  toggleRevokeAdmin = (email) => {
    seafileAPI.orgAdminSetOrgAdmin(orgID, email, false).then(res => {
      this.setState({
        orgAdminUsers: this.state.orgAdminUsers.filter(item => item.email !== email)
      });
      let msg = gettext('Successfully revoke the admin permission of %s');
      msg = msg.replace('%s', res.data.name);
      toaster.success(msg);
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  onAddedOrgAdmin = (userInfo) => {
    this.state.orgAdminUsers.unshift(userInfo);
    this.setState({
      orgAdminUsers: this.state.orgAdminUsers
    });
    let msg = gettext('Successfully set %s as admin.');
    msg = msg.replace('%s', userInfo.email);
    toaster.success(msg);
    this.toggleAddOrgAdmin();
  };

  changeStatus = (email, isActive) => {
    seafileAPI.orgAdminChangeOrgUserStatus(orgID, email, isActive).then(res => {
      let users = this.state.orgAdminUsers.map(item => {
        if (item.email === email) {
          item['is_active'] = res.data['is_active'];
        }
        return item;
      });
      this.setState({ orgAdminUsers: users });
      toaster.success(gettext('Edit succeeded.'));
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  render() {
    const topBtn = 'btn btn-secondary operation-item';
    const addAdminURL = buildAccountsOrgUserManagementURL(this.state.accountsOrgManagementURL, {
      view: ACCOUNTS_ORG_USER_VIEWS.ADMINS,
      action: ACCOUNTS_ORG_USER_ACTIONS.ADD_ADMIN,
    });
    let topbarChildren;
    topbarChildren = (
      <Fragment>
        {this.state.userWritesDisabled ? (
          addAdminURL && (
            <a href={addAdminURL} className={topBtn} target="_blank" rel="noopener noreferrer" title={gettext('Add admin in Accounts')}>
              <i className="fas fa-external-link-alt text-secondary mr-1"></i>{gettext('Add admin')}
            </a>
          )
        ) : (
          <button className={topBtn} title={gettext('Add admin')} onClick={this.toggleAddOrgAdmin}>
            <i className="fas fa-plus-square text-secondary mr-1"></i>{gettext('Add admin')}
          </button>
        )}
        {!this.state.userWritesDisabled && this.state.isShowAddOrgAdminDialog &&
          <ModalPortal>
            <AddOrgAdminDialog toggle={this.toggleAddOrgAdmin} onAddedOrgAdmin={this.onAddedOrgAdmin} />
          </ModalPortal>
        }
      </Fragment>
    );

    return (
      <Fragment>
        <MainPanelTopbar children={topbarChildren} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <Nav currentItem="admins" />
            <OrgAdminList
              canManageUsers={!this.state.userWritesDisabled}
              accountsOrgManagementURL={this.state.accountsOrgManagementURL}
              currentTab="admins"
              toggleDelete={this.toggleOrgAdminDelete}
              toggleRevokeAdmin={this.toggleRevokeAdmin}
              orgAdminUsers={this.state.orgAdminUsers}
              initOrgAdmin={this.initOrgAdmin}
              changeStatus={this.changeStatus}
            />
          </div>
        </div>
      </Fragment>
    );
  }
}

export default OrgUsers;
