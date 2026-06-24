import React from 'react';
import PropTypes from 'prop-types';
import { gettext } from '../../utils/constants';
import { Button } from 'reactstrap';

const propTypes = {
  currentResumableFile: PropTypes.object.isRequired,
  replaceRepetitionFile: PropTypes.func.isRequired,
  uploadFile: PropTypes.func.isRequired,
  cancelFileUpload: PropTypes.func.isRequired,
  // When more than one file in the batch needs a decision, offer to apply the
  // chosen action (replace / don't replace / cancel) to every remaining duplicate
  // so the user is not prompted once per file.
  showApplyToAll: PropTypes.bool,
};

class UploadRemindDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      applyToAll: false,
    };
  }

  toggle = (e) => {
    e.nativeEvent.stopImmediatePropagation();
    this.props.cancelFileUpload(this.state.applyToAll);
  };

  replaceRepetitionFile = (e) => {
    e.nativeEvent.stopImmediatePropagation();
    this.props.replaceRepetitionFile(this.state.applyToAll);
  };

  uploadFile = (e) => {
    e.nativeEvent.stopImmediatePropagation();
    this.props.uploadFile(this.state.applyToAll);
  };

  onApplyToAllChange = (e) => {
    this.setState({ applyToAll: e.target.checked });
  };

  render() {
    const { fileName } = this.props.currentResumableFile;
    // zIndex sits above the upload progress panel (.uploader-list-view is 1050),
    // which renders later in the DOM and would otherwise overlap this modal.
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)', zIndex: 1060 }}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content">
        <div className="modal-header">
              <h5 className="modal-title"><span>{gettext('Replace file {filename}?').replace('{filename}', fileName)}</span></h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
        <div className="modal-body">
          <p>{gettext('A file with the same name already exists in this folder.')}</p>
          <p>{gettext('Replacing it will overwrite its content.')}</p>
          {this.props.showApplyToAll &&
            <div className="form-check">
              <input
                type="checkbox"
                className="form-check-input"
                id="upload-remind-apply-to-all"
                checked={this.state.applyToAll}
                onChange={this.onApplyToAllChange}
              />
              <label className="form-check-label" htmlFor="upload-remind-apply-to-all">
                {gettext('Apply to all duplicate files')}
              </label>
            </div>
          }
        </div>
        <div className="modal-footer">
          <Button color="primary" onClick={this.replaceRepetitionFile}>{gettext('Replace')}</Button>
          <Button color="primary" onClick={this.uploadFile}>{gettext('Don\'t replace')}</Button>
          <Button color="secondary" onClick={this.toggle}>{gettext('Cancel')}</Button>
        </div>
      </div>
          </div>
        </div>
    );
  }
}

UploadRemindDialog.propTypes = propTypes;

export default UploadRemindDialog;
