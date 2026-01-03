import React from 'react';
import PropTypes from 'prop-types';
import moment from 'moment';
import { navigate } from '@gatsbyjs/reach-router';
import { Dropdown, DropdownToggle, DropdownMenu, DropdownItem } from 'reactstrap';
import { gettext, siteRoot, serviceURL } from '../../utils/constants';
import { seafileAPI, getToken } from '../../utils/seafile-api';
import Loading from '../../components/loading';
import toaster from '../../components/toast';

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
    };
    this.perPage = 25;
  }

  componentDidMount() {
    // Get file path from URL query parameter
    const urlParams = new URLSearchParams(window.location.search);
    const filePath = urlParams.get('p') || '/';
    const fileName = filePath.split('/').pop() || 'File';

    this.setState({ filePath, fileName });
    this.loadHistory(filePath, 1);
  }

  loadHistory = (filePath, page) => {
    const { repoID } = this.props;

    // Use seafileAPI if available, otherwise make direct API call
    if (seafileAPI.listFileHistoryRecords) {
      seafileAPI.listFileHistoryRecords(repoID, filePath, page, this.perPage).then(res => {
        this.handleHistoryResponse(res.data, page);
      }).catch(err => {
        this.handleError(err);
      });
    } else {
      // Direct API call as fallback
      this.fetchHistoryDirect(repoID, filePath, page);
    }
  };

  fetchHistoryDirect = (repoID, filePath, page) => {
    const token = getToken();
    const server = serviceURL || window.location.origin;

    fetch(`${server}/api2/repo/file_revisions/${repoID}/?p=${encodeURIComponent(filePath)}&page=${page}&per_page=${this.perPage}`, {
      headers: {
        'Authorization': `Token ${token}`,
      }
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
      repoName: data.repo_name || '',
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
    const { repoID } = this.props;
    const { filePath } = this.state;

    if (seafileAPI.revertFile) {
      seafileAPI.revertFile(repoID, filePath, item.commit_id).then(res => {
        toaster.success(gettext('Successfully restored.'));
        // Reload history
        this.setState({ isLoading: true });
        this.loadHistory(filePath, 1);
      }).catch(err => {
        toaster.danger(gettext('Failed to restore file.'));
      });
    } else {
      toaster.warning('Restore not available');
    }
  };

  onDownload = (item) => {
    const { repoID } = this.props;
    const { filePath } = this.state;
    const token = getToken();
    const server = serviceURL || window.location.origin;

    // Build download URL for historic file
    const downloadUrl = `${server}/api2/repos/${repoID}/file/?p=${encodeURIComponent(filePath)}&commit_id=${item.commit_id}`;
    window.open(downloadUrl);
  };

  onBackClick = (e) => {
    e.preventDefault();
    // Try to go back, or navigate to library
    if (window.history.length > 1) {
      window.history.back();
    } else {
      navigate(`${siteRoot}library/${this.props.repoID}/`);
    }
  };

  onScrollHandler = (e) => {
    const { clientHeight, scrollHeight, scrollTop } = e.target;
    const isBottom = (clientHeight + scrollTop + 1 >= scrollHeight);
    if (isBottom && this.state.hasMore) {
      this.loadMore();
    }
  };

  render() {
    const { isLoading, historyList, fileName, errorMsg, currentItem, isReloadingData, hasMore } = this.state;

    return (
      <div className="main-panel o-hidden">
        <div className="main-panel-center">
          <div className="cur-view-container">
            <div className="cur-view-path">
              <div className="d-flex align-items-center">
                <a href="#" onClick={this.onBackClick} className="go-back mr-2" title={gettext('Back')}>
                  <i className="fas fa-chevron-left"></i>
                </a>
                <h4 className="sf-heading m-0 text-truncate" title={fileName}>
                  {gettext('History')}: {fileName}
                </h4>
              </div>
            </div>
            <div className="cur-view-content">
              {isLoading && <Loading />}
              {errorMsg && (
                <div className="text-center mt-4">
                  <p className="text-danger">{errorMsg}</p>
                  <button className="btn btn-secondary" onClick={this.onBackClick}>
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
    const { item, index, isActive, onClick, onRestore, onDownload } = this.props;
    const { isMenuOpen } = this.state;

    const time = moment.unix(item.ctime).format('YYYY-MM-DD HH:mm');
    const creator = item.creator_name || item.creator_email || 'Unknown';
    const size = this.formatSize(item.size);

    return (
      <tr
        className={isActive ? 'table-active' : ''}
        onClick={onClick}
        style={{ cursor: 'pointer' }}
      >
        <td>{time}</td>
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
              {index !== 0 && (
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
  onRestore: PropTypes.func.isRequired,
  onDownload: PropTypes.func.isRequired,
};

export default FileHistory;
