import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { Utils } from '../../../utils/utils';
import { seafileAPI } from '../../../utils/seafile-api';
import { gettext } from '../../../utils/constants';
import toaster from '../../../components/toast';
import MainPanelTopbar from '../main-panel-topbar';
import LinksContent from '../links/links-table';
import Nav from './user-nav';

class Links extends Component {

  constructor(props) {
    super(props);
    this.state = {
      currentTab: 'shareLinks',
      userInfo: {},
      shareLoading: true,
      shareErrorMsg: '',
      shareLinkItems: [],
      sharePage: 1,
      sharePageNext: false,
      sharePerPage: 25,
      shareSortBy: '',
      shareSortOrder: 'desc',
      shareActiveFilter: 'all',
      shareExpiredFilter: 'all',
      uploadLoading: true,
      uploadErrorMsg: '',
      uploadLinkItems: [],
      uploadPage: 1,
      uploadPageNext: false,
      uploadPerPage: 25,
      uploadSortBy: '',
      uploadSortOrder: 'desc',
      uploadActiveFilter: 'all',
      uploadExpiredFilter: 'all'
    };
  }

  componentDidMount() {
    const email = decodeURIComponent(this.props.email);
    seafileAPI.sysAdminGetUser(email).then((res) => {
      this.setState({
        userInfo: res.data
      });
    });

    this.listShareLinks(1);
    this.listUploadLinks(1);
  }

  switchTab = (tab) => {
    this.setState({ currentTab: tab });
  };

  listShareLinks = (page) => {
    const email = decodeURIComponent(this.props.email);
    const { sharePerPage, shareSortBy, shareSortOrder, shareActiveFilter, shareExpiredFilter } = this.state;
    const activeParam = shareActiveFilter === 'all' ? 'all' : (shareActiveFilter === 'active');
    const expiredParam = shareExpiredFilter === 'all' ? 'all' : (shareExpiredFilter === 'expired');

    seafileAPI.sysAdminListShareLinksByUser(email, page, sharePerPage, shareSortBy, shareSortOrder, activeParam, expiredParam).then(res => {
      this.setState({
        shareLoading: false,
        shareErrorMsg: '',
        shareLinkItems: res.data.share_link_list || [],
        sharePage: page,
        sharePageNext: Utils.hasNextPage(page, sharePerPage, res.data.count || 0)
      });
    }).catch((error) => {
      this.setState({
        shareLoading: false,
        shareErrorMsg: Utils.getErrorMsg(error, true)
      });
    });
  };

  listUploadLinks = (page) => {
    const email = decodeURIComponent(this.props.email);
    const { uploadPerPage, uploadSortBy, uploadSortOrder, uploadActiveFilter, uploadExpiredFilter } = this.state;
    const activeParam = uploadActiveFilter === 'all' ? 'all' : (uploadActiveFilter === 'active');
    const expiredParam = uploadExpiredFilter === 'all' ? 'all' : (uploadExpiredFilter === 'expired');

    seafileAPI.sysAdminListUploadLinksByUser(email, page, uploadPerPage, uploadSortBy, uploadSortOrder, activeParam, expiredParam).then(res => {
      this.setState({
        uploadLoading: false,
        uploadErrorMsg: '',
        uploadLinkItems: res.data.upload_link_list || [],
        uploadPage: page,
        uploadPageNext: Utils.hasNextPage(page, uploadPerPage, res.data.count || 0)
      });
    }).catch((error) => {
      this.setState({
        uploadLoading: false,
        uploadErrorMsg: Utils.getErrorMsg(error, true)
      });
    });
  };

