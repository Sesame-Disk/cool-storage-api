import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { Link } from '@gatsbyjs/reach-router';
import moment from 'moment';
import { Utils } from '../../../utils/utils';
import { siteRoot, gettext } from '../../../utils/constants';
import { appendRetentionNotice, getDeletedOrganizationsRetentionMessage } from '../../../utils/trash-retention';
import EmptyTip from '../../../components/empty-tip';
import Loading from '../../../components/loading';
import Paginator from '../../../components/paginator';
import { seafileAPI } from '../../../utils/seafile-api';
import CommonOperationConfirmationDialog from '../../../components/dialog/common-operation-confirmation-dialog';
import UserLink from '../user-link';
import toaster from '../../../components/toast';

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

  getPreviousPage = () => {
    this.props.getListByPage(this.props.currentPage - 1);
  };

  getNextPage = () => {
    this.props.getListByPage(this.props.currentPage + 1);
  };

  render() {
    const { loading, errorMsg, items } = this.props;
    const deletedOrganizationsRetentionMessage = getDeletedOrganizationsRetentionMessage('sys');
    if (loading) {
      return <Loading />;
    } else if (errorMsg) {
      return <p className="error text-center mt-4">{errorMsg}</p>;
    } else {
      const emptyTip = (
        <EmptyTip>
          <h2>{gettext('No organizations')}</h2>
        </EmptyTip>
      );
      const table = (
        <Fragment>
          <p className="mt-2 small text-secondary">{deletedOrganizationsRetentionMessage}</p>
          <table>
            <thead>
              <tr>
                <th width="18%">{gettext('Name')}</th>
                <th width="16%">{gettext('Owner')}</th>
                <th width="12%">{gettext('Status')}</th>
                <th width="16%">{gettext('Plan')}</th>
                <th width="14%">{gettext('Space Used')}</th>
                <th width="16%">{gettext('Created At')}</th>
                <th width="8%">{/* Operations */}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item, index) => {
                return (<Item
                  key={index}
                  item={item}
                  deleteOrg={this.props.deleteOrg}
                  deactivateOrg={this.props.deactivateOrg}
                  reactivateOrg={this.props.reactivateOrg}
                  restoreOrg={this.props.restoreOrg}
                  isItemFreezed={this.state.isItemFreezed}
                  toggleItemFreezed={this.toggleItemFreezed}
                />);
              })}
            </tbody>
          </table>
          {this.props.currentPage &&
            <Paginator
              currentPage={this.props.currentPage}
              hasNextPage={this.props.hasNextPage}
              curPerPage={this.props.curPerPage}
              resetPerPage={this.props.resetPerPage}
              gotoPreviousPage={this.getPreviousPage}
              gotoNextPage={this.getNextPage}
            />
          }
        </Fragment>
      );
      return items.length ? table : emptyTip;
    }
  }
}

Content.propTypes = {
  loading: PropTypes.bool.isRequired,
  errorMsg: PropTypes.string.isRequired,
  getListByPage: PropTypes.func.isRequired,
  currentPage: PropTypes.number,
  items: PropTypes.array.isRequired,
  deleteOrg: PropTypes.func.isRequired,
  deactivateOrg: PropTypes.func.isRequired,
  reactivateOrg: PropTypes.func.isRequired,
  restoreOrg: PropTypes.func.isRequired,
  hasNextPage: PropTypes.bool,
  resetPerPage: PropTypes.func,
  curPerPage: PropTypes.number,
};

class Item extends Component {

  constructor(props) {
    super(props);
    this.state = {
      highlighted: false,
      isOperationDialogOpen: false,
      operationDialogTitle: '',
      operationDialogMsg: '',
      operationDialogConfirmText: '',
      operationHandler: null,
    };
  }

  handleMouseEnter = () => {
    if (this.props.isItemFreezed) return;
    this.setState({ highlighted: true });
  };

  handleMouseLeave = () => {
    if (this.props.isItemFreezed) return;
    this.setState({ highlighted: false });
  };

  getEffectiveStatus = () => {
    return this.props.item.status || 'active';
  };

  getStatusDisplay = (status) => {
    switch (status) {
      case 'deactivated':
        return gettext('Inactive');
      case 'deleted':
        return gettext('Deleted');
      default:
        return gettext('Active');
    }
  };

  getStatusClass = (status) => {
    switch (status) {
      case 'deactivated':
        return 'badge badge-warning';
      case 'deleted':
        return 'badge badge-danger';
      default:
        return 'badge badge-success';
    }
  };

