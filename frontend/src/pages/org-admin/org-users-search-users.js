import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { Form, FormGroup, Input, Col } from 'reactstrap';
import { Utils } from '../../utils/utils';
import { seafileAPI } from '../../utils/seafile-api';
import { gettext, orgID } from '../../utils/constants';
import toaster from '../../components/toast';
import UserItem from './org-user-item';
import OrgUserInfo from '../../models/org-user';

class OrgUsersSearchUsersResult extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      isItemFreezed: false
    };
  }

  onFreezedItem = () => {
    this.setState({ isItemFreezed: true });
  };

  onUnfreezedItem = () => {
    this.setState({ isItemFreezed: false });
  };

  toggleItemFreezed = (isFreezed) => {
    this.setState({ isItemFreezed: isFreezed });
  };

  render() {
    let { orgUsers, changeStatus, restoreUser } = this.props;
    return (
      <div className="cur-view-content">
        <table>
          <thead>
            <tr>
              <th width="30%">{gettext('Name')}</th>
              <th width="15%">{gettext('Status')}</th>
              <th width="20%">
                <a className="d-inline-block table-sort-op" href="#" >{gettext('Space Used')}</a> / {gettext('Quota')}
              </th>
              <th width="25%">{gettext('Created At')} / {gettext('Last Login')}</th>
              <th width="10%">{/*Operations*/}</th>
            </tr>
          </thead>
          <tbody>
            {orgUsers.map((item, index) => {
              return (
                <UserItem
                  key={index}
                  user={item}
                  currentTab="users"
                  isItemFreezed={this.state.isItemFreezed}
                  toggleDelete={this.props.toggleDelete}
                  restoreUser={restoreUser}
                  onFreezedItem={this.onFreezedItem}
                  onUnfreezedItem={this.onUnfreezedItem}
                  toggleItemFreezed={this.toggleItemFreezed}
                  changeStatus={changeStatus}
                />
              );
            })}
          </tbody>
        </table>
      </div>
    );
  }
}

OrgUsersSearchUsersResult.propTypes = {
  toggleDelete: PropTypes.func.isRequired,
  restoreUser: PropTypes.func,
  orgUsers: PropTypes.array.isRequired,
  changeStatus: PropTypes.func.isRequired,
};

class OrgUsersSearchUsers extends Component {

  constructor(props) {
    super(props);
    this.state = {
      query: '',
      orgUsers: [],
      org_id: '',
      statusFilter: 'all',
      isSubmitBtnActive: false,
      loading: true,
      errorMsg: '',
      currentPage: 1,
      perPage: 25,
      pageInfo: {
        current_page: 1,
        has_next_page: false,
      },
    };
  }

  componentDidMount() {
    let params = (new URL(document.location)).searchParams;
    this.setState({
      query: params.get('query') || '',
      statusFilter: params.get('status') || 'all',
      currentPage: parseInt(params.get('page') || 1),
      perPage: parseInt(params.get('per_page') || 25)
    }, () => { this.getItems(this.state.currentPage); });
  }

  getItems = (page = 1) => {
    seafileAPI.orgAdminSearchUser(orgID, this.state.query.trim(), page, this.state.perPage, this.state.statusFilter).then(res => {
      let userList = (res.data.user_list || []).map(item => {
        return new OrgUserInfo(item);
      });
      this.setState({
        orgUsers: userList,
        loading: false,
        currentPage: page,
        pageInfo: res.data.page_info || {
          current_page: res.data.page || page,
          has_next_page: res.data.page_next || false,
        }
      });
    }).catch((error) => {
      this.setState({
        loading: false,
        errorMsg: Utils.getErrorMsg(error, true)
      });
    });
  };

  deleteUser = (email, username) => {
    seafileAPI.orgAdminDeleteOrgUser(orgID, email).then(() => {
      const targetPage = this.state.orgUsers.length === 1 && this.state.currentPage > 1 ? this.state.currentPage - 1 : this.state.currentPage;
      this.getItems(targetPage);
      let msg = gettext('Deleted user %s');
      msg = msg.replace('%s', username);
      toaster.success(msg);
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  restoreUser = (email, username) => {
    seafileAPI.orgAdminRestoreOrgUser(orgID, email).then(() => {
      this.getItems(this.state.currentPage);
      let msg = gettext('Restored user %s');
      msg = msg.replace('%s', username);
      toaster.success(msg);
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  handleInputChange = (e) => {
    this.setState({
      query: e.target.value
    }, this.checkSubmitBtnActive);
  };

  checkSubmitBtnActive = () => {
    const { query } = this.state;
    this.setState({
      isSubmitBtnActive: query.trim()
    });
  };

  handleKeyDown = (e) => {
    if (e.keyCode === 13) {
      const { isSubmitBtnActive } = this.state;
      if (isSubmitBtnActive) {
        this.getItems(1);
      }
    }
  };

  getPreviousPageList = () => {
    this.getItems(this.state.pageInfo.current_page - 1);
  };

  getNextPageList = () => {
    this.getItems(this.state.pageInfo.current_page + 1);
  };

  setStatusFilter = (statusFilter) => {
    this.setState({
      statusFilter,
      currentPage: 1
    }, () => {
      this.getItems(1);
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

  render() {
    const { query, isSubmitBtnActive } = this.state;

    return (
      <Fragment>
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <div className="cur-view-path">
              <h3 className="sf-heading">{gettext('Users')}</h3>
            </div>
            <div className="cur-view-content">
              <div className="mt-4 mb-6">
                <h4 className="border-bottom font-weight-normal mb-2 pb-1">{gettext('Search Users')}</h4>
                <Form tag={'div'}>
                  <FormGroup row>
                    <Col sm={5}>
                      <Input type="text" name="query" value={query} placeholder={gettext('Search users')} onChange={this.handleInputChange} onKeyDown={this.handleKeyDown} />
                    </Col>
                  </FormGroup>
                  <FormGroup row>
                    <Col sm={{ size: 5 }}>
                      <button className="btn btn-outline-primary" disabled={!isSubmitBtnActive} onClick={() => this.getItems(1)}>{gettext('Submit')}</button>
                    </Col>
                  </FormGroup>
                </Form>
              </div>
              <div className="mt-4 mb-6">
                <h4 className="border-bottom font-weight-normal mb-2 pb-1">{gettext('Result')}</h4>
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
                <OrgUsersSearchUsersResult
                  toggleDelete={this.deleteUser}
                  restoreUser={this.restoreUser}
                  changeStatus={this.changeStatus}
                  orgUsers={this.state.orgUsers}
                />
                <div className="paginator">
                  {this.state.pageInfo.current_page > 1 && <a href="#" onClick={(e) => { e.preventDefault(); this.getPreviousPageList(); }}>{gettext('Previous')}</a>}
                  {(this.state.pageInfo.current_page > 1 && this.state.pageInfo.has_next_page) && <span> | </span>}
                  {this.state.pageInfo.has_next_page && <a href="#" onClick={(e) => { e.preventDefault(); this.getNextPageList(); }}>{gettext('Next')}</a>}
                </div>
              </div>
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

export default OrgUsersSearchUsers;
