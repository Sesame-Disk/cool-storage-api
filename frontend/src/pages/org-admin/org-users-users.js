import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { navigate } from '@gatsbyjs/reach-router';
import Nav from './org-users-nav';
import OrgUsersList from './org-users-list';
import MainPanelTopbar from './main-panel-topbar';
import ModalPortal from '../../components/modal-portal';
// import ImportOrgUsersDialog from '../../components/dialog/org-import-users-dialog';
import AddOrgUserDialog from '../../components/dialog/org-add-user-dialog';
import InviteUserDialog from '../../components/dialog/org-admin-invite-user-dialog';
import InviteUserViaWeiXinDialog from '../../components/dialog/org-admin-invite-user-via-weixin-dialog';
import TransferOrgOwnershipDialog from '../../components/dialog/transfer-org-ownership-dialog';
import toaster from '../../components/toast';
import { seafileAPI } from '../../utils/seafile-api';
import OrgUserInfo from '../../models/org-user';
import { billingUrl, gettext, invitationLink, isOrgOwner, orgID, siteRoot, orgEnableAdminInviteUser, username } from '../../utils/constants';
import { getUpgradeState } from '../../utils/upgrade-state';
import { Utils } from '../../utils/utils';

class Search extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      value: ''
    };
  }

  handleInputChange = (e) => {
    this.setState({
      value: e.target.value
    });
  };

  handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      this.handleSubmit();
    }
  };

  handleSubmit = () => {
    const value = this.state.value.trim();
    if (!value) {
      return false;
    }
    this.props.submit(value);
  };

  render() {
    return (
      <div className="input-icon">
        <i className="d-flex input-icon-addon fas fa-search"></i>
        <input
          type="text"
          className="form-control search-input h-6 mr-1"
          style={{ width: '15rem' }}
          placeholder={this.props.placeholder}
          value={this.state.value}
          onChange={this.handleInputChange}
          onKeyDown={this.handleKeyDown}
          autoComplete="off"
        />
      </div>
    );
  }
}

Search.propTypes = {
  placeholder: PropTypes.string.isRequired,
  submit: PropTypes.func.isRequired,
};

class OrgUsers extends Component {

  constructor(props) {
    super(props);
    this.state = {
      orgUsers: [],
      page: 1,
      pageNext: false,
      statusFilter: 'all',
      sortBy: '',
      sortOrder: 'desc',
      isShowAddOrgUserDialog: false,
      isImportOrgUsersDialogOpen: false,
      isInviteUserDialogOpen: false,
      isInviteUserViaWeiXinDialogOpen: false,
      isTransferOwnershipDialogOpen: false,
      canTransferOwnership: isOrgOwner,
      orgPlan: '',
      maxUsers: 0,
      currentUsers: 0,
      limitsLoaded: false,
    };
  }

  componentDidMount() {
    let urlParams = (new URL(window.location)).searchParams;
    const { page, sortBy, sortOrder, statusFilter } = this.state;
    this.setState({
      /*
        perPage: parseInt(urlParams.get('per_page') || perPage),
        currentPage: parseInt(urlParams.get('page') || currentPage),
        */
      page: parseInt(urlParams.get('page') || page),
      sortBy: urlParams.get('order_by') || sortBy,
      sortOrder: urlParams.get('direction') || sortOrder,
      statusFilter: urlParams.get('status') || statusFilter
    }, () => {
      this.refreshOrgLimits();
      this.initOrgUsersData(this.state.page);
    });
  }

  refreshOrgLimits = () => {
    seafileAPI.orgAdminGetOrgInfo().then((res) => {
      this.setState({
        orgPlan: res.data.plan || '',
        maxUsers: Number(res.data.max_users) || 0,
        currentUsers: Number(res.data.member_usage) || 0,
        limitsLoaded: true,
      });
    }).catch(() => {
      this.setState({ limitsLoaded: true });
    });
  };

