import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import moment from 'moment';
import { gettext, siteRoot } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { Utils } from '../../utils/utils';
import Loading from '../../components/loading';
import Paginator from '../../components/paginator';
import CommonToolbar from '../../components/toolbar/common-toolbar';

class RepoHistoryView extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      isLoading: true,
      errorMsg: '',
      repoName: '',
      userPerm: 'rw',
      currentPage: 1,
      perPage: 25,
      hasNextPage: false,
      items: []
    };
  }

  componentDidMount() {
    const { repoID } = this.props;

    // Parse URL params
    let urlParams = (new URL(window.location)).searchParams;
    const perPage = parseInt(urlParams.get('per_page') || this.state.perPage);
    const currentPage = parseInt(urlParams.get('page') || this.state.currentPage);

    // Fetch library name
    seafileAPI.getRepoInfo(repoID).then(res => {
      this.setState({
        repoName: res.data.repo_name || res.data.name || 'Library',
        userPerm: res.data.permission || 'rw'
      });
    }).catch(() => {
      this.setState({ repoName: 'Library' });
    });

    this.setState({ perPage, currentPage }, () => {
      this.getItems(currentPage);
    });
  }

  getItems = (page) => {
    const { repoID } = this.props;
    seafileAPI.getRepoHistory(repoID, page, this.state.perPage).then((res) => {
      this.setState({
        isLoading: false,
        currentPage: page,
        items: res.data.data || [],
        hasNextPage: res.data.more || false
      });
    }).catch((error) => {
      this.setState({
        isLoading: false,
        errorMsg: Utils.getErrorMsg(error, true)
      });
    });
  };

  getPreviousPage = () => {
    this.getItems(this.state.currentPage - 1);
  };

  getNextPage = () => {
    this.getItems(this.state.currentPage + 1);
  };

  resetPerPage = (perPage) => {
    this.setState({ perPage }, () => {
      this.getItems(1);
    });
  };

  goBack = (e) => {
    e.preventDefault();
    window.history.back();
  };

  render() {
    const { repoID } = this.props;
    const { isLoading, errorMsg, items, repoName, userPerm, currentPage, hasNextPage, perPage } = this.state;

    return (
      <Fragment>
        <div className="main-panel-north border-left-show">
          <div className="cur-view-toolbar">
            <span className="sf2-icon-menu hidden-md-up d-md-none side-nav-toggle" title={gettext('Side Nav Menu')}></span>
            <div className="operation">
              <button className="btn btn-secondary operation-item" onClick={this.goBack}>
                <i className="sf2-icon-back mr-1"></i>{gettext('Back')}
              </button>
            </div>
          </div>
          <CommonToolbar onSearchedClick={Utils.handleSearchedItemClick} />
        </div>
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <div className="cur-view-path">
              <h3 className="sf-heading m-0">{repoName} {gettext('History')}</h3>
            </div>
            <div className="cur-view-content">
              {userPerm === 'rw' &&
                <p className="text-secondary mb-3">{gettext('Tip: a snapshot will be generated after modification, which records the library state after the modification.')}</p>
              }
              {isLoading ? (
                <Loading />
              ) : errorMsg ? (
                <p className="error mt-6 text-center">{errorMsg}</p>
              ) : (
                <Fragment>
                  <table className="table-hover">
                    <thead>
                      <tr>
                        <th width="43%">{gettext('Description')}</th>
                        <th width="15%">{gettext('Time')}</th>
                        <th width="15%">{gettext('Modifier')}</th>
                        <th width="15%">{`${gettext('Device')} / ${gettext('Version')}`}</th>
                        <th width="12%"></th>
                      </tr>
                    </thead>
                    <tbody>
                      {items.map((item, index) => (
                        <HistoryItem
                          key={index}
                          item={item}
                          repoID={repoID}
                          isFirstCommit={currentPage === 1 && index === 0}
                          userPerm={userPerm}
                        />
                      ))}
                    </tbody>
                  </table>
                  {items.length === 0 &&
                    <p className="text-center mt-4 text-secondary">{gettext('No history.')}</p>
                  }
                  <Paginator
                    gotoPreviousPage={this.getPreviousPage}
                    gotoNextPage={this.getNextPage}
                    currentPage={currentPage}
                    hasNextPage={hasNextPage}
                    curPerPage={perPage}
                    resetPerPage={this.resetPerPage}
                  />
                </Fragment>
              )}
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

RepoHistoryView.propTypes = {
  repoID: PropTypes.string.isRequired,
};

class HistoryItem extends React.Component {
  constructor(props) {
    super(props);
    this.state = { isIconShown: false };
  }

  handleMouseOver = () => this.setState({ isIconShown: true });
  handleMouseOut = () => this.setState({ isIconShown: false });

  render() {
    const { item, repoID, isFirstCommit, userPerm } = this.props;
    const { isIconShown } = this.state;

    let name = '';
    if (item.email) {
      if (!item.second_parent_id) {
        name = <a href={`${siteRoot}profile/${encodeURIComponent(item.email)}/`}>{item.name}</a>;
      } else {
        name = gettext('None');
      }
    } else {
      name = gettext('Unknown');
    }

    return (
      <tr onMouseOver={this.handleMouseOver} onMouseOut={this.handleMouseOut}>
        <td>{item.description}</td>
        <td title={moment(item.time).format('LLLL')}>{moment(item.time).format('YYYY-MM-DD')}</td>
        <td>{name}</td>
        <td>
          {item.client_version ? `${item.device_name} / ${item.client_version}` : 'API / --'}
        </td>
        <td>
          {userPerm === 'rw' && (
            isFirstCommit ?
              <span className={isIconShown ? '' : 'invisible'}>{gettext('Current Version')}</span> :
              <a href={`${siteRoot}repo/${repoID}/snapshot/?commit_id=${item.commit_id}`} className={isIconShown ? '' : 'invisible'}>{gettext('View Snapshot')}</a>
          )}
        </td>
      </tr>
    );
  }
}

HistoryItem.propTypes = {
  item: PropTypes.object.isRequired,
  repoID: PropTypes.string.isRequired,
  isFirstCommit: PropTypes.bool.isRequired,
  userPerm: PropTypes.string.isRequired,
};

export default RepoHistoryView;
