import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import moment from 'moment';
import { navigate, Link } from '@gatsbyjs/reach-router';
import { Dropdown, DropdownToggle, DropdownMenu, DropdownItem } from 'reactstrap';
import { gettext, siteRoot, serviceURL } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import Loading from '../../components/loading';
import toaster from '../../components/toast';
import ConflictDialog from '../../components/dialog/conflict-dialog';
import { buildHistoricFileViewURL } from '../../utils/file-view-url';

import '../../css/history-record-item.css';

const propTypes = {
  repoID: PropTypes.string.isRequired,
};

class FileHistory extends React.Component {
  constructor(props) {
    super(props);
    this.state = {
      isLoading: true,
      historyList: [],
      hasMore: false,
      currentPage: 1,
      isReloadingData: false,
      currentItem: null,
      filePath: '',
      fileName: '',
      repoName: '',
      errorMsg: '',
      showConflictDialog: false,
      conflictCommitID: '',
    };
    this.perPage = 25;
  }

  componentDidMount() {
    const urlParams = new URLSearchParams(window.location.search);
    const filePath = urlParams.get('p') || '/';
    const fileName = filePath.split('/').pop() || 'File';

    this.setState({ filePath, fileName });
    this.loadHistory(filePath, 1);
    this.loadRepoName();
  }

  loadRepoName = () => {
    const { repoID } = this.props;
    if (seafileAPI.getRepoInfo) {
      seafileAPI.getRepoInfo(repoID).then(res => {
        if (res.data && res.data.repo_name) {
          this.setState({ repoName: res.data.repo_name });
        }
      }).catch(() => { /* ignore - breadcrumb will use fallback */ });
    }
  };

  loadHistory = (filePath, page) => {
    const { repoID } = this.props;

    if (seafileAPI.listFileHistoryRecords) {
      seafileAPI.listFileHistoryRecords(repoID, filePath, page, this.perPage).then(res => {
        this.handleHistoryResponse(res.data, page);
      }).catch(err => {
        this.handleError(err);
      });
    } else {
      this.fetchHistoryDirect(repoID, filePath, page);
    }
  };

  fetchHistoryDirect = (repoID, filePath, page) => {
    const server = serviceURL || window.location.origin;

    fetch(`${server}/api2/repo/file_revisions/${repoID}/?p=${encodeURIComponent(filePath)}&page=${page}&per_page=${this.perPage}`, {
      credentials: 'same-origin'
    })
      .then(response => {
        if (!response.ok) throw new Error('Failed to fetch history');
        return response.json();
      })
      .then(data => {
        this.handleHistoryResponse(data, page);
      })
      .catch(err => {
        this.handleError(err);
      });
  };

  handleHistoryResponse = (data, page) => {
    const historyList = data.data || [];
    const totalCount = data.total_count || historyList.length;

    this.setState(prevState => ({
      isLoading: false,
      isReloadingData: false,
      historyList: page === 1 ? historyList : [...prevState.historyList, ...historyList],
      hasMore: totalCount > (this.perPage * page),
      currentPage: page,
      repoName: data.repo_name || prevState.repoName,
      currentItem: page === 1 && historyList.length > 0 ? historyList[0] : prevState.currentItem,
    }));
  };

  handleError = (err) => {
    console.error('Failed to load history:', err);
    this.setState({
      isLoading: false,
      isReloadingData: false,
      errorMsg: 'Failed to load file history',
    });
    toaster.danger('Failed to load file history');
  };

  loadMore = () => {
    if (!this.state.isReloadingData && this.state.hasMore) {
      const nextPage = this.state.currentPage + 1;
      this.setState({ isReloadingData: true, currentPage: nextPage });
      this.loadHistory(this.state.filePath, nextPage);
    }
  };

  onItemClick = (item) => {
    this.setState({ currentItem: item });
  };

  onItemRestore = (item) => {
    this.executeRestore(item.commit_id);
  };

