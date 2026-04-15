import React from 'react';
import PropTypes from 'prop-types';
import moment from 'moment';
import { Dropdown, DropdownToggle, DropdownMenu, DropdownItem } from 'reactstrap';
import { gettext, siteRoot, serviceURL } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { Utils } from '../../utils/utils';
import { buildHistoricFileViewURL } from '../../utils/file-view-url';
import Loading from '../loading';
import toaster from '../toast';
import ConflictDialog from '../dialog/conflict-dialog';

const propTypes = {
  repoID: PropTypes.string.isRequired,
  filePath: PropTypes.string.isRequired,
};

class FileHistoryPanel extends React.Component {
  constructor(props) {
    super(props);
    this.state = {
      isLoading: true,
      historyList: [],
      hasMore: false,
      currentPage: 1,
      isLoadingMore: false,
      errorMsg: '',
      showConflictDialog: false,
      conflictCommitID: '',
    };
    this.perPage = 25;
  }

  componentDidMount() {
    this.loadHistory(1);
  }

  componentDidUpdate(prevProps) {
    if (prevProps.filePath !== this.props.filePath || prevProps.repoID !== this.props.repoID) {
      this.setState({ isLoading: true, historyList: [], currentPage: 1 });
      this.loadHistory(1);
    }
  }

  loadHistory = (page) => {
    const { repoID, filePath } = this.props;

    if (seafileAPI.listFileHistoryRecords) {
      seafileAPI.listFileHistoryRecords(repoID, filePath, page, this.perPage).then(res => {
        this.handleResponse(res.data, page);
      }).catch(err => {
        this.handleError(err);
      });
    } else {
      this.fetchDirect(repoID, filePath, page);
    }
  };

  fetchDirect = (repoID, filePath, page) => {
    const server = serviceURL || window.location.origin;

    fetch(`${server}/api2/repo/file_revisions/${repoID}/?p=${encodeURIComponent(filePath)}&page=${page}&per_page=${this.perPage}`, {
      credentials: 'same-origin'
    })
      .then(response => {
        if (!response.ok) throw new Error('Failed to fetch history');
        return response.json();
      })
      .then(data => {
        this.handleResponse(data, page);
      })
      .catch(err => {
        this.handleError(err);
      });
  };

  handleResponse = (data, page) => {
    const historyList = data.data || [];
    const totalCount = data.total_count || historyList.length;

    this.setState(prevState => ({
      isLoading: false,
      isLoadingMore: false,
      historyList: page === 1 ? historyList : [...prevState.historyList, ...historyList],
      hasMore: totalCount > (this.perPage * page),
      currentPage: page,
    }));
  };

  handleError = (err) => {
    console.error('Failed to load history:', err);
    this.setState({
      isLoading: false,
      isLoadingMore: false,
      errorMsg: gettext('Failed to load file history'),
    });
  };

  onScroll = (e) => {
    const { clientHeight, scrollHeight, scrollTop } = e.target;
    const isBottom = (clientHeight + scrollTop + 1 >= scrollHeight);
    if (isBottom && this.state.hasMore && !this.state.isLoadingMore) {
      const nextPage = this.state.currentPage + 1;
      this.setState({ isLoadingMore: true, currentPage: nextPage });
      this.loadHistory(nextPage);
    }
  };

  onView = (item) => {
    const { repoID, filePath } = this.props;

    const viewUrl = buildHistoricFileViewURL({ repoID, filePath, objID: item.rev_file_id });
    window.open(viewUrl);
  };

  onRestore = (item) => {
    this.executeRestore(item.commit_id);
  };