  sortByQuotaUsage = () => {
    this.setState({
      sortBy: 'quota_usage',
      sortOrder: this.state.sortOrder === 'asc' ? 'desc' : 'asc',
      page: 1
    }, () => {
      let url = new URL(location.href);
      let searchParams = new URLSearchParams(url.search);
      const { page, sortBy, sortOrder } = this.state;
      searchParams.set('page', page);
      searchParams.set('order_by', sortBy);
      searchParams.set('direction', sortOrder);
      url.search = searchParams.toString();
      navigate(url.toString());
      this.initOrgUsersData(page);
    });
  };

  toggleImportOrgUsersDialog = () => {
    this.setState({ isImportOrgUsersDialogOpen: !this.state.isImportOrgUsersDialogOpen });
  };

  toggleAddOrgUser = () => {
    this.setState({ isShowAddOrgUserDialog: !this.state.isShowAddOrgUserDialog });
  };

  toggleInviteUserDialog = () => {
    this.setState({ isInviteUserDialogOpen: !this.state.isInviteUserDialogOpen });
  };

  toggleInviteUserViaWeiXinDialog = () => {
    this.setState({ isInviteUserViaWeiXinDialogOpen: !this.state.isInviteUserViaWeiXinDialogOpen });
  };

  toggleTransferOwnershipDialog = () => {
    this.setState({ isTransferOwnershipDialogOpen: !this.state.isTransferOwnershipDialogOpen });
  };

