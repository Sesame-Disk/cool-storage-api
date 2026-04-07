import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import MediaQuery from 'react-responsive';
import { gettext, storages } from '../../utils/constants';
import MylibRepoListItem from './mylib-repo-list-item';
import LibsMobileThead from '../../components/libs-mobile-thead';

const propTypes = {
  sortBy: PropTypes.string.isRequired,
  sortOrder: PropTypes.string.isRequired,
  repoList: PropTypes.array.isRequired,
  sortRepoList: PropTypes.func.isRequired,
  onRenameRepo: PropTypes.func.isRequired,
  onDeleteRepo: PropTypes.func.isRequired,
  onTransferRepo: PropTypes.func.isRequired,
  onRepoClick: PropTypes.func.isRequired,
  onMonitorRepo: PropTypes.func.isRequired,
  // Selection props for batch operations
  selectedRepos: PropTypes.array,
  isAllSelected: PropTypes.bool,
  onSelectRepo: PropTypes.func,
  onSelectAllRepos: PropTypes.func,
  isRepoSelected: PropTypes.func,
};

class MylibRepoListView extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      isItemFreezed: false,
    };
  }

  onFreezedItem = () => {
    this.setState({ isItemFreezed: true });
  };

  onUnfreezedItem = () => {
    this.setState({ isItemFreezed: false });
  };

  sortByName = (e) => {
    e.preventDefault();
    const sortBy = 'name';
    const sortOrder = this.props.sortOrder === 'asc' ? 'desc' : 'asc';
    this.props.sortRepoList(sortBy, sortOrder);
  };

  sortByTime = (e) => {
    e.preventDefault();
    const sortBy = 'time';
    const sortOrder = this.props.sortOrder === 'asc' ? 'desc' : 'asc';
    this.props.sortRepoList(sortBy, sortOrder);
  };

  sortBySize = (e) => {
    e.preventDefault();
    const sortBy = 'size';
    const sortOrder = this.props.sortOrder === 'asc' ? 'desc' : 'asc';
    this.props.sortRepoList(sortBy, sortOrder);
  };

  onSelectAllChange = (e) => {
    if (this.props.onSelectAllRepos) {
      this.props.onSelectAllRepos(e.target.checked);
    }
  };

  renderRepoListView = () => {
    return (
      <Fragment>
        {this.props.repoList.map(item => {
          const isSelected = this.props.isRepoSelected ? this.props.isRepoSelected(item) : false;
          return (
            <MylibRepoListItem
              key={item.repo_id}
              repo={item}
              isItemFreezed={this.state.isItemFreezed}
              isSelected={isSelected}
              onSelectRepo={this.props.onSelectRepo}
              onFreezedItem={this.onFreezedItem}
              onUnfreezedItem={this.onUnfreezedItem}
              onRenameRepo={this.props.onRenameRepo}
              onDeleteRepo={this.props.onDeleteRepo}
              onTransferRepo={this.props.onTransferRepo}
              onMonitorRepo={this.props.onMonitorRepo}
              onRepoClick={this.props.onRepoClick}
            />
          );
        })}
      </Fragment>
    );
  };

  renderPCUI = () => {
    const showStorageBackend = storages.length > 0;
    const hasSelection = this.props.onSelectRepo !== undefined;
    const sortIcon = this.props.sortOrder === 'asc' ? <span className="fas fa-caret-up"></span> : <span className="fas fa-caret-down"></span>;
    return (
      <table>
        <thead>
          <tr>
            {hasSelection && (
              <th width="3%" className="text-center">
                <input
                  type="checkbox"
                  checked={this.props.isAllSelected || false}
                  onChange={this.onSelectAllChange}
                  title={gettext('Select all')}
                />
              </th>
            )}
            <th width={hasSelection ? '3%' : '4%'}></th>
            <th width="4%"><span className="sr-only">{gettext('Library Type')}</span></th>
            <th width={showStorageBackend ? '30%' : '35%'}><button type="button" className="d-block table-sort-op bg-transparent border-0 p-0 text-left w-100" onClick={this.sortByName}>{gettext('Name')} {this.props.sortBy === 'name' && sortIcon}</button></th>
            <th width="14%"><span className="sr-only">{gettext('Actions')}</span></th>
            <th width={showStorageBackend ? '15%' : '20%'}><button type="button" className="d-block table-sort-op bg-transparent border-0 p-0 text-left w-100" onClick={this.sortBySize}>{gettext('Size')} {this.props.sortBy === 'size' && sortIcon}</button></th>
            {showStorageBackend ? <th width="15%">{gettext('Storage Backend')}</th> : null}
            <th width={showStorageBackend ? '15%' : '20%'}><button type="button" className="d-block table-sort-op bg-transparent border-0 p-0 text-left w-100" onClick={this.sortByTime}>{gettext('Last Update')} {this.props.sortBy === 'time' && sortIcon}</button></th>
          </tr>
        </thead>
        <tbody>
          {this.renderRepoListView()}
        </tbody>
      </table>
    );
  };

  renderMobileUI = () => {
    return (
      <table className="table-thead-hidden">
        <LibsMobileThead />
        <tbody>
          {this.renderRepoListView()}
        </tbody>
      </table>
    );
  };

  render() {
    return (
      <Fragment>
        <MediaQuery query="(min-width: 768px)">
          {this.renderPCUI()}
        </MediaQuery>
        <MediaQuery query="(max-width: 767.8px)">
          {this.renderMobileUI()}
        </MediaQuery>
      </Fragment>
    );
  }
}

MylibRepoListView.propTypes = propTypes;

export default MylibRepoListView;