  executeRestore = (commitID, conflictPolicy) => {
    const { repoID } = this.props;
    const { filePath } = this.state;

    if (!seafileAPI.revertFile) {
      toaster.warning('Restore not available');
      return;
    }

    seafileAPI.revertFile(repoID, filePath, commitID, conflictPolicy).then(res => {
      toaster.success(gettext('Successfully restored.'));
      this.setState({ isLoading: true, showConflictDialog: false, conflictCommitID: '' });
      this.loadHistory(filePath, 1);
    }).catch(err => {
      if (err.response && err.response.status === 409) {
        this.setState({ showConflictDialog: true, conflictCommitID: commitID });
      } else {
        toaster.danger(gettext('Failed to restore file.'));
      }
    });
  };

  closeConflictDialog = () => {
    this.setState({ showConflictDialog: false, conflictCommitID: '' });
  };

  handleConflictReplace = () => {
    this.executeRestore(this.state.conflictCommitID, 'replace');
  };

  handleConflictKeepBoth = () => {
    this.executeRestore(this.state.conflictCommitID, 'keep_both');
  };

  onView = (item) => {
    const { repoID } = this.props;
    const { filePath } = this.state;

    const viewUrl = buildHistoricFileViewURL({ repoID, filePath, objID: item.rev_file_id });
    window.open(viewUrl);
  };

  onDownload = (item) => {
    const { repoID } = this.props;
    const { filePath } = this.state;

    const params = `obj_id=${item.rev_file_id}&p=${encodeURIComponent(filePath)}`;
    const downloadUrl = `${siteRoot}repo/${repoID}/history/download?${params}`;
    window.open(downloadUrl);
  };

  onPathClick = (e) => {
    e.preventDefault();
    const path = e.currentTarget.getAttribute('data-path');
    if (path) {
      navigate(`${siteRoot}library/${this.props.repoID}${path}`);
    }
  };

  onScrollHandler = (e) => {
    const { clientHeight, scrollHeight, scrollTop } = e.target;
    const isBottom = (clientHeight + scrollTop + 1 >= scrollHeight);
    if (isBottom && this.state.hasMore) {
      this.loadMore();
    }
  };

  renderBreadcrumb = () => {
    const { filePath, repoName } = this.state;

    // Split path into segments: /folder/sub/file.txt → ['folder', 'sub', 'file.txt']
    const parts = filePath.split('/').filter(p => p);
    const folders = parts.slice(0, -1);
    const fileName = parts.length > 0 ? parts[parts.length - 1] : '';

    return (
      <div className="path-container">
        <Link to={`${siteRoot}my-libs/`} className="normal">{gettext('Libraries')}</Link>
        <span className="path-split">/</span>
        <button type="button" className="path-link border-0 bg-transparent p-0" data-path="/" onClick={this.onPathClick}>
          {repoName || gettext('Library')}
        </button>
        {folders.map((folder, i) => {
          const partialPath = '/' + parts.slice(0, i + 1).join('/') + '/';
          return (
            <Fragment key={i}>
              <span className="path-split">/</span>
              <button type="button" className="path-link border-0 bg-transparent p-0" data-path={partialPath} onClick={this.onPathClick}>
                {folder}
              </button>
            </Fragment>
          );
        })}
        {fileName && (
          <Fragment>
            <span className="path-split">/</span>
            <span className="path-file-name">{fileName}</span>
          </Fragment>
        )}
        <span className="path-split mx-2">|</span>
        <span className="text-secondary">{gettext('History')}</span>
      </div>
    );
  };

