import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import moment from 'moment';
import { Dropdown, DropdownToggle, DropdownMenu, DropdownItem } from 'reactstrap';
import { seafileAPI } from '../../utils/seafile-api';
import { siteRoot, gettext } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import toaster from '../../components/toast';
import EmptyTip from '../../components/empty-tip';
import Loading from '../../components/loading';
import Paginator from '../../components/paginator';
import MainPanelTopbar from './main-panel-topbar';
import ShareAdminLinkEnhanced from '../../components/dialog/share-admin-link-enhanced';

class OrgLinks extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      currentTab: 'shareLinks',
      // Share links state
      shareLinkList: [],
      sharePage: 1,
      sharePageNext: false,
      sharePerPage: 25,
      shareSortBy: '',
      shareSortOrder: 'asc',
      shareExpiredFilter: 'all',
      shareLoading: true,
      shareErrorMsg: '',
      // Upload links state
      uploadLinkList: [],
      uploadPage: 1,
      uploadPageNext: false,
      uploadPerPage: 25,
      uploadSortBy: '',
      uploadSortOrder: 'asc',
      uploadExpiredFilter: 'all',
      uploadLoading: true,
      uploadErrorMsg: '',
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
    const { sharePerPage, shareSortBy, shareSortOrder, shareExpiredFilter } = this.state;
    seafileAPI.orgAdminListOrgLinks(page, sharePerPage, shareSortBy, shareSortOrder, shareExpiredFilter).then(res => {
      const data = res.data;
      this.setState({
        shareLinkList: data.link_list,
        sharePage: data.page,
        sharePageNext: Utils.hasNextPage(data.page || page, sharePerPage, data.count || 0),
        shareLoading: false,
        shareErrorMsg: '',
      });
    }).catch(error => {
      this.setState({
        shareLoading: false,
        shareErrorMsg: Utils.getErrorMsg(error, true)
      });
    });
  };

  deleteShareLink = (token) => {
    seafileAPI.orgAdminDeleteOrgLink(token).then(res => {
      if (res.data.success === true) {
        const targetPage = this.state.shareLinkList && this.state.shareLinkList.length === 1 && this.state.sharePage > 1
          ? this.state.sharePage - 1
          : this.state.sharePage;
        this.listShareLinks(targetPage);
        toaster.success(gettext('Successfully deleted 1 item.'));
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
    const { uploadPerPage, uploadSortBy, uploadSortOrder, uploadExpiredFilter } = this.state;
    seafileAPI.orgAdminListOrgUploadLinks(page, uploadPerPage, uploadSortBy, uploadSortOrder, uploadExpiredFilter).then(res => {
      const data = res.data;
      this.setState({
        uploadLinkList: data.upload_link_list,
        uploadPage: data.page || 1,
        uploadPageNext: Utils.hasNextPage(data.page || page, uploadPerPage, data.count),
        uploadLoading: false,
        uploadErrorMsg: '',
      });
    }).catch(error => {
      this.setState({
        uploadLoading: false,
        uploadErrorMsg: Utils.getErrorMsg(error, true)
      });
    });
  };

  deleteUploadLink = (token) => {
    seafileAPI.orgAdminDeleteOrgUploadLink(token).then(res => {
      if (res.data.success === true) {
        const targetPage = this.state.uploadLinkList && this.state.uploadLinkList.length === 1 && this.state.uploadPage > 1
          ? this.state.uploadPage - 1
          : this.state.uploadPage;
        this.listUploadLinks(targetPage);
        toaster.success(gettext('Successfully deleted 1 item.'));
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

  sortItems = (tab, sortBy) => {
    const sortByKey = tab === 'shareLinks' ? 'shareSortBy' : 'uploadSortBy';
    const sortOrderKey = tab === 'shareLinks' ? 'shareSortOrder' : 'uploadSortOrder';
    const pageKey = tab === 'shareLinks' ? 'sharePage' : 'uploadPage';
    this.setState({
      [pageKey]: 1,
      [sortByKey]: sortBy,
      [sortOrderKey]: this.state[sortOrderKey] === 'asc' ? 'desc' : 'asc'
    }, () => {
      if (tab === 'shareLinks') {
        this.listShareLinks(1);
      } else {
        this.listUploadLinks(1);
      }
    });
  };

  setExpiredFilter = (tab, expiredFilter) => {
    const filterKey = tab === 'shareLinks' ? 'shareExpiredFilter' : 'uploadExpiredFilter';
    const pageKey = tab === 'shareLinks' ? 'sharePage' : 'uploadPage';
    this.setState({
      [filterKey]: expiredFilter,
      [pageKey]: 1,
    }, () => {
      if (tab === 'shareLinks') {
        this.listShareLinks(1);
      } else {
        this.listUploadLinks(1);
      }
    });
  };

  resetPerPage = (tab, perPage) => {
    const perPageKey = tab === 'shareLinks' ? 'sharePerPage' : 'uploadPerPage';
    this.setState({
      [perPageKey]: perPage,
    }, () => {
      if (tab === 'shareLinks') {
        this.listShareLinks(1);
      } else {
        this.listUploadLinks(1);
      }
    });
  };

  renderExpiredFilters = (tab) => {
    const expiredFilter = tab === 'shareLinks' ? this.state.shareExpiredFilter : this.state.uploadExpiredFilter;

    return (
      <div className="d-flex align-items-center mb-2 flex-wrap">
        <span className="mr-2">{gettext('Expired')}</span>
        {['all', 'expired', 'not_expired'].map(expired => {
          const labels = {
            all: gettext('All'),
            expired: gettext('Expired'),
            not_expired: gettext('Not Expired')
          };
          const expiredSelected = expiredFilter === expired;
          return (
            <button
              key={`${tab}-expired-${expired}`}
              type="button"
              className={`btn btn-sm mr-2 mb-1 ${expiredSelected ? 'btn-primary' : 'btn-outline-secondary'}`}
              onClick={() => this.setExpiredFilter(tab, expired)}
            >
              {labels[expired]}
            </button>
          );
        })}
      </div>
    );
  };

  render() {
    const { currentTab, shareLinkList, uploadLinkList } = this.state;
    const isShare = currentTab === 'shareLinks';
    const linkList = isShare ? shareLinkList : uploadLinkList;
    const page = isShare ? this.state.sharePage : this.state.uploadPage;
    const pageNext = isShare ? this.state.sharePageNext : this.state.uploadPageNext;
    const perPage = isShare ? this.state.sharePerPage : this.state.uploadPerPage;
    const loading = isShare ? this.state.shareLoading : this.state.uploadLoading;
    const errorMsg = isShare ? this.state.shareErrorMsg : this.state.uploadErrorMsg;
    const sortBy = isShare ? this.state.shareSortBy : this.state.uploadSortBy;
    const sortOrder = isShare ? this.state.shareSortOrder : this.state.uploadSortOrder;
    const expiredFilter = isShare ? this.state.shareExpiredFilter : this.state.uploadExpiredFilter;
    const deleteLink = isShare ? this.deleteShareLink : this.deleteUploadLink;
    const onPageChange = isShare ? this.onSharePageChange : this.onUploadPageChange;
    const emptyTitle = expiredFilter === 'all'
      ? (isShare ? gettext('No share links') : gettext('No upload links'))
      : (isShare ? gettext('No share links match the current filter') : gettext('No upload links match the current filter'));
    const emptyDescription = expiredFilter === 'all'
      ? ''
      : gettext('Try changing the Expired filter to see other links.');

    return (
      <Fragment>
        <MainPanelTopbar />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <div className="cur-view-path tab-nav-container">
              <ul className="nav">
                <li className="nav-item">
                  <button type="button" className={`nav-link btn btn-link${currentTab === 'shareLinks' ? ' active' : ''}`} onClick={() => { this.switchTab('shareLinks'); }}>{gettext('Share Links')}</button>
                </li>
                <li className="nav-item">
                  <button type="button" className={`nav-link btn btn-link${currentTab === 'uploadLinks' ? ' active' : ''}`} onClick={() => { this.switchTab('uploadLinks'); }}>{gettext('Upload Links')}</button>
                </li>
              </ul>
            </div>
            <div className="cur-view-content">
              {this.renderExpiredFilters(currentTab)}
              <OrgLinksContent
                loading={loading}
                errorMsg={errorMsg}
                items={linkList}
                isShareLink={isShare}
                currentPage={page}
                perPage={perPage}
                hasNextPage={pageNext}
                emptyTitle={emptyTitle}
                emptyDescription={emptyDescription}
                sortBy={sortBy}
                sortOrder={sortOrder}
                getByPage={(targetPage) => onPageChange({ preventDefault() { } }, targetPage - page)}
                resetPerPage={(newPerPage) => this.resetPerPage(currentTab, newPerPage)}
                sortItems={(field) => this.sortItems(currentTab, field)}
                onDelete={deleteLink}
                onViewLink={this.openLinkDialog}
                isItemFreezed={this.state.isItemFreezed}
                onFreezedItem={this.onFreezedItem}
                onUnfreezedItem={this.onUnfreezedItem}
              />
            </div>
          </div>
        </div>
        {this.state.isShowLinkDialog && this.state.currentLink &&
          <ShareAdminLinkEnhanced
            link={this.state.currentLink.link || ''}
            password={this.state.currentLink.password || ''}
            hasPassword={this.state.currentLink.has_password === true}
            viewCount={this.state.currentLink.view_count || this.state.currentLink.view_cnt || 0}
            isShareLink={isShare}
            toggleDialog={this.toggleLinkDialog}
          />
        }
      </Fragment>
    );
  }
}

class OrgLinksContent extends React.Component {

  getPreviousPage = () => {
    this.props.getByPage(this.props.currentPage - 1);
  };

  getNextPage = () => {
    this.props.getByPage(this.props.currentPage + 1);
  };

  renderSortHeader = (field, label) => {
    const { sortBy, sortOrder, sortItems } = this.props;
    const initialSortIcon = <span className="fas fa-sort"></span>;
    const sortIcon = <span className={`fas ${sortOrder === 'asc' ? 'fa-caret-up' : 'fa-caret-down'}`}></span>;

    return (
      <button
        type="button"
        className="d-inline-block table-sort-op btn btn-link p-0 border-0 align-baseline"
        onClick={() => sortItems(field)}
      >
        {label} {sortBy === field ? sortIcon : initialSortIcon}
      </button>
    );
  };

  render() {
    const {
      loading,
      errorMsg,
      items,
      currentPage,
      hasNextPage,
      perPage,
      resetPerPage,
      isShareLink,
      emptyTitle,
      emptyDescription,
    } = this.props;

    if (loading) {
      return <Loading />;
    }

    if (errorMsg) {
      return <p className="error text-center">{errorMsg}</p>;
    }

    return (
      <Fragment>
        {!items.length ? (
          <EmptyTip>
            <h2>{emptyTitle}</h2>
            {emptyDescription ? <p>{emptyDescription}</p> : null}
          </EmptyTip>
        ) : (
          <Fragment>
            <table className="table-hover">
              <thead>
                <tr>
                  <th width="14%">{this.renderSortHeader('obj_name', gettext('Name'))}</th>
                  <th width="14%">{gettext('Repo')}</th>
                  <th width="12%">{gettext('Token')}</th>
                  <th width="12%">{gettext('Owner')}</th>
                  <th width="8%">{gettext('Status')}</th>
                  <th width="8%">{gettext('Protected')}</th>
                  <th width="13%">{this.renderSortHeader('ctime', gettext('Created At'))}</th>
                  <th width="8%">{this.renderSortHeader('view_cnt', gettext('Visits'))}</th>
                  <th width="7%">{gettext('Expired')}</th>
                  <th width="5%"></th>
                </tr>
              </thead>
              <tbody>
                {items.map((item, index) => (
                  <LinkItem
                    key={index}
                    link={item}
                    isShareLink={isShareLink}
                    isItemFreezed={this.props.isItemFreezed}
                    onFreezedItem={this.props.onFreezedItem}
                    onUnfreezedItem={this.props.onUnfreezedItem}
                    deleteLink={this.props.onDelete}
                    openLinkDialog={this.props.onViewLink}
                  />
                ))}
              </tbody>
            </table>
            <Paginator
              gotoPreviousPage={this.getPreviousPage}
              gotoNextPage={this.getNextPage}
              currentPage={currentPage}
              hasNextPage={hasNextPage}
              curPerPage={perPage}
              resetPerPage={resetPerPage}
            />
          </Fragment>
        )}
      </Fragment>
    );
  }
}

OrgLinksContent.propTypes = {
  loading: PropTypes.bool.isRequired,
  errorMsg: PropTypes.string.isRequired,
  items: PropTypes.array.isRequired,
  isShareLink: PropTypes.bool.isRequired,
  currentPage: PropTypes.number.isRequired,
  perPage: PropTypes.number.isRequired,
  hasNextPage: PropTypes.bool.isRequired,
  emptyTitle: PropTypes.string.isRequired,
  emptyDescription: PropTypes.string,
  getByPage: PropTypes.func.isRequired,
  resetPerPage: PropTypes.func.isRequired,
  sortBy: PropTypes.string.isRequired,
  sortOrder: PropTypes.string.isRequired,
  sortItems: PropTypes.func.isRequired,
  onDelete: PropTypes.func.isRequired,
  onViewLink: PropTypes.func.isRequired,
  isItemFreezed: PropTypes.bool.isRequired,
  onFreezedItem: PropTypes.func.isRequired,
  onUnfreezedItem: PropTypes.func.isRequired,
};

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
    const name = link.obj_name || link.name || link.path || '';
    const token = link.token || '';
    const createdTime = link.created_time || link.ctime || '';
    const viewCount = link.view_count !== undefined ? link.view_count : (link.view_cnt !== undefined ? link.view_cnt : 0);
    const effectiveStatus = link.status || (link.active === false ? 'inactive' : 'active');
    const href = siteRoot + 'org/useradmin/info/' + encodeURIComponent(ownerEmail) + '/';

    return (
      <tr className={this.state.highlight ? 'tr-highlight' : ''} onMouseEnter={this.onMouseEnter} onMouseLeave={this.onMouseLeave}>
        <td>{name}</td>
        <td>{link.repo_name || '--'}</td>
        <td>{token}</td>
        <td><a href={href}>{ownerName}</a></td>
        <td>
          <span className={effectiveStatus === 'inactive' ? 'badge badge-warning' : 'badge badge-success'}>
            {effectiveStatus === 'inactive' ? gettext('Inactive') : gettext('Active')}
          </span>
        </td>
        <td>
          <span className={link.has_password ? 'badge badge-warning' : 'badge badge-secondary'}>
            {link.has_password ? gettext('Yes') : gettext('No')}
          </span>
        </td>
        <td>{createdTime ? moment(createdTime).fromNow() : ''}</td>
        <td>{viewCount}</td>
        <td>
          <span className={link.is_expired ? 'badge badge-danger' : 'badge badge-secondary'}>
            {link.is_expired ? gettext('Expired') : gettext('No')}
          </span>
        </td>
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
                <DropdownItem onClick={this.props.openLinkDialog.bind(this, link)}>{gettext('View Link')}</DropdownItem>
                <DropdownItem onClick={deleteLink.bind(this, token)}>{gettext('Delete')}</DropdownItem>
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
