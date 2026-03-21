import React, { Component, Fragment } from 'react';
import { navigate } from '@gatsbyjs/reach-router';
import { seafileAPI } from '../../../utils/seafile-api';
import { gettext } from '../../../utils/constants';
import toaster from '../../../components/toast';
import { Utils } from '../../../utils/utils';
import LinksNav from './links-nav';
import MainPanelTopbar from '../main-panel-topbar';
import LinksContent from './links-table';


class UploadLinks extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      uploadLinkList: [],
      perPage: 25,
      currentPage: 1,
      hasNextPage: false,
      sortBy: '',
      sortOrder: 'asc',
      activeFilter: 'all',
      expiredFilter: 'all',
    };
    this.initPage = 1;
  }

  componentDidMount() {
    let urlParams = (new URL(window.location)).searchParams;
    const { currentPage, perPage, sortBy, sortOrder, activeFilter, expiredFilter } = this.state;
    this.setState({
      perPage: parseInt(urlParams.get('per_page') || perPage),
      currentPage: parseInt(urlParams.get('page') || currentPage),
      sortBy: urlParams.get('order_by') || sortBy,
      sortOrder: urlParams.get('direction') || sortOrder,
      activeFilter: urlParams.get('active') || urlParams.get('status') || activeFilter,
      expiredFilter: urlParams.get('expired') || expiredFilter
    }, () => {
      this.getUploadLinksByPage(this.state.currentPage);
    });
  }

  getUploadLinksByPage = (page) => {
    let { perPage, sortBy, sortOrder, activeFilter, expiredFilter } = this.state;
    const activeParam = activeFilter === 'all' ? 'all' : (activeFilter === 'active');
    const expiredParam = expiredFilter === 'all' ? 'all' : (expiredFilter === 'expired');
    seafileAPI.sysAdminListAllUploadLinks(page, perPage, sortBy, sortOrder, null, activeParam, expiredParam).then((res) => {
      this.setState({
        uploadLinkList: res.data.upload_link_list,
        loading: false,
        currentPage: page,
        hasNextPage: Utils.hasNextPage(page, perPage, res.data.count),
      });
    }).catch((error) => {
      this.setState({
        loading: false,
        errorMsg: Utils.getErrorMsg(error, true) // true: show login tip if 403
      });
    });
  };

  deleteUploadLink = (linkToken) => {
    seafileAPI.sysAdminDeleteUploadLink(linkToken).then(res => {
      const targetPage = this.state.uploadLinkList.length === 1 && this.state.currentPage > 1
        ? this.state.currentPage - 1
        : this.state.currentPage;
      this.getUploadLinksByPage(targetPage);
      toaster.success(gettext('Successfully deleted 1 item.'));
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  setUploadLinkActive = (linkToken, active) => {
    seafileAPI.sysAdminSetUploadLinkActive(linkToken, active).then(() => {
      const uploadLinkList = this.state.uploadLinkList.map(item => {
        if (item.token === linkToken) {
          item.active = active;
        }
        return item;
      });
      this.setState({ uploadLinkList });
      toaster.success(gettext('Edit succeeded'));
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  resetPerPage = (newPerPage) => {
    this.setState({
      perPage: newPerPage,
    }, () => this.getUploadLinksByPage(this.initPage));
  };

  sortItems = (sortBy) => {
    this.setState({
      currentPage: 1,
      sortBy: sortBy,
      sortOrder: this.state.sortOrder === 'asc' ? 'desc' : 'asc'
    }, () => {
      let url = new URL(location.href);
      let searchParams = new URLSearchParams(url.search);
      const { currentPage, sortBy, sortOrder } = this.state;
      searchParams.set('page', currentPage);
      searchParams.set('order_by', sortBy);
      searchParams.set('direction', sortOrder);
      searchParams.set('active', this.state.activeFilter);
      searchParams.set('expired', this.state.expiredFilter);
      url.search = searchParams.toString();
      navigate(url.toString());
      this.getUploadLinksByPage(currentPage);
    });
  };

  setActiveFilter = (activeFilter) => {
    this.setState({
      currentPage: 1,
      activeFilter,
    }, () => {
      let url = new URL(location.href);
      let searchParams = new URLSearchParams(url.search);
      searchParams.set('page', '1');
      searchParams.set('active', activeFilter);
      searchParams.set('expired', this.state.expiredFilter);
      searchParams.set('order_by', this.state.sortBy);
      searchParams.set('direction', this.state.sortOrder);
      url.search = searchParams.toString();
      navigate(url.toString());
      this.getUploadLinksByPage(1);
    });
  };

  setExpiredFilter = (expiredFilter) => {
    this.setState({
      currentPage: 1,
      expiredFilter,
    }, () => {
      let url = new URL(location.href);
      let searchParams = new URLSearchParams(url.search);
      searchParams.set('page', '1');
      searchParams.set('active', this.state.activeFilter);
      searchParams.set('expired', expiredFilter);
      searchParams.set('order_by', this.state.sortBy);
      searchParams.set('direction', this.state.sortOrder);
      url.search = searchParams.toString();
      navigate(url.toString());
      this.getUploadLinksByPage(1);
    });
  };

  render() {
    let { uploadLinkList, currentPage, perPage, hasNextPage } = this.state;
    return (
      <Fragment>
        <MainPanelTopbar {...this.props} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <LinksNav currentItem="uploadLinks" />
            <div className="cur-view-content">
              <LinksContent
                loading={this.state.loading}
                errorMsg={this.state.errorMsg}
                items={uploadLinkList}
                currentPage={currentPage}
                perPage={perPage}
                hasNextPage={hasNextPage}
                getByPage={this.getUploadLinksByPage}
                resetPerPage={this.resetPerPage}
                emptyTitle={gettext('No upload links')}
                enableSort={true}
                sortBy={this.state.sortBy}
                sortOrder={this.state.sortOrder}
                activeFilter={this.state.activeFilter}
                expiredFilter={this.state.expiredFilter}
                setActiveFilter={this.setActiveFilter}
                setExpiredFilter={this.setExpiredFilter}
                sortItems={this.sortItems}
                onDelete={this.deleteUploadLink}
                onToggleActive={this.setUploadLinkActive}
              />
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

export default UploadLinks;
