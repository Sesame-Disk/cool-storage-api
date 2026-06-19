import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import { gettext } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import { isFileSaving } from '../../utils/upload-finalization';
import UploadListItem from './upload-list-item';
import ForbidUploadListItem from './forbid-upload-list-item';

// The "active upload" the highlight and auto-scroll follow must be the file
// actually transferring bytes, not merely the first not-yet-saved file. A file
// in "Saving..." (server-side finalize) keeps isUploading() true while its last
// chunk awaits the server, so it has to be excluded or the scroll would stay
// pinned to it while a later file uploads visibly.
const isTransferringBytes = (file) => {
  return Boolean(file)
    && !file.isSaved
    && !file.error
    && typeof file.isUploading === 'function'
    && file.isUploading()
    && !isFileSaving(file);
};

export const findActiveUploadFile = (uploadFileList) => {
  const list = uploadFileList || [];
  return list.find(isTransferringBytes)
    || list.find(file => file && !file.isSaved && !file.error)
    || null;
};

const propTypes = {
  uploadBitrate: PropTypes.number.isRequired,
  totalProgress: PropTypes.number.isRequired,
  retryFileList: PropTypes.array.isRequired,
  uploadFileList: PropTypes.array.isRequired,
  forbidUploadFileList: PropTypes.array.isRequired,
  onCloseUploadDialog: PropTypes.func.isRequired,
  onCancelAllUploading: PropTypes.func.isRequired,
  onUploadCancel: PropTypes.func.isRequired,
  onUploadRetry: PropTypes.func.isRequired,
  onUploadRetryAll: PropTypes.func.isRequired,
  isUploading: PropTypes.bool.isRequired
};

class UploadProgressDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      isMinimized: false
    };
    this.contentRef = React.createRef();
    this.activeUploadRowRef = React.createRef();
  }

  componentDidMount() {
    this.scrollActiveUploadIntoView();
  }

  componentDidUpdate(prevProps, prevState) {
    // Only auto-scroll when the file actually being uploaded changes or when the
    // dialog is restored from minimized. onFileProgress rebuilds uploadFileList
    // on every chunk tick, so reacting to the array reference here would re-pin
    // the scroll to the active row continuously and fight the user's own scroll.
    const activeIdChanged = this.getActiveUploadId(prevProps.uploadFileList) !== this.getActiveUploadId(this.props.uploadFileList);
    const restoredFromMinimized = prevState.isMinimized && !this.state.isMinimized;
    if (activeIdChanged || restoredFromMinimized) {
      this.scrollActiveUploadIntoView();
    }
  }

  getActiveUploadId = (uploadFileList) => {
    const activeFile = findActiveUploadFile(uploadFileList);
    return activeFile ? activeFile.uniqueIdentifier : null;
  };
  onCancelAllUploading = () => {
    this.props.onCancelAllUploading();
  };

  onMinimizeUpload = (e) => {
    e.nativeEvent.stopImmediatePropagation();
    this.setState({ isMinimized: !this.state.isMinimized });
  };

  onCloseUpload = (e) => {
    e.nativeEvent.stopImmediatePropagation();
    this.props.onCloseUploadDialog();
  };

  scrollActiveUploadIntoView = () => {
    if (this.state.isMinimized) {
      return;
    }

    const container = this.contentRef.current;
    const row = this.activeUploadRowRef.current;
    if (!container || !row) {
      return;
    }

    // Measure with getBoundingClientRect instead of offsetTop: the active row is
    // a <tr> whose offsetParent is the <table>, not this scroll container, so
    // row.offsetTop - container.offsetTop mixes coordinate frames and collapses
    // to a negative value (forcing scrollTop to 0 and pinning the list at the
    // top). Rects are viewport-relative, so the difference plus the current
    // scrollTop gives the row's true position within the scrolled content.
    const containerRect = container.getBoundingClientRect();
    const rowRect = row.getBoundingClientRect();
    const rowTop = (rowRect.top - containerRect.top) + container.scrollTop;
    const rowBottom = rowTop + rowRect.height;
    const visibleTop = container.scrollTop;
    const visibleBottom = visibleTop + container.clientHeight;

    if (rowTop < visibleTop) {
      container.scrollTop = Math.max(0, rowTop - 8);
    } else if (rowBottom > visibleBottom) {
      container.scrollTop = rowBottom - container.clientHeight + 8;
    }
  };

  render() {
    const { totalProgress, retryFileList, uploadBitrate, uploadFileList, forbidUploadFileList, isUploading } = this.props;
    const activeUploadFile = findActiveUploadFile(uploadFileList);

    const filesUploadedMsg = gettext('{uploaded_files_num}/{all_files_num} Files')
      .replace('{uploaded_files_num}', uploadFileList.filter(file => file.isSaved).length)
      .replace('{all_files_num}', uploadFileList.length);

    let filesFailedMsg;
    if (!isUploading) {
      const failedNum = uploadFileList.filter(file => file.error).length + forbidUploadFileList.length;
      if (failedNum > 0) {
        filesFailedMsg = gettext('{failed_files_num} file(s) failed to upload')
          .replace('{failed_files_num}', failedNum);
      }
    }

    return (
      <div className="uploader-list-view mw-100" style={{ height: this.state.isMinimized ? document.querySelector('.uploader-list-header').offsetHeight : '20rem' }}>
        <div className="uploader-list-header flex-shrink-0">
          <div>
            {isUploading ? (
              <>
                <span>{gettext('File Uploading...')}</span>
                <span className="ml-2">{`${totalProgress}% (${Utils.formatBitRate(uploadBitrate)})`}</span>
                <div className="progress">
                  <div className="progress-bar" role="progressbar" style={{ width: `${totalProgress}%` }} aria-valuenow={totalProgress} aria-valuemin="0" aria-valuemax="100"></div>
                </div>
              </>
            ) : (
              <>
                {filesFailedMsg ?
                  <p className="m-0 error">{filesFailedMsg}</p> :
                  <p className="m-0">{gettext('All files uploaded')}</p>
                }
              </>
            )}
          </div>
          <div className="upload-dialog-op-container">
            <span className="sf2-icon-minus upload-dialog-op" onClick={this.onMinimizeUpload}></span>
            {!isUploading && <span className="sf2-icon-x1 upload-dialog-op" onClick={this.onCloseUpload}></span>}
          </div>
        </div>
        <div className="uploader-list-content" ref={this.contentRef}>
          <div className="d-flex justify-content-between align-items-center border-bottom">
            {uploadFileList.length > 0 && <span>{filesUploadedMsg}</span>}
            <div className="ml-auto">
              <button
                className="btn btn-lg border-0 background-transparent px-0"
                onClick={this.props.onUploadRetryAll}
                disabled={retryFileList.length === 0}
              >
                {gettext('Retry All')}
              </button>
              <button
                className="btn btn-lg border-0 background-transparent px-0 ml-3"
                onClick={this.props.onCancelAllUploading}
                disabled={!isUploading}
              >
                {gettext('Cancel All')}
              </button>
            </div>
          </div>
          <table className="table-thead-hidden">
            <thead>
              <tr>
                <th width="40%">{gettext('name')}</th>
                <th width="15%">{gettext('size')}</th>
                <th width="30%">{gettext('progress')}</th>
                <th width="15%">{gettext('state')}</th>
              </tr>
            </thead>
            <tbody>
              {
                this.props.forbidUploadFileList.map((file, index) => {
                  return (<ForbidUploadListItem key={index} file={file} />);
                })
              }
              {
                this.props.uploadFileList.map((resumableFile, index) => {
                  const isCurrentUpload = activeUploadFile && activeUploadFile.uniqueIdentifier === resumableFile.uniqueIdentifier;
                  return (
                    <UploadListItem
                      key={index}
                      resumableFile={resumableFile}
                      onUploadCancel={this.props.onUploadCancel}
                      onUploadRetry={this.props.onUploadRetry}
                      isCurrentUpload={isCurrentUpload}
                      rowRef={isCurrentUpload ? this.activeUploadRowRef : null}
                    />
                  );
                })
              }
            </tbody>
          </table>
        </div>
      </div>
    );
  }
}

UploadProgressDialog.propTypes = propTypes;

export default UploadProgressDialog;

