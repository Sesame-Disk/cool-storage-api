import React, { Component, Fragment } from 'react';
import { navigate } from '@gatsbyjs/reach-router';
import PropTypes from 'prop-types';
import { Button } from 'reactstrap';
import moment from 'moment';
import { Utils } from '../../../utils/utils';
import { seafileAPI } from '../../../utils/seafile-api';
import { billingUrl, gettext, username } from '../../../utils/constants';
import toaster from '../../../components/toast';
import EmptyTip from '../../../components/empty-tip';
import Loading from '../../../components/loading';
import Selector from '../../../components/single-selector';
import SysAdminAddUserDialog from '../../../components/dialog/sysadmin-dialog/sysadmin-add-user-dialog';
import CommonOperationConfirmationDialog from '../../../components/dialog/common-operation-confirmation-dialog';
import OpMenu from '../../../components/dialog/op-menu';
import MainPanelTopbar from '../main-panel-topbar';
import UserLink from '../user-link';
import OrgNav from './org-nav';

class Content extends Component {

  constructor(props) {
    super(props);
    this.state = {
      isItemFreezed: false
    };
  }

  toggleItemFreezed = (isFreezed) => {
    this.setState({ isItemFreezed: isFreezed });
  };

  onFreezedItem = () => {
    this.setState({ isItemFreezed: true });
  };

  onUnfreezedItem = () => {
    this.setState({ isItemFreezed: false });
  };

  render() {
    const { loading, errorMsg, items } = this.props;
    if (loading) {
      return <Loading />;
    } else if (errorMsg) {
      return <p className="error text-center mt-4">{errorMsg}</p>;
    } else {
      const emptyTip = (
        <EmptyTip>
          <h2>{gettext('No members')}</h2>
        </EmptyTip>
      );
      const table = (
        <Fragment>
          <table>
            <thead>
              <tr>
                <th width="25%">{gettext('Name')}</th>
                <th width="15%">{gettext('Status')}</th>
                <th width="15%">{gettext('Role')}</th>
                <th width="15%">{gettext('Space Used')}</th>
                <th width="25%">{gettext('Created At')}{' / '}{gettext('Last Login')}</th>
                <th width="5%">{/* Operations */}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item, index) => {
                return (<Item
                  key={index}
                  item={item}
                  isItemFreezed={this.state.isItemFreezed}
                  onFreezedItem={this.onFreezedItem}
                  onUnfreezedItem={this.onUnfreezedItem}
                  toggleItemFreezed={this.toggleItemFreezed}
                  updateStatus={this.props.updateStatus}
                  updateMembership={this.props.updateMembership}
                  deleteUser={this.props.deleteUser}
                  restoreUser={this.props.restoreUser}
                />);
              })}
            </tbody>
          </table>
        </Fragment>
      );
      return items.length ? table : emptyTip;
    }
  }
}

Content.propTypes = {
  loading: PropTypes.bool.isRequired,
  errorMsg: PropTypes.string.isRequired,
  items: PropTypes.array.isRequired,
  updateStatus: PropTypes.func.isRequired,
  updateMembership: PropTypes.func.isRequired,
  deleteUser: PropTypes.func.isRequired,
  restoreUser: PropTypes.func.isRequired,
};
class Item extends Component {

  constructor(props) {
    super(props);
    this.state = {
      isOpIconShown: false,
      highlight: false,
      isDeleteDialogOpen: false,
      isResetPasswordDialogOpen: false,
      isRestoreDialogOpen: false,
      isConfirmInactiveDialogOpen: false,
      isConfirmTransferOwnershipDialogOpen: false
    };
  }

  handleMouseEnter = () => {
    if (!this.props.isItemFreezed) {
      this.setState({
        isOpIconShown: true,
        highlight: true
      });
    }
  };

  handleMouseLeave = () => {
    if (!this.props.isItemFreezed) {
      this.setState({
        isOpIconShown: false,
        highlight: false
      });
    }
  };

