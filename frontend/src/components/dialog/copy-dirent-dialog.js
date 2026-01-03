import React from 'react';
import PropTypes from 'prop-types';
import { Button, Alert } from 'reactstrap';
import { gettext } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import FileChooser from '../file-chooser/file-chooser';

const propTypes = {
  path: PropTypes.string.isRequired,
  repoID: PropTypes.string.isRequired,
  dirent: PropTypes.object,
  selectedDirentList: PropTypes.array,
  isMutipleOperation: PropTypes.bool.isRequired,
  onItemCopy: PropTypes.func,
  onItemsCopy: PropTypes.func,
  onCancelCopy: PropTypes.func.isRequired,
  repoEncrypted: PropTypes.bool.isRequired,
};

// need dirent file Path；
class CopyDirent extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      repo: { repo_id: this.props.repoID },
      selectedPath: this.props.path,
      errMessage: ''
    };
  }

  shouldComponentUpdate(nextProps, nextState) {
    if (this.state.errMessage === nextState.errMessage) {
      return false;
    }
    return true;
  }

  handleSubmit = () => {
    if (this.props.isMutipleOperation) {
      this.copyItems();
    } else {
      this.copyItem();
    }
  };

  copyItems = () => {
    let { repo, selectedPath } = this.state;
    let message = gettext('Invalid destination path');

    if (!repo || selectedPath === '') {
      this.setState({errMessage: message});
      return;
    }

    let selectedDirentList = this.props.selectedDirentList;
    let direntPaths = [];
    selectedDirentList.forEach(dirent => {
      let path = Utils.joinPath(this.props.path, dirent.name);
      direntPaths.push(path);
    });

    // copy dirents to one of them. eg: A/B, A/C -> A/B
    if (direntPaths.some(direntPath => { return direntPath === selectedPath;})) {
      this.setState({errMessage: message});
      return;
    }

    // copy dirents to one of their child. eg: A/B, A/D -> A/B/C
    let copyDirentPath = '';
    let isChildPath = direntPaths.some(direntPath => {
      let flag = selectedPath.length > direntPath.length && selectedPath.indexOf(direntPath) > -1;
      if (flag) {
        copyDirentPath = direntPath;
      }
      return flag;
    });

    if (isChildPath) {
      message = gettext('Can not move directory %(src)s to its subdirectory %(des)s');
      message = message.replace('%(src)s', copyDirentPath);
      message = message.replace('%(des)s', selectedPath);
      this.setState({errMessage: message});
      return;
    }

    this.props.onItemsCopy(repo, selectedPath);
    this.toggle();
  };

  copyItem = () => {
    let { repo, repoID, selectedPath } = this.state;
    let direntPath = Utils.joinPath(this.props.path, this.props.dirent.name);
    let message = gettext('Invalid destination path');

    if (!repo || (repo.repo_id === repoID && selectedPath === '')) {
      this.setState({errMessage: message});
      return;
    }

    // copy the dirent to itself. eg: A/B -> A/B
    if (selectedPath && direntPath === selectedPath) {
      this.setState({errMessage: message});
      return;
    }

    // copy the dirent to it's child. eg: A/B -> A/B/C
    if ( selectedPath && selectedPath.length > direntPath.length && selectedPath.indexOf(direntPath) > -1) {
      message = gettext('Can not copy directory %(src)s to its subdirectory %(des)s');
      message = message.replace('%(src)s', direntPath);
      message = message.replace('%(des)s', selectedPath);
      this.setState({errMessage: message});
      return;
    }

    this.props.onItemCopy(repo, this.props.dirent, selectedPath, this.props.path);
    this.toggle();
  };

  toggle = () => {
    this.props.onCancelCopy();
  };

  onDirentItemClick = (repo, selectedPath) => {
    this.setState({
      repo: repo,
      selectedPath: selectedPath,
      errMessage: ''
    });
  };

  onRepoItemClick = (repo) => {
    this.setState({
      repo: repo,
      selectedPath: '/',
      errMessage: ''
    });
  };

  render() {
    let title = gettext('Copy {placeholder} to');
    if (!this.props.isMutipleOperation) {
      title = title.replace('{placeholder}', '<span class="op-target text-truncate mx-1">' + Utils.HTMLescape(this.props.dirent.name) + '</span>');
    } else {
      title = gettext('Copy selected item(s) to:');
    }
    let mode = 'current_repo_and_other_repos';
    const { isMutipleOperation } = this.props;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">
                {isMutipleOperation ? title : <span dangerouslySetInnerHTML={{__html: title}} className="d-flex mw-100"></span>}
              </h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              <FileChooser
                repoID={this.props.repoID}
                currentPath={this.props.path}
                onDirentItemClick={this.onDirentItemClick}
                onRepoItemClick={this.onRepoItemClick}
                mode={mode}
              />
              {this.state.errMessage && <Alert color="danger" className="mt-2">{this.state.errMessage}</Alert>}
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={this.toggle}>{gettext('Cancel')}</Button>
              <Button color="primary" onClick={this.handleSubmit}>{gettext('Submit')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

CopyDirent.propTypes = propTypes;

export default CopyDirent;