  executeRestore = (commitID, conflictPolicy) => {
    const { repoID, filePath } = this.props;

    if (!seafileAPI.revertFile) {
      toaster.warning(gettext('Restore not available'));
      return;
    }

    seafileAPI.revertFile(repoID, filePath, commitID, conflictPolicy).then(() => {
      toaster.success(gettext('Successfully restored.'));
      this.setState({ isLoading: true, historyList: [], showConflictDialog: false, conflictCommitID: '' });
      this.loadHistory(1);
    }).catch((err) => {
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

  onDownload = (item) => {
    const { repoID, filePath } = this.props;

    if (item.rev_file_id) {
      // Use the history download endpoint with the FS object ID
      const params = `obj_id=${item.rev_file_id}&p=${encodeURIComponent(filePath)}`;
      const downloadUrl = `${siteRoot}repo/${repoID}/history/download?${params}`;
      window.open(downloadUrl);
      return;
    }

    // Fallback: fetch download URL via API (for items without rev_file_id)
    const server = serviceURL || window.location.origin;
    const apiUrl = `${server}/api2/repos/${repoID}/file/?p=${encodeURIComponent(filePath)}`;

    fetch(apiUrl, {
      credentials: 'same-origin'
    })
      .then(response => {
        if (!response.ok) throw new Error('Failed to get download link');
        return response.text();
      })
      .then(downloadUrl => {
        const url = downloadUrl.replace(/"/g, '').trim();
        window.location.href = url;
      })
      .catch(() => {
        toaster.danger(gettext('Failed to download file.'));
      });
  };

  render() {
    const { repoID, filePath } = this.props;
    const { isLoading, historyList, errorMsg, isLoadingMore, hasMore, showConflictDialog } = this.state;

    if (isLoading) {
      return <div className="history-panel"><Loading /></div>;
    }

    if (errorMsg) {
      return <div className="history-panel"><p className="text-center text-danger mt-4">{errorMsg}</p></div>;
    }

    if (historyList.length === 0) {
      return <div className="history-panel"><p className="text-center text-secondary mt-4">{gettext('No history available')}</p></div>;
    }

    const fullHistoryUrl = `${siteRoot}repo/file_revisions/${repoID}/?p=${encodeURIComponent(filePath)}`;

    return (
      <div className="history-panel" onScroll={this.onScroll}>
        {historyList.map((item, index) => (
          <HistoryRecord
            key={item.commit_id + '-' + index}
            item={item}
            index={index}
            onView={this.onView}
            onRestore={this.onRestore}
            onDownload={this.onDownload}
          />
        ))}
        {isLoadingMore && <Loading />}
        {!hasMore && historyList.length > 0 && (
          <p className="text-center text-secondary mt-2 mb-2" style={{ fontSize: '13px' }}>{gettext('No more history')}</p>
        )}
        <div className="text-center mt-2 mb-3">
          <a href={fullHistoryUrl} className="text-primary" style={{ fontSize: '13px' }}>
            {gettext('View all history')}
          </a>
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

FileHistoryPanel.propTypes = propTypes;

class HistoryRecord extends React.Component {
  constructor(props) {
    super(props);
    this.state = { isMenuOpen: false };
  }

  toggleMenu = () => {
    this.setState(prevState => ({ isMenuOpen: !prevState.isMenuOpen }));
  };

  render() {
    const { item, index, onView, onRestore, onDownload } = this.props;
    const { isMenuOpen } = this.state;

    const timeStr = moment.unix(item.ctime).fromNow();
    const creator = item.creator_name || item.creator_email || 'Unknown';
    const size = item.size ? Utils.bytesToSize(item.size) : '';

    return (
      <div className="history-record">
        <div className="history-record-top">
          <span className="history-record-time" title={moment.unix(item.ctime).format('YYYY-MM-DD HH:mm:ss')}>
            {timeStr}
            {index === 0 && <span className="text-secondary ml-1">({gettext('current version')})</span>}
          </span>
          <Dropdown isOpen={isMenuOpen} toggle={this.toggleMenu} size="sm">
            <DropdownToggle tag="span" className="history-record-menu-toggle" data-toggle="dropdown">
              <i className="fas fa-ellipsis-h"></i>
            </DropdownToggle>
            <DropdownMenu right>
              <DropdownItem onClick={() => onView(item)}>
                <i className="fas fa-eye mr-2"></i>{gettext('View')}
              </DropdownItem>
              {index !== 0 && (
                <DropdownItem onClick={() => onRestore(item)}>
                  <i className="fas fa-undo mr-2"></i>{gettext('Restore')}
                </DropdownItem>
              )}
              <DropdownItem onClick={() => onDownload(item)}>
                <i className="fas fa-download mr-2"></i>{gettext('Download')}
              </DropdownItem>
            </DropdownMenu>
          </Dropdown>
        </div>
        <div className="history-record-bottom">
          <span className="history-record-creator text-truncate" title={creator}>{creator}</span>
          {size && <span className="history-record-size">{size}</span>}
        </div>
      </div>
    );
  }
}

HistoryRecord.propTypes = {
  item: PropTypes.object.isRequired,
  index: PropTypes.number.isRequired,
  onView: PropTypes.func.isRequired,
  onRestore: PropTypes.func.isRequired,
  onDownload: PropTypes.func.isRequired,
};

export default FileHistoryPanel;