  onUnfreezedItem = () => {
    this.setState({
      highlight: false,
      isOpIconShow: false
    });
    this.props.onUnfreezedItem();
  };

  onMenuItemClick = (operation) => {
    switch (operation) {
      case 'Delete':
        this.toggleDeleteDialog();
        break;
      case 'Reset Password':
        this.toggleResetPasswordDialog();
        break;
      case 'Restore':
        this.toggleRestoreDialog();
        break;
      default:
        break;
    }
  };

  toggleDeleteDialog = (e) => {
    if (e) {
      e.preventDefault();
    }
    this.setState({ isDeleteDialogOpen: !this.state.isDeleteDialogOpen });
  };

  toggleResetPasswordDialog = (e) => {
    if (e) {
      e.preventDefault();
    }
    this.setState({ isResetPasswordDialogOpen: !this.state.isResetPasswordDialogOpen });
  };

  toggleRestoreDialog = (e) => {
    if (e) {
      e.preventDefault();
    }
    this.setState({ isRestoreDialogOpen: !this.state.isRestoreDialogOpen });
  };

  toggleConfirmInactiveDialog = () => {
    this.setState({ isConfirmInactiveDialogOpen: !this.state.isConfirmInactiveDialogOpen });
  };

  toggleConfirmTransferOwnershipDialog = () => {
    this.setState({ isConfirmTransferOwnershipDialogOpen: !this.state.isConfirmTransferOwnershipDialogOpen });
  };

  updateStatus = (statusOption) => {
    this.props.updateStatus(this.props.item.email, statusOption.value);
  };

  setUserInactive = () => {
    this.props.updateStatus(this.props.item.email, 'inactive');
  };

  updateMembership = (membershipOption) => {
    if (membershipOption.value === 'Owner') {
      this.toggleConfirmTransferOwnershipDialog();
      return;
    }
    this.props.updateMembership(this.props.item.email, membershipOption.value);
  };

  confirmTransferOwnership = () => {
    this.props.updateMembership(this.props.item.email, 'Owner');
    this.toggleConfirmTransferOwnershipDialog();
  };

  deleteUser = () => {
    const { item } = this.props;
    this.props.deleteUser(item.org_id, item.email);
  };

  restoreUser = () => {
    this.props.restoreUser(this.props.item.email);
  };

