import React from 'react';
import PropTypes from 'prop-types';
import { Dropdown, DropdownToggle, DropdownMenu, DropdownItem } from 'reactstrap';
import { gettext, siteRoot, orgID, username } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { ACCOUNTS_ORG_USER_ACTIONS, ACCOUNTS_ORG_USER_VIEWS, buildAccountsOrgUserManagementURL } from '../../utils/accounts-org-user-management';
import { appendRetentionNotice, getDeletedUsersRetentionMessage } from '../../utils/trash-retention';
import { Utils } from '../../utils/utils';
import toaster from '../../components/toast';
import Selector from '../../components/single-selector';
import CommonOperationConfirmationDialog from '../../components/dialog/common-operation-confirmation-dialog';

const propTypes = {
  accountsOrgManagementURL: PropTypes.string,
  user: PropTypes.object,
  currentTab: PropTypes.string,
  canManageUsers: PropTypes.bool,
  toggleRevokeAdmin: PropTypes.func,
  isItemFreezed: PropTypes.bool.isRequired,
  toggleDelete: PropTypes.func.isRequired,
  restoreUser: PropTypes.func,
  onFreezedItem: PropTypes.func.isRequired,
  onUnfreezedItem: PropTypes.func.isRequired,
  toggleItemFreezed: PropTypes.func,
  changeStatus: PropTypes.func.isRequired,
};

