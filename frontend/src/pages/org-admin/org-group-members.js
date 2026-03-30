import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { Link } from '@gatsbyjs/reach-router';
import { seafileAPI } from '../../utils/seafile-api';
import { gettext, siteRoot } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import Loading from '../../components/loading';
import toaster from '../../components/toast';
import RoleSelector from '../../components/single-selector';
import OrgAdminGroupNav from '../../components/org-admin-group-nav';
import MainPanelTopbar from './main-panel-topbar';

import '../../css/org-admin-user.css';

class OrgGroupMembers extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      members: [],
      isItemFreezed: false,
    };
  }

  componentDidMount() {
    const { orgID } = window.org.pageOptions;
    seafileAPI.orgAdminListGroupMembers(orgID, this.props.groupID).then((res) => {
      this.setState({
        loading: false,
        members: res.data.members || [],
      });
    }).catch((error) => {
      this.setState({
        loading: false,
        errorMsg: Utils.getErrorMsg(error, true),
      });
    });
  }

  updateMemberRole = (email, role) => {
    const { orgID } = window.org.pageOptions;
    const isAdmin = role === 'Admin';
    seafileAPI.orgAdminSetGroupMemberRole(orgID, this.props.groupID, email, isAdmin).then(() => {
      const members = this.state.members.map(m => {
        if (m.email === email) {
          return Object.assign({}, m, { role });
        }
        return m;
      });
      this.setState({ members });
    }).catch(error => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  toggleItemFreezed = (isFreezed) => {
    this.setState({ isItemFreezed: isFreezed });
  };

  render() {
    const { loading, errorMsg, members, isItemFreezed } = this.state;
    return (
      <Fragment>
        <MainPanelTopbar/>
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <OrgAdminGroupNav groupID={this.props.groupID} currentItem='members' />
            <div className="cur-view-content">
              {loading ? (
                <Loading />
              ) : errorMsg ? (
                <p className="error text-center mt-2">{errorMsg}</p>
              ) : (
                <table className="table-hover">
                  <thead>
                    <tr>
                      <th width="10%"></th>
                      <th width="50%">{gettext('Name')}</th>
                      <th width="40%">{gettext('Role')}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {members.map((item, index) => (
                      <Item
                        key={index}
                        data={item}
                        isItemFreezed={isItemFreezed}
                        toggleItemFreezed={this.toggleItemFreezed}
                        updateMemberRole={this.updateMemberRole}
                      />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

class Item extends Component {

  constructor(props) {
    super(props);
    this.roleOptions = [
      { value: 'Admin', text: gettext('Admin'), isSelected: false },
      { value: 'Member', text: gettext('Member'), isSelected: false },
    ];
    this.state = {
      highlighted: false,
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

  updateMemberRole = (roleOption) => {
    this.props.updateMemberRole(this.props.data.email, roleOption.value);
  };

  render() {
    const { data: item } = this.props;
    const { highlighted } = this.state;

    this.roleOptions = this.roleOptions.map(opt => {
      opt.isSelected = opt.value === item.role;
      return opt;
    });
    const currentSelectedOption = this.roleOptions.find(opt => opt.isSelected);

    return (
      <tr
        className={highlighted ? 'tr-highlight' : ''}
        onMouseEnter={this.handleMouseEnter}
        onMouseLeave={this.handleMouseLeave}
      >
        <td className="text-center">
          <img src={item.avatar_url} alt="" className="avatar" width="32" />
        </td>
        <td>
          <Link to={`${siteRoot}org/useradmin/info/${encodeURIComponent(item.email)}/`}>{item.name}</Link>
        </td>
        <td>
          {item.role === 'Owner' ? (
            gettext('Owner')
          ) : (
            <RoleSelector
              isDropdownToggleShown={highlighted}
              currentSelectedOption={currentSelectedOption}
              options={this.roleOptions}
              selectOption={this.updateMemberRole}
              toggleItemFreezed={this.props.toggleItemFreezed}
            />
          )}
        </td>
      </tr>
    );
  }
}

Item.propTypes = {
  data: PropTypes.object.isRequired,
  isItemFreezed: PropTypes.bool.isRequired,
  toggleItemFreezed: PropTypes.func.isRequired,
  updateMemberRole: PropTypes.func.isRequired,
};

OrgGroupMembers.propTypes = {
  groupID: PropTypes.string,
};

export default OrgGroupMembers;