  toggleOperationDialog = (e, operationType) => {
    if (e) {
      e.preventDefault();
    }

    if (this.state.isOperationDialogOpen) {
      this.setState({ isOperationDialogOpen: false });
      return;
    }

    if (operationType === 'delete') {
      seafileAPI.sysAdminGetOrg(this.props.item.org_id).then((res) => {
        let orgName = '<span class="op-target">' + Utils.HTMLescape(res.data.org_name) + '</span>';
        let userCount = '<span class="op-target">' + Utils.HTMLescape(res.data.users_count) + '</span>';
        let repoCount = '<span class="op-target">' + Utils.HTMLescape(res.data.repos_count) + '</span>';
        let operationDialogMsg = appendRetentionNotice(
          gettext('Are you sure you want to delete {placeholder} ?').replace('{placeholder}', orgName),
          getDeletedOrganizationsRetentionMessage('sys')
        ) + '<br/>' +
          gettext('{userCount} user(s) and {repoCount} libraries of this organization will also be deleted.')
            .replace('{userCount}', userCount)
            .replace('{repoCount}', repoCount);
        this.setState({
          isOperationDialogOpen: true,
          operationDialogTitle: gettext('Delete Organization'),
          operationDialogMsg,
          operationDialogConfirmText: gettext('Delete'),
          operationHandler: this.deleteOrg,
        });
      }).catch(error => {
        let errorMsg = Utils.getErrorMsg(error);
        toaster.danger(errorMsg);
      });
      return;
    }

    const orgName = '<span class="op-target">' + Utils.HTMLescape(this.props.item.org_name) + '</span>';
    const deletedOrganizationsRetentionMessage = getDeletedOrganizationsRetentionMessage('sys');
    if (operationType === 'deactivate') {
      this.setState({
        isOperationDialogOpen: true,
        operationDialogTitle: gettext('Deactivate Organization'),
        operationDialogMsg: gettext('Are you sure you want to deactivate {placeholder} ?').replace('{placeholder}', orgName),
        operationDialogConfirmText: gettext('Deactivate'),
        operationHandler: this.deactivateOrg,
      });
      return;
    }

    if (operationType === 'reactivate') {
      this.setState({
        isOperationDialogOpen: true,
        operationDialogTitle: gettext('Reactivate Organization'),
        operationDialogMsg: gettext('Are you sure you want to reactivate {placeholder} ?').replace('{placeholder}', orgName),
        operationDialogConfirmText: gettext('Reactivate'),
        operationHandler: this.reactivateOrg,
      });
      return;
    }

    this.setState({
      isOperationDialogOpen: true,
      operationDialogTitle: gettext('Restore Organization'),
      operationDialogMsg: appendRetentionNotice(
        gettext('Are you sure you want to restore {placeholder} ?').replace('{placeholder}', orgName),
        deletedOrganizationsRetentionMessage
      ),
      operationDialogConfirmText: gettext('Restore'),
      operationHandler: this.restoreOrg,
    });
  };

  deleteOrg = () => {
    toaster.notify(gettext('It may take some time, please wait.'));
    this.props.deleteOrg(this.props.item.org_id);
  };

  deactivateOrg = () => {
    toaster.notify(gettext('It may take some time, please wait.'));
    this.props.deactivateOrg(this.props.item.org_id);
  };

  reactivateOrg = () => {
    toaster.notify(gettext('It may take some time, please wait.'));
    this.props.reactivateOrg(this.props.item.org_id);
  };

  restoreOrg = () => {
    toaster.notify(gettext('It may take some time, please wait.'));
    this.props.restoreOrg(this.props.item.org_id);
  };

  render() {
    const { item } = this.props;
    const {
      highlighted,
      isOperationDialogOpen,
      operationDialogTitle,
      operationDialogMsg,
      operationDialogConfirmText,
      operationHandler,
    } = this.state;
    const status = this.getEffectiveStatus();

    return (
      <Fragment>
        <tr className={highlighted ? 'tr-highlight' : ''} onMouseEnter={this.handleMouseEnter} onMouseLeave={this.handleMouseLeave}>
          <td><Link to={`${siteRoot}sys/organizations/${item.org_id}/info/`}>{item.org_name}</Link></td>
          <td>
            <UserLink email={item.owner_email} name={item.owner_name} />
          </td>
          <td>
            <span className={this.getStatusClass(status)}>{this.getStatusDisplay(status)}</span>
          </td>
          <td>{item.plan || '--'}</td>
          <td>{`${Utils.bytesToSize(item.quota_usage)} / ${item.quota > 0 ? Utils.bytesToSize(item.quota) : '--'}`}</td>
          <td>{moment(item.ctime).format('YYYY-MM-DD HH:mm:ss')}</td>
          <td>
            {status === 'active' && (
              <Fragment>
                <a href="#" className={`action-icon sf2-icon-x3 mr-2 ${highlighted ? '' : 'invisible'}`} title={gettext('Deactivate')} aria-label={gettext('Deactivate')} onClick={(e) => this.toggleOperationDialog(e, 'deactivate')}></a>
                <a href="#" className={`action-icon sf2-icon-delete ${highlighted ? '' : 'invisible'}`} title={gettext('Delete')} aria-label={gettext('Delete')} onClick={(e) => this.toggleOperationDialog(e, 'delete')}></a>
              </Fragment>
            )}
            {status === 'deactivated' && (
              <Fragment>
                <a href="#" className={`action-icon sf2-icon-reply mr-2 ${highlighted ? '' : 'invisible'}`} title={gettext('Reactivate')} aria-label={gettext('Reactivate')} onClick={(e) => this.toggleOperationDialog(e, 'reactivate')}></a>
                <a href="#" className={`action-icon sf2-icon-delete ${highlighted ? '' : 'invisible'}`} title={gettext('Delete')} aria-label={gettext('Delete')} onClick={(e) => this.toggleOperationDialog(e, 'delete')}></a>
              </Fragment>
            )}
            {status === 'deleted' && (
              <a href="#" className={`action-icon sf2-icon-reply ${highlighted ? '' : 'invisible'}`} title={gettext('Restore')} aria-label={gettext('Restore')} onClick={(e) => this.toggleOperationDialog(e, 'restore')}></a>
            )}
          </td>
        </tr>
        {isOperationDialogOpen &&
          <CommonOperationConfirmationDialog
            title={operationDialogTitle}
            message={operationDialogMsg}
            executeOperation={operationHandler}
            confirmBtnText={operationDialogConfirmText}
            toggleDialog={this.toggleOperationDialog}
          />
        }
      </Fragment>
    );
  }
}

Item.propTypes = {
  item: PropTypes.object.isRequired,
  deleteOrg: PropTypes.func.isRequired,
  deactivateOrg: PropTypes.func.isRequired,
  reactivateOrg: PropTypes.func.isRequired,
  restoreOrg: PropTypes.func.isRequired,
  isItemFreezed: PropTypes.bool.isRequired,
  toggleItemFreezed: PropTypes.func.isRequired
};

export default Content;