  render() {
    const { isLoading, historyList, errorMsg, currentItem, isReloadingData, hasMore, showConflictDialog } = this.state;

    return (
      <div className="main-panel o-hidden">
        <div className="main-panel-center">
          <div className="cur-view-container">
            <div className="cur-view-path">
              {this.renderBreadcrumb()}
            </div>
            <div className="cur-view-content">
              {isLoading && <Loading />}
              {errorMsg && (
                <div className="text-center mt-4">
                  <p className="text-danger">{errorMsg}</p>
                  <button className="btn btn-secondary" onClick={() => navigate(`${siteRoot}library/${this.props.repoID}/`)}>
                    {gettext('Go Back')}
                  </button>
                </div>
              )}
              {!isLoading && !errorMsg && historyList.length === 0 && (
                <div className="text-center mt-4">
                  <p>{gettext('No history available for this file.')}</p>
                </div>
              )}
              {!isLoading && !errorMsg && historyList.length > 0 && (
                <div className="file-history-container" style={{ height: 'calc(100vh - 180px)', overflowY: 'auto' }} onScroll={this.onScrollHandler}>
                  <table className="table table-hover">
                    <thead>
                      <tr>
                        <th>{gettext('Time')}</th>
                        <th>{gettext('Modifier')}</th>
                        <th>{gettext('Size')}</th>
                        <th style={{ width: '100px' }}>{gettext('Actions')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {historyList.map((item, index) => (
                        <HistoryItem
                          key={item.commit_id}
                          item={item}
                          index={index}
                          isActive={currentItem && currentItem.commit_id === item.commit_id}
                          onClick={() => this.onItemClick(item)}
                          onView={() => this.onView(item)}
                          onRestore={() => this.onItemRestore(item)}
                          onDownload={() => this.onDownload(item)}
                        />
                      ))}
                    </tbody>
                  </table>
                  {isReloadingData && <Loading />}
                  {!hasMore && historyList.length > 0 && (
                    <p className="text-center text-muted mt-2">{gettext('No more history')}</p>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>

        {showConflictDialog && (
          <ConflictDialog
            onReplace={this.handleConflictReplace}
            onKeepBoth={this.handleConflictKeepBoth}
            onCancel={this.closeConflictDialog}
          />
        )}
      </div>
    );
  }
}

FileHistory.propTypes = propTypes;

// Sub-component for history item row
class HistoryItem extends React.Component {
  constructor(props) {
    super(props);
    this.state = {
      isMenuOpen: false,
    };
  }

  toggleMenu = () => {
    this.setState(prevState => ({ isMenuOpen: !prevState.isMenuOpen }));
  };

  formatSize = (bytes) => {
    if (!bytes || bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
  };

  render() {
    const { item, index, isActive, onClick, onView, onRestore, onDownload } = this.props;
    const { isMenuOpen } = this.state;

    const time = moment.unix(item.ctime).format('YYYY-MM-DD HH:mm:ss');
    const creator = item.creator_name || item.creator_email || 'Unknown';
    const size = this.formatSize(item.size);
    const isCurrent = index === 0;

    return (
      <tr
        className={isActive ? 'table-active' : ''}
        onClick={onClick}
        style={{ cursor: 'pointer' }}
      >
        <td>
          {time}
          {isCurrent && <span className="text-secondary ml-2">({gettext('current version')})</span>}
        </td>
        <td>{creator}</td>
        <td>{size}</td>
        <td>
          <Dropdown isOpen={isMenuOpen} toggle={this.toggleMenu}>
            <DropdownToggle
              tag="button"
              className="btn btn-sm btn-secondary"
              data-toggle="dropdown"
            >
              <i className="fas fa-ellipsis-h"></i>
            </DropdownToggle>
            <DropdownMenu right>
              <DropdownItem onClick={(e) => { e.stopPropagation(); onView(); }}>
                <i className="fas fa-eye mr-2"></i>{gettext('View')}
              </DropdownItem>
              {!isCurrent && (
                <DropdownItem onClick={(e) => { e.stopPropagation(); onRestore(); }}>
                  <i className="fas fa-undo mr-2"></i>{gettext('Restore')}
                </DropdownItem>
              )}
              <DropdownItem onClick={(e) => { e.stopPropagation(); onDownload(); }}>
                <i className="fas fa-download mr-2"></i>{gettext('Download')}
              </DropdownItem>
            </DropdownMenu>
          </Dropdown>
        </td>
      </tr>
    );
  }
}

HistoryItem.propTypes = {
  item: PropTypes.object.isRequired,
  index: PropTypes.number.isRequired,
  isActive: PropTypes.bool.isRequired,
  onClick: PropTypes.func.isRequired,
  onView: PropTypes.func.isRequired,
  onRestore: PropTypes.func.isRequired,
  onDownload: PropTypes.func.isRequired,
};

export default FileHistory;