class UserItem extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      highlight: false,
      showMenu: false,
      isItemMenuShow: false,
      isConfirmInactiveDialogOpen: false,
      isRestoreUserDialogOpen: false
    };
  }

  onMouseEnter = () => {
    if (!this.props.isItemFreezed) {
      this.setState({
        showMenu: true,
        highlight: true,
      });
    }
  };

  onMouseLeave = () => {
    if (!this.props.isItemFreezed) {
      this.setState({
        showMenu: false,
        highlight: false
      });
    }
  };

  toggleDelete = () => {
    const email = this.props.user.email;
    const username = this.props.user.name;
    this.props.toggleDelete(email, username);
  };


  toggleRevokeAdmin = () => {
    const email = this.props.user.email;
    this.props.toggleRevokeAdmin(email);
  };

  restoreUser = () => {
    const { email, name } = this.props.user;
    this.props.restoreUser(email, name);
  };

  changeStatus = (statusOption) => {
    const isActive = statusOption.value === 'active';
    if (isActive) {
      toaster.notify(gettext('It may take some time, please wait.'));
    }
    this.props.changeStatus(this.props.user.email, isActive);
  };

  setUserInactive = () => {
    const isActive = false;
    this.props.changeStatus(this.props.user.email, isActive);
  };

  onDropdownToggleClick = (e) => {
    e.preventDefault();
    this.toggleOperationMenu(e);
  };

  toggleOperationMenu = (e) => {
    e.stopPropagation();
    this.setState(
      { isItemMenuShow: !this.state.isItemMenuShow }, () => {
        if (this.state.isItemMenuShow) {
          this.props.onFreezedItem();
        } else {
          this.setState({
            highlight: false,
            showMenu: false,
          });
          this.props.onUnfreezedItem();
        }
      }
    );
  };

  getQuotaTotal = (data) => {
    switch (data) {
      case -1: // failed to fetch quota
        return gettext('Failed');
      case -2:
        return '--';
      default: // data > 0
        return Utils.bytesToSize(data);
    }
  };

  translateStatus = (status) => {
    switch (status) {
      case 'active':
        return gettext('Active');
      case 'deactivated':
      case 'inactive':
        return gettext('Inactive');
      case 'deleted':
        return gettext('Deleted');
    }
  };

  toggleConfirmInactiveDialog = () => {
    this.setState({ isConfirmInactiveDialogOpen: !this.state.isConfirmInactiveDialogOpen });
  };

  toggleRestoreUserDialog = () => {
    this.setState({ isRestoreUserDialogOpen: !this.state.isRestoreUserDialogOpen });
  };

  getAccountsURL = (action, extraOptions = {}) => {
    return buildAccountsOrgUserManagementURL(this.props.accountsOrgManagementURL, {
      view: ACCOUNTS_ORG_USER_VIEWS.USER,
      action,
      userEmail: this.props.user.email,
      ...extraOptions,
    });
  };

  getMenuOperations = () => {
    if (this.props.canManageUsers === false) {
      return [];
    }
    const { currentTab, user } = this.props;
    if (user.status === 'deleted') {
      return ['Restore'];
    }

    const operations = ['Delete'];
    if (currentTab === 'admins') {
      operations.push('Revoke Admin');
    }
    return operations;
  };

  translateOperation = (operation) => {
    switch (operation) {
      case 'Delete':
        return gettext('Delete');

      case 'Revoke Admin':
        return gettext('Revoke Admin');
      case 'Restore':
        return gettext('Restore');
      default:
        return operation;
    }
  };

  getExternalMenuOperations = (effectiveStatus) => {
    const operations = [];
    const manageUserURL = this.getAccountsURL(ACCOUNTS_ORG_USER_ACTIONS.MANAGE_USER);

    if (manageUserURL) {
      operations.push({
        label: gettext('Manage in Accounts'),
        url: manageUserURL,
      });
    }

    if (effectiveStatus === 'deleted') {
      const restoreURL = this.getAccountsURL(ACCOUNTS_ORG_USER_ACTIONS.RESTORE_USER);
      if (restoreURL) {
        operations.push({
          label: gettext('Restore'),
          url: restoreURL,
        });
      }
      return operations;
    }

    const targetStatus = effectiveStatus === 'active' ? 'deactivated' : 'active';
    const statusURL = this.getAccountsURL(ACCOUNTS_ORG_USER_ACTIONS.SET_STATUS, { status: targetStatus });
    if (statusURL) {
      operations.push({
        label: targetStatus === 'active' ? gettext('Activate') : gettext('Deactivate'),
        url: statusURL,
      });
    }

    const deleteURL = this.getAccountsURL(ACCOUNTS_ORG_USER_ACTIONS.DELETE_USER);
    if (deleteURL) {
      operations.push({
        label: gettext('Delete'),
        url: deleteURL,
      });
    }


    if (this.props.currentTab === 'admins') {
      const revokeAdminURL = this.getAccountsURL(ACCOUNTS_ORG_USER_ACTIONS.REVOKE_ADMIN);
      if (revokeAdminURL) {
        operations.push({
          label: gettext('Revoke Admin'),
          url: revokeAdminURL,
        });
      }
    }

    return operations;
  };

  onMenuItemClick = (operation) => {
    switch (operation) {
      case 'Delete':
        this.toggleDelete();
        break;

      case 'Revoke Admin':
        this.toggleRevokeAdmin();
        break;
      case 'Restore':
        this.toggleRestoreUserDialog();
        break;
      default:
        break;
    }
  };

  render() {
    const { highlight, isConfirmInactiveDialogOpen, isRestoreUserDialogOpen } = this.state;
    let { user } = this.props;
    let href = siteRoot + 'org/useradmin/info/' + encodeURIComponent(user.email) + '/';
    const effectiveStatus = user.status || (user.is_active ? 'active' : 'deactivated');
    const isDeleted = effectiveStatus === 'deleted';
    const canManageUsers = this.props.canManageUsers !== false;
    const externalMenuOperations = canManageUsers ? [] : this.getExternalMenuOperations(effectiveStatus);
    const manageUserURL = this.getAccountsURL(ACCOUNTS_ORG_USER_ACTIONS.MANAGE_USER);
    let isOperationMenuShow = (user.email !== username) && this.state.showMenu && (canManageUsers || externalMenuOperations.length > 0);

    // for 'user status'
    const curStatus = effectiveStatus === 'active' ? 'active' : 'deactivated';
    this.statusOptions = ['active', 'deactivated'].map(item => {
      return {
        value: item,
        text: this.translateStatus(item),
        isSelected: item === curStatus
      };
    });
    const currentSelectedStatusOption = this.statusOptions.filter(item => item.isSelected)[0];

    const itemName = '<span class="op-target">' + Utils.HTMLescape(user.name) + '</span>';
    const confirmSetUserInactiveMsg = gettext('Are you sure you want to set {user_placeholder} inactive?').replace('{user_placeholder}', itemName);
    const deletedUsersRetentionMessage = getDeletedUsersRetentionMessage();
    const restoreUserDialogMsg = appendRetentionNotice(
      gettext('Are you sure you want to restore {placeholder} ?').replace('{placeholder}', itemName),
      deletedUsersRetentionMessage
    );
    const menuOperations = this.getMenuOperations();

    return (
      <>
        <tr className={this.state.highlight ? 'tr-highlight' : ''} onMouseEnter={this.onMouseEnter} onMouseLeave={this.onMouseLeave}>
          <td>
            <a href={href}>{user.name}</a>
            {manageUserURL && (
              <a
                href={manageUserURL}
                className="attr-action-icon fas fa-external-link-alt ml-2 text-secondary"
                target="_blank"
                rel="noopener noreferrer"
                title={gettext('Manage in Accounts')}
                aria-label={gettext('Manage in Accounts')}
              ></a>
            )}
          </td>
          <td>
            {isDeleted ?
              <span className="badge badge-danger">{gettext('Deleted')}</span>
              : !canManageUsers ?
                <span>{this.translateStatus(curStatus)}</span>
                :
                <Selector
                  isDropdownToggleShown={highlight}
                  currentSelectedOption={currentSelectedStatusOption}
                  options={this.statusOptions}
                  selectOption={this.changeStatus}
                  toggleItemFreezed={this.props.toggleItemFreezed}
                  operationBeforeSelect={effectiveStatus === 'active' ? this.toggleConfirmInactiveDialog : undefined}
                />
            }
          </td>
          <td>{`${Utils.bytesToSize(user.quota_usage)} / ${this.getQuotaTotal(user.quota_total)}`}</td>
          <td>
            {user.ctime} /
            <br />
            {user.last_login ? user.last_login : '--'}
          </td>
          <td className="text-center cursor-pointer">
            {isOperationMenuShow && (
              <Dropdown isOpen={this.state.isItemMenuShow} toggle={this.toggleOperationMenu}>
                <DropdownToggle
                  tag="a"
                  className="attr-action-icon fas fa-ellipsis-v"
                  title={gettext('More operations')}
                  aria-label={gettext('More operations')}
                  data-toggle="dropdown"
                  aria-expanded={this.state.isItemMenuShow}
                  onClick={this.onDropdownToggleClick}
                />
                <DropdownMenu>
                  {canManageUsers ? (
                    menuOperations.map((operation, index) => (
                      <DropdownItem key={index} onClick={() => this.onMenuItemClick(operation)}>{this.translateOperation(operation)}</DropdownItem>
                    ))
                  ) : (
                    externalMenuOperations.map((operation, index) => (
                      <DropdownItem key={index} tag="a" href={operation.url} target="_blank" rel="noopener noreferrer">
                        <i className="fas fa-external-link-alt text-secondary mr-1"></i>{operation.label}
                      </DropdownItem>
                    ))
                  )}
                </DropdownMenu>
              </Dropdown>
            )}
          </td>
        </tr>
        {isConfirmInactiveDialogOpen &&
          <CommonOperationConfirmationDialog
            title={gettext('Set user inactive')}
            message={confirmSetUserInactiveMsg}
            executeOperation={this.setUserInactive}
            confirmBtnText={gettext('Set')}
            toggleDialog={this.toggleConfirmInactiveDialog}
          />
        }
        {isRestoreUserDialogOpen &&
          <CommonOperationConfirmationDialog
            title={gettext('Restore User')}
            message={restoreUserDialogMsg}
            executeOperation={this.restoreUser}
            confirmBtnText={gettext('Restore')}
            toggleDialog={this.toggleRestoreUserDialog}
          />
        }
      </>
    );
  }
}

UserItem.propTypes = propTypes;

export default UserItem;