  sortItems = (tab, sortBy) => {
    const sortByKey = tab === 'shareLinks' ? 'shareSortBy' : 'uploadSortBy';
    const sortOrderKey = tab === 'shareLinks' ? 'shareSortOrder' : 'uploadSortOrder';
    this.setState({
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

  setActiveFilter = (tab, activeFilter) => {
    const filterKey = tab === 'shareLinks' ? 'shareActiveFilter' : 'uploadActiveFilter';
    this.setState({ [filterKey]: activeFilter }, () => {
      if (tab === 'shareLinks') {
        this.listShareLinks(1);
      } else {
        this.listUploadLinks(1);
      }
    });
  };

  setExpiredFilter = (tab, expiredFilter) => {
    const filterKey = tab === 'shareLinks' ? 'shareExpiredFilter' : 'uploadExpiredFilter';
    this.setState({ [filterKey]: expiredFilter }, () => {
      if (tab === 'shareLinks') {
        this.listShareLinks(1);
      } else {
        this.listUploadLinks(1);
      }
    });
  };

  resetPerPage = (tab, perPage) => {
    const key = tab === 'shareLinks' ? 'sharePerPage' : 'uploadPerPage';
    this.setState({ [key]: perPage }, () => {
      if (tab === 'shareLinks') {
        this.listShareLinks(1);
      } else {
        this.listUploadLinks(1);
      }
    });
  };

  getByPage = (tab, page) => {
    if (tab === 'shareLinks') {
      this.listShareLinks(page);
    } else {
      this.listUploadLinks(page);
    }
  };

  deleteShareLink = (token) => {
    seafileAPI.sysAdminDeleteShareLink(token).then(() => {
      const targetPage = this.state.shareLinkItems.length === 1 && this.state.sharePage > 1 ? this.state.sharePage - 1 : this.state.sharePage;
      this.listShareLinks(targetPage);
      toaster.success(gettext('Successfully deleted 1 item.'));
    }).catch((error) => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  deleteUploadLink = (token) => {
    seafileAPI.sysAdminDeleteUploadLink(token).then(() => {
      const targetPage = this.state.uploadLinkItems.length === 1 && this.state.uploadPage > 1 ? this.state.uploadPage - 1 : this.state.uploadPage;
      this.listUploadLinks(targetPage);
      toaster.success(gettext('Successfully deleted 1 item.'));
    }).catch((error) => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  setShareLinkActive = (token, active) => {
    seafileAPI.sysAdminSetShareLinkActive(token, active).then(() => {
      const shareLinkItems = this.state.shareLinkItems.map(item => item.token === token ? { ...item, active, status: active ? 'active' : 'inactive' } : item);
      this.setState({ shareLinkItems });
      toaster.success(gettext('Edit succeeded'));
    }).catch((error) => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  setUploadLinkActive = (token, active) => {
    seafileAPI.sysAdminSetUploadLinkActive(token, active).then(() => {
      const uploadLinkItems = this.state.uploadLinkItems.map(item => item.token === token ? { ...item, active, status: active ? 'active' : 'inactive' } : item);
      this.setState({ uploadLinkItems });
      toaster.success(gettext('Edit succeeded'));
    }).catch((error) => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  render() {
    const { currentTab } = this.state;
    const isShare = currentTab === 'shareLinks';
    const items = isShare ? this.state.shareLinkItems : this.state.uploadLinkItems;
    const loading = isShare ? this.state.shareLoading : this.state.uploadLoading;
    const errorMsg = isShare ? this.state.shareErrorMsg : this.state.uploadErrorMsg;
    const currentPage = isShare ? this.state.sharePage : this.state.uploadPage;
    const hasNextPage = isShare ? this.state.sharePageNext : this.state.uploadPageNext;
    const perPage = isShare ? this.state.sharePerPage : this.state.uploadPerPage;
    const sortBy = isShare ? this.state.shareSortBy : this.state.uploadSortBy;
    const sortOrder = isShare ? this.state.shareSortOrder : this.state.uploadSortOrder;
    const activeFilter = isShare ? this.state.shareActiveFilter : this.state.uploadActiveFilter;
    const expiredFilter = isShare ? this.state.shareExpiredFilter : this.state.uploadExpiredFilter;
    const onDelete = isShare ? this.deleteShareLink : this.deleteUploadLink;
    const onToggleActive = isShare ? this.setShareLinkActive : this.setUploadLinkActive;
    const emptyTitle = expiredFilter === 'all' && activeFilter === 'all'
      ? (isShare ? gettext('No share links') : gettext('No upload links'))
      : (isShare ? gettext('No share links match the current filter') : gettext('No upload links match the current filter'));

    return (
      <Fragment>
        <MainPanelTopbar {...this.props} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <Nav currentItem="links" email={this.props.email} userName={this.state.userInfo.name} />
            <div className="cur-view-content">
              <div className="tab-nav-container mb-4">
                <ul className="nav">
                  <li className="nav-item">
                    <button type="button" className={`nav-link btn btn-link${isShare ? ' active' : ''}`} onClick={() => this.switchTab('shareLinks')}>{gettext('Share Links')}</button>
                  </li>
                  <li className="nav-item">
                    <button type="button" className={`nav-link btn btn-link${!isShare ? ' active' : ''}`} onClick={() => this.switchTab('uploadLinks')}>{gettext('Upload Links')}</button>
                  </li>
                </ul>
              </div>
              <LinksContent
                loading={loading}
                errorMsg={errorMsg}
                items={items}
                isShareLink={isShare}
                currentPage={currentPage}
                perPage={perPage}
                hasNextPage={hasNextPage}
                getByPage={(page) => this.getByPage(currentTab, page)}
                resetPerPage={(value) => this.resetPerPage(currentTab, value)}
                emptyTitle={emptyTitle}
                enableSort={true}
                sortBy={sortBy}
                sortOrder={sortOrder}
                activeFilter={activeFilter}
                expiredFilter={expiredFilter}
                setActiveFilter={(value) => this.setActiveFilter(currentTab, value)}
                setExpiredFilter={(value) => this.setExpiredFilter(currentTab, value)}
                sortItems={(field) => this.sortItems(currentTab, field)}
                onDelete={onDelete}
                onToggleActive={onToggleActive}
              />
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

Links.propTypes = {
  email: PropTypes.string,
};

export default Links;
