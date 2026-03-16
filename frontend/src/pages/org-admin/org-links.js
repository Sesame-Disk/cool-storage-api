import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import moment from 'moment';
import { Dropdown, DropdownToggle, DropdownMenu, DropdownItem } from 'reactstrap';
import { seafileAPI } from '../../utils/seafile-api';
import { siteRoot, gettext } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import toaster from '../../components/toast';
import MainPanelTopbar from './main-panel-topbar';
import ShareAdminLinkEnhanced from '../../components/dialog/share-admin-link-enhanced';

class OrgLinks extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      currentTab: 'shareLinks',
      // Share links state
      shareLinkList: null,
      sharePage: 1,
      sharePageNext: false,
      // Upload links state
      uploadLinkList: null,
      uploadPage: 1,
      uploadPageNext: false,
      // Common
      isItemFreezed: false,
      isShowLinkDialog: false,
      currentLink: null,
    };
  }

  componentDidMount() {
    this.listShareLinks(1);
    this.listUploadLinks(1);
  }

  switchTab = (tab) => {
    this.setState({ currentTab: tab });
  };

  // Share links
  listShareLinks = (page) => {
    seafileAPI.orgAdminListOrgLinks(page).then(res => {
      const data = res.data;
      this.setState({
        shareLinkList: data.link_list,
        sharePage: data.page,
        sharePageNext: data.page_next,
      });
    });
  };

  deleteShareLink = (token) => {
    seafileAPI.orgAdminDeleteOrgLink(token).then(res => {
      if (res.data.success === true) {
        this.listShareLinks(this.state.sharePage);
      }
    }).catch(error => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  onSharePageChange = (event, num) => {
    event.preventDefault();
    let page = this.state.sharePage + num;
    this.listShareLinks(page);
  };

  // Upload links
  listUploadLinks = (page) => {
    seafileAPI.orgAdminListOrgUploadLinks(page).then(res => {
      const data = res.data;
      this.setState({
        uploadLinkList: data.upload_link_list,
        uploadPage: data.page || 1,
        uploadPageNext: Utils.hasNextPage(page, 25, data.count),
      });
    });
  };

  deleteUploadLink = (token) => {
    seafileAPI.orgAdminDeleteOrgUploadLink(token).then(res => {
      if (res.data.success === true) {
        this.listUploadLinks(this.state.uploadPage);
      }
    }).catch(error => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  onUploadPageChange = (event, num) => {
    event.preventDefault();
    let page = this.state.uploadPage + num;
    this.listUploadLinks(page);
  };

  // Common
  onFreezedItem = () => {
    this.setState({ isItemFreezed: true });
  };

  onUnfreezedItem = () => {
    this.setState({ isItemFreezed: false });
  };

  openLinkDialog = (link) => {
    this.setState({ currentLink: link });
    this.toggleLinkDialog();
  };

  toggleLinkDialog = () => {
    this.setState({ isShowLinkDialog: !this.state.isShowLinkDialog });
  };

  render() {
    const { currentTab, shareLinkList, uploadLinkList } = this.state;
    const isShare = currentTab === 'shareLinks';
    const linkList = isShare ? shareLinkList : uploadLinkList;
    const page = isShare ? this.state.sharePage : this.state.uploadPage;
    const pageNext = isShare ? this.state.sharePageNext : this.state.uploadPageNext;
    const deleteLink = isShare ? this.deleteShareLink : this.deleteUploadLink;
    const onPageChange = isShare ? this.onSharePageChange : this.onUploadPageChange;

    return (
      <Fragment>
        <MainPanelTopbar />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <div className="cur-view-path tab-nav-container">
              <ul className="nav">
                <li className="nav-item">
                  <a href="#" className={`nav-link${currentTab === 'shareLinks' ? ' active' : ''}`} onClick={(e) => { e.preventDefault(); this.switchTab('shareLinks'); }}>{gettext('Share Links')}</a>
                </li>
                <li className="nav-item">
                  <a href="#" className={`nav-link${currentTab === 'uploadLinks' ? ' active' : ''}`} onClick={(e) => { e.preventDefault(); this.switchTab('uploadLinks'); }}>{gettext('Upload Links')}</a>
                </li>
              </ul>
            </div>
            <div className="cur-view-content">
              <table>
                <thead>
                  <tr>
                    <th width="30%">{gettext('Name')}</th>
                    <th width="20%">{gettext('Token')}</th>
                    <th width="15%">{gettext('Owner')}</th>
                    <th width="15%">{gettext('Created At')}</th>
                    <th width="10%">{gettext('Count')}</th>
                    <th width="10%"></th>
                  </tr>
                </thead>
                <tbody>
                  {linkList && linkList.map((item, index) => {
                    return (
                      <React.Fragment key={index}>
                        <LinkItem
                          link={item}
                          isShareLink={isShare}
                          isItemFreezed={this.state.isItemFreezed}
                          onFreezedItem={this.onFreezedItem}
                          onUnfreezedItem={this.onUnfreezedItem}
                          deleteLink={deleteLink}
                          openLinkDialog={this.openLinkDialog}
                        />
                      </React.Fragment>
                    );
                  })}
                </tbody>
              </table>
              <div className="paginator">
                {page !== 1 && <a href="#" onClick={(e) => onPageChange(e, -1)}>{gettext('Previous')}</a>}
                {(page !== 1 && pageNext) && <span> | </span>}
                {pageNext && <a href="#" onClick={(e) => onPageChange(e, 1)}>{gettext('Next')}</a>}
              </div>
            </div>
          </div>
        </div>
        {this.state.isShowLinkDialog && this.state.currentLink &&
          <ShareAdminLinkEnhanced
            link={this.state.currentLink.link || ''}
            password={this.state.currentLink.password || ''}
            viewCount={this.state.currentLink.view_count || this.state.currentLink.view_cnt || 0}
            isShareLink={isShare}
            toggleDialog={this.toggleLinkDialog}
          />
        }
      </Fragment>
    );
  }
}

const linkItemPropTypes = {
  link: PropTypes.object.isRequired,
  isShareLink: PropTypes.bool.isRequired,
  isItemFreezed: PropTypes.bool.isRequired,
  onFreezedItem: PropTypes.func.isRequired,
  onUnfreezedItem: PropTypes.func.isRequired,
  deleteLink: PropTypes.func.isRequired,
  openLinkDialog: PropTypes.func.isRequired,
};

class LinkItem extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      highlight: false,
      showMenu: false,
      isItemMenuShow: false,
    };
  }

  onMouseEnter = () => {
    if (!this.props.isItemFreezed) {
      this.setState({ showMenu: true, highlight: true });
    }
  };

  onMouseLeave = () => {
    if (!this.props.isItemFreezed) {
      this.setState({ showMenu: false, highlight: false });
    }
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
          this.setState({ highlight: false, showMenu: false });
          this.props.onUnfreezedItem();
        }
      }
    );
  };

  render() {
    const { link, deleteLink } = this.props;
    const ownerName = link.owner_name || link.creator_name || '';
    const ownerEmail = link.owner_email || link.creator_email || '';
    const name = link.name || link.obj_name || link.path || '';
    const token = link.token || '';
    const createdTime = link.created_time || link.ctime || '';
    const viewCount = link.view_count !== undefined ? link.view_count : (link.view_cnt !== undefined ? link.view_cnt : 0);
    const href = siteRoot + 'org/useradmin/info/' + encodeURIComponent(ownerEmail) + '/';

    return (
      <tr className={this.state.highlight ? 'tr-highlight' : ''} onMouseEnter={this.onMouseEnter} onMouseLeave={this.onMouseLeave}>
        <td>{name}</td>
        <td>{token}</td>
        <td><a href={href}>{ownerName}</a></td>
        <td>{createdTime ? moment(createdTime).fromNow() : ''}</td>
        <td>{viewCount}</td>
        <td className="cursor-pointer text-center">
          {this.state.showMenu &&
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
                <DropdownItem onClick={deleteLink.bind(this, token)}>{gettext('Delete')}</DropdownItem>
                <DropdownItem onClick={this.props.openLinkDialog.bind(this, link)}>{gettext('View Link')}</DropdownItem>
              </DropdownMenu>
            </Dropdown>
          }
        </td>
      </tr>
    );
  }
}

LinkItem.propTypes = linkItemPropTypes;

export default OrgLinks;