  resetPassword = () => {
    seafileAPI.sysAdminResetUserPassword(this.props.item.email).then(res => {
      toaster.success(res.data.reset_tip);
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  translateOperations = (item) => {
    let translateResult = '';
    switch (item) {
      case 'Delete':
        translateResult = gettext('Delete');
        break;
      case 'Reset Password':
        translateResult = gettext('Reset Password');
        break;
      case 'Restore':
        translateResult = gettext('Restore');
        break;
    }

    return translateResult;
  };

  translateStatus = (status) => {
    switch (status) {
      case 'active':
        return gettext('Active');
      case 'inactive':
        return gettext('Inactive');
      case 'deleted':
        return gettext('Deleted');
    }
  };

  translateMembership = (membership) => {
    switch (membership) {
      case 'Owner':
        return gettext('Owner');
      case 'Admin':
        return gettext('Admin');
      case 'Member':
        return gettext('Member');
    }
  };

  render() {
    const { item } = this.props;
    const { highlight, isOpIconShown, isDeleteDialogOpen, isResetPasswordDialogOpen, isRestoreDialogOpen, isConfirmInactiveDialogOpen, isConfirmTransferOwnershipDialogOpen } = this.state;

    const itemName = '<span class="op-target">' + Utils.HTMLescape(item.name) + '</span>';
    let deleteDialogMsg = gettext('Are you sure you want to delete {placeholder} ?').replace('{placeholder}', itemName);
    let resetPasswordDialogMsg = gettext('Are you sure you want to reset the password of {placeholder} ?').replace('{placeholder}', itemName);
    const confirmSetUserInactiveMsg = gettext('Are you sure you want to set {user_placeholder} inactive?').replace('{user_placeholder}', itemName);
    const restoreDialogMsg = gettext('Are you sure you want to restore {placeholder} ?').replace('{placeholder}', itemName);
    const confirmTransferOwnershipMsg = gettext('Transfer organization ownership to {user_placeholder}? The current owner will be downgraded to admin.').replace('{user_placeholder}', itemName);

    const effectiveStatus = item.status || (item.active ? 'active' : 'inactive');
    const isDeleted = effectiveStatus === 'deleted';

    // for 'user status'
    const curStatus = effectiveStatus === 'active' ? 'active' : 'inactive';
    this.statusOptions = ['active', 'inactive'].map(item => {
      return {
        value: item,
        text: this.translateStatus(item),
        isSelected: item === curStatus
      };
    });
    const currentSelectedStatusOption = this.statusOptions.filter(item => item.isSelected)[0];

    // for 'user membership'
    let curMembership = 'Member';
    if (item.role === 'owner') {
      curMembership = 'Owner';
    } else if (item.role === 'admin' || item.is_org_staff) {
      curMembership = 'Admin';
    }
    this.membershipOptions = ['Member', 'Admin', 'Owner'].map(item => {
      return {
        value: item,
        text: this.translateMembership(item),
        isSelected: item === curMembership
      };
    });
    const currentSelectedMembershipOption = this.membershipOptions.filter(item => item.isSelected)[0];

    return (
      <Fragment>
        <tr className={this.state.highlight ? 'tr-highlight' : ''} onMouseEnter={this.handleMouseEnter} onMouseLeave={this.handleMouseLeave}>
          <td><UserLink email={item.email} name={item.name} /></td>
          <td>
            {isDeleted ?
              <span className="badge badge-danger">{gettext('Deleted')}</span>
              :
              <Selector
                isDropdownToggleShown={highlight}
                currentSelectedOption={currentSelectedStatusOption}
                options={this.statusOptions}
                selectOption={this.updateStatus}
                toggleItemFreezed={this.props.toggleItemFreezed}
                operationBeforeSelect={effectiveStatus === 'active' ? this.toggleConfirmInactiveDialog : undefined}
              />
            }
          </td>
          <td>
            {isDeleted ?
              <span className="text-secondary">--</span>
              : item.role === 'owner' ?
                <span>{gettext('Owner')}</span>
                :
                <Selector
                  isDropdownToggleShown={highlight}
                  currentSelectedOption={currentSelectedMembershipOption}
                  options={this.membershipOptions}
                  selectOption={this.updateMembership}
                  toggleItemFreezed={this.props.toggleItemFreezed}
                />
            }
          </td>
          <td>{`${Utils.bytesToSize(item.quota_usage)} / ${item.quota_total > 0 ? Utils.bytesToSize(item.quota_total) : '--'}`}</td>
          <td>
            {moment(item.create_time).format('YYYY-MM-DD HH:mm:ss')}{' / '}{item.last_login ? moment(item.last_login).fromNow() : '--'}
          </td>
          <td>
            {(isOpIconShown && item.email !== username) &&
              <OpMenu
                operations={isDeleted ? ['Restore'] : ['Delete', 'Reset Password']}
                translateOperations={this.translateOperations}
                onMenuItemClick={this.onMenuItemClick}
                onFreezedItem={this.props.onFreezedItem}
                onUnfreezedItem={this.onUnfreezedItem}
              />
            }
          </td>
        </tr>
        {isDeleteDialogOpen &&
          <CommonOperationConfirmationDialog
            title={gettext('Delete Member')}
            message={deleteDialogMsg}
            executeOperation={this.deleteUser}
            confirmBtnText={gettext('Delete')}
            toggleDialog={this.toggleDeleteDialog}
          />
        }
        {isResetPasswordDialogOpen &&
          <CommonOperationConfirmationDialog
            title={gettext('Reset Password')}
            message={resetPasswordDialogMsg}
            executeOperation={this.resetPassword}
            confirmBtnText={gettext('Reset')}
            toggleDialog={this.toggleResetPasswordDialog}
          />
        }
        {isConfirmInactiveDialogOpen &&
          <CommonOperationConfirmationDialog
            title={gettext('Set user inactive')}
            message={confirmSetUserInactiveMsg}
            executeOperation={this.setUserInactive}
            confirmBtnText={gettext('Set')}
            toggleDialog={this.toggleConfirmInactiveDialog}
          />
        }
        {isRestoreDialogOpen &&
          <CommonOperationConfirmationDialog
            title={gettext('Restore Member')}
            message={restoreDialogMsg}
            executeOperation={this.restoreUser}
            confirmBtnText={gettext('Restore')}
            toggleDialog={this.toggleRestoreDialog}
          />
        }
        {isConfirmTransferOwnershipDialogOpen &&
          <CommonOperationConfirmationDialog
            title={gettext('Transfer ownership')}
            message={confirmTransferOwnershipMsg}
            executeOperation={this.confirmTransferOwnership}
            confirmBtnText={gettext('Transfer')}
            toggleDialog={this.toggleConfirmTransferOwnershipDialog}
          />
        }
      </Fragment>
    );
  }
}

Item.propTypes = {
  item: PropTypes.object.isRequired,
  isItemFreezed: PropTypes.bool.isRequired,
  onFreezedItem: PropTypes.func.isRequired,
  onUnfreezedItem: PropTypes.func.isRequired,
  toggleItemFreezed: PropTypes.func.isRequired,
  updateStatus: PropTypes.func.isRequired,
  updateMembership: PropTypes.func.isRequired,
  deleteUser: PropTypes.func.isRequired,
};

class OrgUsers extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      orgName: '',
      orgPlan: '',
      maxUsers: 0,
      currentUsers: 0,
      userList: [],
      statusFilter: 'all',
      isAddUserDialogOpen: false
    };
  }

