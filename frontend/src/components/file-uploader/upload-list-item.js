import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import { gettext } from '../../utils/constants';
import { isFileSaving } from '../../utils/upload-finalization';
import { Utils } from '../../utils/utils';

const propTypes = {
  resumableFile: PropTypes.object.isRequired,
  onUploadCancel: PropTypes.func.isRequired,
  onUploadRetry: PropTypes.func.isRequired,
  isCurrentUpload: PropTypes.bool,
  rowRef: PropTypes.oneOfType([PropTypes.func, PropTypes.shape({ current: PropTypes.any })]),
};

const UPLOAD_UPLOADING = 'uploading';
const UPLOAD_ERROR = 'error';
const UPLOAD_ISSAVING = 'isSaving';
const UPLOAD_UPLOADED = 'uploaded';

class UploadListItem extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      uploadState: UPLOAD_UPLOADING
    };
  }

  UNSAFE_componentWillReceiveProps(nextProps) {
    let { resumableFile } = nextProps;
    let uploadState = UPLOAD_UPLOADING;

    if (resumableFile.error) {
      uploadState = UPLOAD_ERROR;
    } else {
      // isFinalizing: bytes fully transferred, waiting for the server to hash,
      // store blocks to S3, and commit metadata. remainingTime alone is not a
      // reliable signal because resumable.js keeps a non-zero ETA while the
      // last chunk's XHR is still pending.
      if (isFileSaving(resumableFile)) {
        uploadState = UPLOAD_ISSAVING;
      }

      if (resumableFile.isSaved) {
        uploadState = UPLOAD_UPLOADED;
      }
    }

    this.setState({ uploadState: uploadState });
  }

  onUploadCancel = (e) => {
    e.preventDefault();
    this.props.onUploadCancel(this.props.resumableFile);
  };

  onUploadRetry = (e) => {
    e.preventDefault();
    this.props.onUploadRetry(this.props.resumableFile);
  };

  // blockProgressText maps a block-upload entry's explicit phase to a label, so the
  // row reports the actual step ('Hashing…' / 'Checking…' / 'Uploading… X%' /
  // 'Saving…') instead of the legacy chunk/remainingTime heuristics, which a block
  // entry has no data for. The percent is the overall bar (hashing is its first
  // half), so the number and the bar always agree.
  blockProgressText = (resumableFile, progress) => {
    switch (resumableFile._phase) {
      case 'hashing':
        return gettext('Hashing...');
      case 'checking':
        return gettext('Checking...');
      case 'saving':
        return gettext('Saving...');
      case 'uploading':
      default:
        return progress === 0 ? gettext('Waiting...') : `${gettext('Uploading...')} ${progress}%`;
    }
  };

  // dedupNote surfaces the bytes already on the server (CAS dedup) so a fast repeat
  // upload is explained ("40.0 M already on server") instead of looking suspicious.
  // Rendered only once the /blocks/check plan is known and something was skipped.
  dedupNote = (resumableFile) => {
    if (!resumableFile.isBlockUpload || !(resumableFile._dedupedBytes > 0)) {
      return null;
    }
    return (
      <span className="dedup-note text-muted ml-2">
        {`${this.formatFileSize(resumableFile._dedupedBytes)} ${gettext('already on server')}`}
      </span>
    );
  };

  formatFileSize = (size) => {
    if (typeof size !== 'number') {
      return '';
    }
    if (size >= 1000 * 1000 * 1000) {
      return (size / (1000 * 1000 * 1000)).toFixed(1) + ' G';
    }
    if (size >= 1000 * 1000) {
      return (size / (1000 * 1000)).toFixed(1) + ' M';
    }
    if (size >= 1000) {
      return (size / 1000).toFixed(1) + ' K';
    }
    return size.toFixed(1) + ' B';
  };

  render() {
    let { resumableFile } = this.props;
    let progress = Math.round(resumableFile.progress() * 100);
    let error = resumableFile.error;
    const displayName = resumableFile.newFileName || resumableFile.fileName || '';

    return (
      <tr className={`file-upload-item${this.props.isCurrentUpload ? ' current-upload-item' : ''}`} ref={this.props.rowRef}>
        <td className="upload-name">
          <div className="ellipsis" title={displayName}>{displayName}</div>
        </td>
        <td>
          <span className="file-size">{this.formatFileSize(resumableFile.size)}</span>
        </td>
        <td className="upload-progress">
          {(this.state.uploadState === UPLOAD_UPLOADING || this.state.uploadState === UPLOAD_ISSAVING) &&
            <Fragment>
              {resumableFile.size >= (100 * 1000 * 1000) &&
                <Fragment>
                  {resumableFile.isUploading() && (
                    <div className="progress-container">
                      <div className="progress">
                        <div className="progress-bar" role="progressbar" style={{ width: `${progress}%` }} aria-valuenow={progress} aria-valuemin="0" aria-valuemax="100"></div>
                      </div>
                      {resumableFile.isBlockUpload ? (
                        // Block uploads have no chunk ETA; show the explicit phase
                        // ('Hashing…' / 'Checking…' / 'Uploading… X%' / 'Saving…')
                        // instead of a perpetual "Preparing to upload...".
                        <div className="progress-text">
                          {this.blockProgressText(resumableFile, progress)}
                          {this.dedupNote(resumableFile)}
                        </div>
                      ) : (
                        <Fragment>
                          {(resumableFile.remainingTime === -1) && <div className="progress-text">{gettext('Preparing to upload...')}</div>}
                          {(!isFileSaving(resumableFile) && resumableFile.remainingTime > 0) && <div className="progress-text">{gettext('Remaining')}{' '}{Utils.formatTime(resumableFile.remainingTime)}</div>}
                          {isFileSaving(resumableFile) && <div className="progress-text">{gettext('Saving...')}</div>}
                        </Fragment>
                      )}
                    </div>
                  )}
                  {!resumableFile.isUploading() && (
                    <div className="progress-container d-flex align-items-center">
                      <div className="progress">
                        <div className="progress-bar" role="progressbar" style={{ width: `${progress}%` }} aria-valuenow={progress} aria-valuemin="0" aria-valuemax="100"></div>
                      </div>
                      <div className="progress-text ml-2 mb-0">
                        {isFileSaving(resumableFile) ? gettext('Saving...') : (progress === 0 ? gettext('Waiting...') : `${gettext('Uploading...')} ${progress}%`)}
                      </div>
                    </div>
                  )}
                </Fragment>
              }
              {(resumableFile.size < (100 * 1000 * 1000)) && (
                <>
                  <div className="progress-container d-flex align-items-center">
                    <div className="progress">
                      <div className="progress-bar" role="progressbar" style={{ width: `${progress}%` }} aria-valuenow={progress} aria-valuemin="0" aria-valuemax="100"></div>
                    </div>
                  </div>
                  {resumableFile.isBlockUpload ? (
                    // Block entry: drive the label off the explicit phase, not the
                    // legacy uploadState (which only knows uploading vs saving).
                    <p className="progress-text mb-0">
                      {this.blockProgressText(resumableFile, progress)}
                      {this.dedupNote(resumableFile)}
                    </p>
                  ) : (
                    <>
                      {this.state.uploadState === UPLOAD_UPLOADING && (
                        <>
                          {progress === 0 && <p className="progress-text mb-0">{gettext('Waiting...')}</p>}
                          {progress > 0 && <p className="progress-text mb-0">{`${gettext('Uploading...')} ${progress}%`}</p>}
                        </>
                      )}
                      {this.state.uploadState === UPLOAD_ISSAVING && (
                        <p className="progress-text mb-0">{gettext('Saving...')}</p>
                      )}
                    </>
                  )}
                </>
              )}
            </Fragment>
          }
          {this.state.uploadState === UPLOAD_UPLOADED && (
            <div className="d-flex align-items-center">
              <span className="upload-success-icon sf2-icon-tick mr-2"></span>
              <span className="upload-success-msg">{gettext('Uploaded')}</span>
              {this.dedupNote(resumableFile)}
            </div>
          )}
          {this.state.uploadState === UPLOAD_ERROR && (
            <div className="d-flex align-items-center">
              <span className="upload-failure-icon fas fa-exclamation mr-2"></span>
              <span className="upload-failure-msg" dangerouslySetInnerHTML={{ __html: error }}></span>
            </div>
          )}
        </td>
        <td className="upload-operation">
          <Fragment>
            {this.state.uploadState === UPLOAD_UPLOADING && (
              <a href="#" onClick={this.onUploadCancel} role="button">{gettext('Cancel')}</a>
            )}
            {this.state.uploadState === UPLOAD_ERROR && (
              <a href="#" onClick={this.onUploadRetry} role="button">{gettext('Retry')}</a>
            )}
          </Fragment>
        </td>
      </tr>
    );
  }
}

UploadListItem.propTypes = propTypes;

export default UploadListItem;
