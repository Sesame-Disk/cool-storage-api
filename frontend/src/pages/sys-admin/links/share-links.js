import React, { Component, Fragment } from 'react';
import { navigate } from '@gatsbyjs/reach-router';
import { seafileAPI } from '../../../utils/seafile-api';
import { gettext } from '../../../utils/constants';
import toaster from '../../../components/toast';
import { Utils } from '../../../utils/utils';
import LinksNav from './links-nav';
import MainPanelTopbar from '../main-panel-topbar';
import LinksContent from './links-table';

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
      expiredFilter: 'all'
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
      this.getShareLinksByPage(this.state.currentPage);
    });
  }

  getShareLinksByPage = (page) => {
    const { perPage, sortBy, sortOrder, activeFilter, expiredFilter } = this.state;
    const activeParam = activeFilter === 'all' ? 'all' : (activeFilter === 'active');
    const expiredParam = expiredFilter === 'all' ? 'all' : (expiredFilter === 'expired');
    seafileAPI.sysAdminListShareLinks(page, perPage, sortBy, sortOrder, null, activeParam, expiredParam).then((res) => {
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
      this.getShareLinksByPage(currentPage);
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
      this.getShareLinksByPage(1);
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
      this.getShareLinksByPage(1);
    });
  };

  deleteShareLink = (linkToken) => {
    seafileAPI.sysAdminDeleteShareLink(linkToken).then(res => {
      let newShareLinkList = this.state.shareLinkList.filter(item =>
        item.token !== linkToken
      );
      this.setState({ shareLinkList: newShareLinkList });
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
        <MainPanelTopbar {...this.props} />
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