  componentDidMount() {
    const urlParams = (new URL(window.location)).searchParams;
    const statusFilter = urlParams.get('status') || this.state.statusFilter;
    this.setState({ statusFilter });

    this.refreshOrgInfo();

    this.getUsers(statusFilter);
  }

  refreshOrgInfo = () => {
    seafileAPI.sysAdminGetOrg(this.props.orgID).then((res) => {
      this.setState({
        orgName: res.data.org_name,
        orgPlan: res.data.plan || '',
        maxUsers: Number(res.data.max_users || res.data.max_user_number) || 0,
        currentUsers: Number(res.data.users_count) || 0,
      });
    });
  };

  getUsers = (statusFilter = this.state.statusFilter) => {
    seafileAPI.sysAdminListOrgUsers(this.props.orgID, statusFilter).then((res) => {
      this.setState({
        loading: false,
        errorMsg: '',
        statusFilter,
        userList: res.data.users
      });
    }).catch((error) => {
      this.setState({
        loading: false,
        errorMsg: Utils.getErrorMsg(error, true) // true: show login tip if 403
      });
    });
  };

  setStatusFilter = (statusFilter) => {
    const url = new URL(location.href);
    const searchParams = new URLSearchParams(url.search);
    searchParams.set('status', statusFilter);
    url.search = searchParams.toString();
    navigate(url.toString());
    this.setState({ loading: true }, () => {
      this.getUsers(statusFilter);
    });
  };

  toggleAddUserDialog = () => {
    this.setState({ isAddUserDialogOpen: !this.state.isAddUserDialogOpen });
  };

  addUser = (newUserInfo) => {
    const { email, name, password } = newUserInfo;
    return seafileAPI.sysAdminAddOrgUser(this.props.orgID, email, name, password).then(res => {
      let userList = this.state.userList;
      userList.unshift(res.data);
      this.setState({ userList: userList });
      this.refreshOrgInfo();
    });
  };

