import React, { Component, Fragment } from 'react';
import { navigate } from '@gatsbyjs/reach-router';
import { seafileAPI } from '../../../utils/seafile-api';
import { gettext } from '../../../utils/constants';
import toaster from '../../../components/toast';
import { Utils } from '../../../utils/utils';
import LinksNav from './links-nav';
import MainPanelTopbar from '../main-panel-topbar';
import LinksContent from './links-table';
import Search from '../search';

class ShareLinks extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      shareLinkList: [],
      perPage: 25,
      currentPage: 1,
      hasNextPage: false,
      sortBy: '',
      sortOrder: 'asc',
      activeFilter: 'all',
      expiredFilter: 'all',
      search: ''
    };
    this.initPage = 1;
  }

  componentDidMount() {
    let urlParams = (new URL(window.location)).searchParams;
    const { currentPage, perPage, sortBy, sortOrder, activeFilter, expiredFilter, search } = this.state;
    this.setState({
      perPage: parseInt(urlParams.get('per_page') || perPage),
      currentPage: parseInt(urlParams.get('page') || currentPage),
      sortBy: urlParams.get('order_by') || sortBy,
      sortOrder: urlParams.get('direction') || sortOrder,
      activeFilter: urlParams.get('active') || urlParams.get('status') || activeFilter,
	      expiredFilter: urlParams.get('expired') || expiredFilter,
	      search: urlParams.get('search') || search
    }, () => {
      this.getShareLinksByPage(this.state.currentPage);
    });
  }

  updateURL = (overrides = {}) => {
    const url = new URL(window.location);
    const params = new URLSearchParams(url.search);
    const state = {
      page: this.state.currentPage,
      per_page: this.state.perPage,
      order_by: this.state.sortBy,
      direction: this.state.sortOrder,
      active: this.state.activeFilter,
      expired: this.state.expiredFilter,
      search: this.state.search,
      ...overrides,
    };

    const syncParam = (key, value, defaultValue) => {
      if (value === undefined || value === null || value === '' || value === defaultValue) {
        params.delete(key);
        return;
      }
      params.set(key, String(value));
    };

    syncParam('page', state.page, 1);
    syncParam('per_page', state.per_page, 25);
    syncParam('order_by', state.order_by, '');
    syncParam('direction', state.direction, 'asc');
    syncParam('active', state.active, 'all');
    syncParam('expired', state.expired, 'all');
    syncParam('search', state.search, '');

    url.search = params.toString();
    navigate(url.toString(), { replace: true });
  };

  getShareLinksByPage = (page) => {
    const { perPage, sortBy, sortOrder, activeFilter, expiredFilter, search } = this.state;
    const activeParam = activeFilter === 'all' ? 'all' : (activeFilter === 'active');
    const expiredParam = expiredFilter === 'all' ? 'all' : (expiredFilter === 'expired');
    this.updateURL({ page });
    seafileAPI.sysAdminListShareLinks(page, perPage, sortBy, sortOrder, null, activeParam, expiredParam, search).then((res) => {
      this.setState({
        shareLinkList: res.data.share_link_list,
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

  sortItems = (sortBy) => {
    this.setState({
      currentPage: 1,
      sortBy: sortBy,
      sortOrder: this.state.sortOrder === 'asc' ? 'desc' : 'asc'
    }, () => {
      this.getShareLinksByPage(this.state.currentPage);
    });
  };

  setActiveFilter = (activeFilter) => {
    this.setState({
      currentPage: 1,
      activeFilter,
    }, () => {
      this.getShareLinksByPage(1);
    });
  };

  setExpiredFilter = (expiredFilter) => {
    this.setState({
      currentPage: 1,
      expiredFilter,
    }, () => {
      this.getShareLinksByPage(1);
    });
  };

  submitSearch = (search) => {
    this.setState({
      currentPage: 1,
      search,
    }, () => {
      this.getShareLinksByPage(1);
    });
  };

  clearSearch = () => {
    if (!this.state.search) {
      return;
    }
    this.setState({
      currentPage: 1,
      search: '',
    }, () => {
      this.getShareLinksByPage(1);
    });
  };

  renderSearch = () => {
    return (
      <div className="d-flex align-items-center">
        <Search
          placeholder={gettext('Search share links')}
          submit={this.submitSearch}
          initialValue={this.state.search}
        />
        {this.state.search && (
          <button type="button" className="btn btn-outline-secondary btn-sm ml-2" onClick={this.clearSearch}>
            {gettext('Clear')}
          </button>
        )}
      </div>
    );
  };

  deleteShareLink = (linkToken) => {
    seafileAPI.sysAdminDeleteShareLink(linkToken).then(res => {
      const targetPage = this.state.shareLinkList.length === 1 && this.state.currentPage > 1
        ? this.state.currentPage - 1
        : this.state.currentPage;
      this.getShareLinksByPage(targetPage);
      toaster.success(gettext('Successfully deleted 1 item.'));
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  setShareLinkActive = (linkToken, active) => {
    seafileAPI.sysAdminSetShareLinkActive(linkToken, active).then(() => {
      const shareLinkList = this.state.shareLinkList.map(item => {
        if (item.token === linkToken) {
          item.active = active;
        }
        return item;
      });
      this.setState({ shareLinkList });
      toaster.success(gettext('Edit succeeded'));
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  resetPerPage = (newPerPage) => {
    this.setState({
      perPage: newPerPage,
    }, () => this.getShareLinksByPage(this.initPage));
  };

  render() {
    let { shareLinkList, currentPage, perPage, hasNextPage } = this.state;
    return (
      <Fragment>
        <MainPanelTopbar {...this.props} search={this.renderSearch()} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <LinksNav currentItem="shareLinks" />
            <div className="cur-view-content">
              <LinksContent
                loading={this.state.loading}
                errorMsg={this.state.errorMsg}
                items={shareLinkList}
                currentPage={currentPage}
                perPage={perPage}
                hasNextPage={hasNextPage}
                getByPage={this.getShareLinksByPage}
                resetPerPage={this.resetPerPage}
                emptyTitle={gettext('No share links')}
                enableSort={true}
                sortBy={this.state.sortBy}
                sortOrder={this.state.sortOrder}
                activeFilter={this.state.activeFilter}
                expiredFilter={this.state.expiredFilter}
                setActiveFilter={this.setActiveFilter}
                setExpiredFilter={this.setExpiredFilter}
                sortItems={this.sortItems}
                onDelete={this.deleteShareLink}
                onToggleActive={this.setShareLinkActive}
              />
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

export default ShareLinks;