  initOrgUsersData = (page) => {
    const { sortBy, sortOrder, statusFilter } = this.state;
    seafileAPI.orgAdminListOrgUsers(orgID, '', page, sortBy, sortOrder, statusFilter).then(res => {
      let userList = res.data.user_list.map(item => {
        return new OrgUserInfo(item);
      });
      this.setState({
        orgUsers: userList,
        pageNext: res.data.page_next,
        page: res.data.page,
      });
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  setStatusFilter = (statusFilter) => {
    this.setState({
      statusFilter,
      page: 1
    }, () => {
      let url = new URL(location.href);
      let searchParams = new URLSearchParams(url.search);
      searchParams.set('page', '1');
      searchParams.set('status', statusFilter);
      url.search = searchParams.toString();
      navigate(url.toString());
      this.initOrgUsersData(1);
    });
  };

  importOrgUsers = (file) => {
    toaster.notify(gettext('It may take some time, please wait.'));
    seafileAPI.orgAdminImportUsersViaFile(orgID, file).then((res) => {
      if (res.data.success.length) {
        const users = res.data.success.map(item => {
          if (item.institution === undefined) {
            item.institution = '';
          }
          return new OrgUserInfo(item);
        });
        this.setState({
          orgUsers: users.concat(this.state.orgUsers)
        });
        this.refreshOrgLimits();
      }
      res.data.failed.forEach(item => {
        const msg = `${item.email}: ${item.error_msg}`;
        toaster.danger(msg);
      });
    }).catch((error) => {
      let errMsg = Utils.getErrorMsg(error);
      toaster.danger(errMsg);
    });
  };

  addOrgUser = (email, name, password) => {
    seafileAPI.orgAdminAddOrgUser(orgID, email, name, password).then(res => {
      let userInfo = new OrgUserInfo(res.data);
      this.state.orgUsers.unshift(userInfo);
      this.setState({
        orgUsers: this.state.orgUsers
      });
      this.refreshOrgLimits();
      this.toggleAddOrgUser();
      let msg = gettext('successfully added user %s.');
      msg = msg.replace('%s', email);
      toaster.success(msg);
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
      this.toggleAddOrgUser();
    });
  };

  toggleOrgUsersDelete = (email, username) => {
    seafileAPI.orgAdminDeleteOrgUser(orgID, email).then(res => {
      const targetPage = this.state.orgUsers.length === 1 && this.state.page > 1 ? this.state.page - 1 : this.state.page;
      this.initOrgUsersData(targetPage);
      this.refreshOrgLimits();
      let msg = gettext('Deleted user %s');
      msg = msg.replace('%s', username);
      toaster.success(msg);
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  inviteOrgUser = (emails) => {
    seafileAPI.orgAdminInviteOrgUsers(orgID, emails.split(',')).then(res => {
      this.toggleInviteUserDialog();
      let users = res.data.success.map(user => {
        return new OrgUserInfo(user);
      });
      this.setState({
        orgUsers: users.concat(this.state.orgUsers)
      });
      this.refreshOrgLimits();

      res.data.success.forEach(item => {
        let msg = gettext('successfully sent email to %s.');
        msg = msg.replace('%s', item.email);
        toaster.success(msg);
      });

      res.data.failed.forEach(item => {
        const msg = `${item.email}: ${item.error_msg}`;
        toaster.danger(msg);
      });
    }).catch(error => {
      this.toggleInviteUserDialog();
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  changeStatus = (email, isActive) => {
    seafileAPI.orgAdminChangeOrgUserStatus(orgID, email, isActive).then(res => {
      let users = this.state.orgUsers.map(item => {
        if (item.email === email) {
          item['is_active'] = res.data['is_active'];
          item['status'] = res.data['status'];
        }
        return item;
      });
      this.setState({ orgUsers: users });
      toaster.success(gettext('Edit succeeded.'));
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  restoreUser = (email, username) => {
    seafileAPI.orgAdminRestoreOrgUser(orgID, email).then(() => {
      this.initOrgUsersData(this.state.page);
      this.refreshOrgLimits();
      let msg = gettext('Restored user %s');
      msg = msg.replace('%s', username);
      toaster.success(msg);
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  searchItems = (keyword) => {
    navigate(`${siteRoot}org/useradmin/search-users/?query=${encodeURIComponent(keyword)}`);
  };

  searchOrgAdmins = (query) => {
    return seafileAPI.orgAdminSearchUser(orgID, query, 1, 25, 'active').then((res) => {
      const users = (res.data.user_list || []).filter((user) => user.is_org_staff);
      return { data: { users } };
    });
  };

  transferOwnership = (newOwnerEmail) => {
    seafileAPI.orgAdminTransferOwnership(orgID, newOwnerEmail).then(() => {
      this.setState({
        isTransferOwnershipDialogOpen: false,
        canTransferOwnership: false,
      });
      this.initOrgUsersData(this.state.page);
      toaster.success(gettext('Organization ownership transferred successfully.'));
    }).catch((error) => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  getSearch = () => {
    return <Search
      placeholder={gettext('Search users')}
      submit={this.searchItems}
    />;
  };

  render() {
    const topBtn = 'btn btn-secondary operation-item';
    const { isFeatureLockedOwner } = getUpgradeState();
    const { canTransferOwnership, isTransferOwnershipDialogOpen, currentUsers, limitsLoaded, maxUsers, orgPlan } = this.state;
    const hasUserAvailability = maxUsers <= 0 || currentUsers < maxUsers;
    const canAddUsers = limitsLoaded && !isFeatureLockedOwner && hasUserAvailability;

    let topbarChildren;
    topbarChildren = (
      <Fragment>
        {/* <button className="btn btn-secondary operation-item" onClick={this.toggleImportOrgUsersDialog}>{gettext('Import users')}</button> */}

        {canTransferOwnership && (
          <button className={topBtn} onClick={this.toggleTransferOwnershipDialog}>
            {gettext('Transfer ownership')}
          </button>
        )}

        {isFeatureLockedOwner ? (
          <div className="d-flex align-items-center">
            <span className="mr-3" style={{ color: '#666' }}>{gettext('Upgrade your plan to unlock additional seats and member management.')}</span>
            <a href={billingUrl} className="btn btn-primary" target="_blank" rel="noopener noreferrer">{gettext('Upgrade Plan')}</a>
          </div>
        ) : limitsLoaded && !hasUserAvailability ? (
          <div className="d-flex align-items-center">
            <span className="mr-3" style={{ color: '#666' }}>
              {gettext('You have reached the member limit (%(used)s/%(total)s) for the %(plan)s plan. Update billing to add more users.')
                .replace('%(used)s', currentUsers)
                .replace('%(total)s', maxUsers)
                .replace('%(plan)s', orgPlan || gettext('current'))}
            </span>
            <a href={billingUrl} className="btn btn-outline-primary" target="_blank" rel="noopener noreferrer">{gettext('Manage Billing')}</a>
          </div>
        ) : !limitsLoaded ? null : (
          <Fragment>
            <button className={topBtn} title={gettext('Add user')} onClick={this.toggleAddOrgUser}>
              <i className="fas fa-plus-square text-secondary mr-1"></i>{gettext('Add user')}</button>
            {orgEnableAdminInviteUser &&
              <button className={topBtn} title={gettext('Invite users')} onClick={this.toggleInviteUserDialog}>
                <i className="fas fa-plus-square text-secondary mr-1"></i>{gettext('Invite users')}</button>
            }
            {invitationLink &&
              <button className={topBtn} title={'通过微信邀请用户'} onClick={this.toggleInviteUserViaWeiXinDialog}>
                <i className="fas fa-plus-square text-secondary mr-1"></i>{'通过微信邀请用户'}</button>
            }
          </Fragment>
        )}

        {/* Dialogs - only render if not free user */}
        {/* {this.state.isImportOrgUsersDialogOpen &&
        <ModalPortal>
          <ImportOrgUsersDialog importUsersInBatch={this.importOrgUsers} toggle={this.toggleImportOrgUsersDialog}/>
        </ModalPortal>
        } */}
        {canAddUsers && this.state.isShowAddOrgUserDialog &&
          <ModalPortal>
            <AddOrgUserDialog handleSubmit={this.addOrgUser} toggle={this.toggleAddOrgUser} />
          </ModalPortal>
        }
        {canAddUsers && this.state.isInviteUserDialogOpen &&
          <ModalPortal>
            <InviteUserDialog handleSubmit={this.inviteOrgUser} toggle={this.toggleInviteUserDialog} />
          </ModalPortal>
        }
        {canAddUsers && this.state.isInviteUserViaWeiXinDialogOpen &&
          <ModalPortal>
            <InviteUserViaWeiXinDialog invitationLink={invitationLink} toggle={this.toggleInviteUserViaWeiXinDialog} />
          </ModalPortal>
        }
        {isTransferOwnershipDialogOpen &&
          <TransferOrgOwnershipDialog
            currentOwner={username}
            searchFunc={this.searchOrgAdmins}
            onSubmit={this.transferOwnership}
            toggleDialog={this.toggleTransferOwnershipDialog}
          />
        }
      </Fragment>
    );

    return (
      <Fragment>
        <MainPanelTopbar children={topbarChildren} search={this.getSearch()} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <Nav currentItem="all" />
            <div className="d-flex align-items-center mb-3">
              <span className="mr-2">{gettext('Status')}</span>
              {['all', 'active', 'deactivated', 'deleted'].map(status => {
                const isStatusSelected = this.state.statusFilter === status;
                const labelMap = {
                  all: gettext('All'),
                  active: gettext('Active'),
                  deactivated: gettext('Inactive'),
                  deleted: gettext('Deleted')
                };
                return (
                  <button
                    key={status}
                    className={`btn btn-sm mr-2 ${isStatusSelected ? 'btn-primary' : 'btn-outline-secondary'}`}
                    onClick={() => this.setStatusFilter(status)}
                  >
                    {labelMap[status]}
                  </button>
                );
              })}
            </div>
            <OrgUsersList
              initOrgUsersData={this.initOrgUsersData}
              toggleDelete={this.toggleOrgUsersDelete}
              restoreUser={this.restoreUser}
              changeStatus={this.changeStatus}
              orgUsers={this.state.orgUsers}
              page={this.state.page}
              pageNext={this.state.pageNext}
              sortBy={this.state.sortBy}
              sortOrder={this.state.sortOrder}
              sortByQuotaUsage={this.sortByQuotaUsage}
            />
          </div>
        </div>
      </Fragment>
    );
  }
}

export default OrgUsers;