  deleteUser = (orgID, email) => {
    seafileAPI.sysAdminDeleteOrgUser(orgID, email).then(res => {
      this.getUsers(this.state.statusFilter);
      this.refreshOrgInfo();
      toaster.success(gettext('Successfully deleted 1 item.'));
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  restoreUser = (email) => {
    seafileAPI.sysAdminRestoreUser(email).then(() => {
      this.getUsers(this.state.statusFilter);
      this.refreshOrgInfo();
      toaster.success(gettext('Edit succeeded'));
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  updateStatus = (email, statusValue) => {
    const isActive = statusValue === 'active';
    seafileAPI.sysAdminUpdateOrgUser(this.props.orgID, email, 'active', isActive).then(res => {
      let newUserList = this.state.userList.map(item => {
        if (item.email === email) {
          item.status = res.data.status;
          item.active = res.data.active;
        }
        return item;
      });
      this.setState({ userList: newUserList });
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  updateMembership = (email, membershipValue) => {
    const role = membershipValue === 'Owner'
      ? 'owner'
      : membershipValue === 'Admin'
        ? 'admin'
        : 'user';
    seafileAPI.sysAdminUpdateOrgUser(this.props.orgID, email, 'role', role).then(res => {
      if (membershipValue === 'Owner') {
        this.getUsers(this.state.statusFilter);
        toaster.success(gettext('Organization ownership transferred successfully.'));
        return;
      }
      let newUserList = this.state.userList.map(item => {
        if (item.email === email) {
          item.status = res.data.status;
          item.is_org_staff = res.data.is_org_staff;
          item.role = res.data.role;
        }
        return item;
      });
      this.setState({ userList: newUserList });
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  render() {
    const { isAddUserDialogOpen, orgName, orgPlan, maxUsers, currentUsers } = this.state;
    const canAddUsers = maxUsers <= 0 || currentUsers < maxUsers;
    return (
      <Fragment>
        <MainPanelTopbar {...this.props}>
          {canAddUsers ? (
            <Button className="btn btn-secondary operation-item" onClick={this.toggleAddUserDialog}>{gettext('Add Member')}</Button>
          ) : (
            <div className="d-flex align-items-center">
              <span className="mr-3 text-secondary">
                {gettext('This organization has reached its member limit (%(used)s/%(total)s) for the %(plan)s plan.')
                  .replace('%(used)s', currentUsers)
                  .replace('%(total)s', maxUsers)
                  .replace('%(plan)s', orgPlan || gettext('current'))}
              </span>
              <a href={billingUrl} className="btn btn-outline-primary" target="_blank" rel="noopener noreferrer">{gettext('Manage Billing')}</a>
            </div>
          )}
        </MainPanelTopbar>
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <OrgNav
              currentItem="users"
              orgID={this.props.orgID}
              orgName={orgName}
            />
            <div className="d-flex align-items-center mb-3">
              <span className="mr-2">{gettext('Status')}</span>
              {['all', 'active', 'deactivated', 'deleted'].map(status => {
                const isActiveStatus = this.state.statusFilter === status;
                const labelMap = {
                  all: gettext('All'),
                  active: gettext('Active'),
                  deactivated: gettext('Inactive'),
                  deleted: gettext('Deleted')
                };
                return (
                  <button
                    key={status}
                    className={`btn btn-sm mr-2 ${isActiveStatus ? 'btn-primary' : 'btn-outline-secondary'}`}
                    onClick={() => this.setStatusFilter(status)}
                  >
                    {labelMap[status]}
                  </button>
                );
              })}
            </div>
            <div className="cur-view-content">
              <Content
                loading={this.state.loading}
                errorMsg={this.state.errorMsg}
                items={this.state.userList}
                updateStatus={this.updateStatus}
                updateMembership={this.updateMembership}
                deleteUser={this.deleteUser}
                restoreUser={this.restoreUser}
              />
            </div>
          </div>
        </div>
        {canAddUsers && isAddUserDialogOpen &&
          <SysAdminAddUserDialog
            addUser={this.addUser}
            toggleDialog={this.toggleAddUserDialog}
          />
        }
      </Fragment>
    );
  }
}

OrgUsers.propTypes = {
  orgID: PropTypes.string,
};

export default OrgUsers;
