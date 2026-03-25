import React from 'react';
import PropTypes from 'prop-types';
import { InputGroup, InputGroupAddon, InputGroupText } from 'reactstrap';
import { gettext } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { Utils } from '../../utils/utils';

const propTypes = {
  orgID: PropTypes.string,
  email: PropTypes.string.isRequired,
  quotaTotal: PropTypes.oneOfType([PropTypes.string, PropTypes.number]).isRequired,
  trafficUploadQuota: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  trafficDownloadQuota: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  updateQuota: PropTypes.func.isRequired,
  toggleDialog: PropTypes.func.isRequired
};

const MB = 1000 * 1000;

const quotaBytesToInputValue = (quota) => {
  return quota > 0 ? String(Math.round(quota / MB)) : '';
};

class SetOrgUserQuota extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      storageInputValue: quotaBytesToInputValue(this.props.quotaTotal),
      uploadInputValue: quotaBytesToInputValue(this.props.trafficUploadQuota),
      downloadInputValue: quotaBytesToInputValue(this.props.trafficDownloadQuota),
      submitBtnDisabled: false
    };
  }

  handleInputChange = (field) => (e) => {
    this.setState({
      [field]: e.target.value
    });
  };

  parseMegabytes = (value, emptyValue) => {
    const trimmed = `${value || ''}`.trim();
    if (trimmed === '') {
      return emptyValue;
    }

    const parsed = Number(trimmed);
    if (!Number.isFinite(parsed) || parsed < 0) {
      return null;
    }

    return Math.round(parsed * MB);
  };

  formSubmit = () => {
    const { orgID, email } = this.props;
    const storageQuota = this.parseMegabytes(this.state.storageInputValue, 0);
    const uploadQuota = this.parseMegabytes(this.state.uploadInputValue, -1);
    const downloadQuota = this.parseMegabytes(this.state.downloadInputValue, -1);

    if (storageQuota === null || uploadQuota === null || downloadQuota === null) {
      this.setState({ formErrorMsg: gettext('Please enter a valid non-negative number.') });
      return false;
    }

    this.setState({
      submitBtnDisabled: true
    });

    seafileAPI.orgAdminUpdateOrgUserQuotas(orgID, email, {
      quotaTotal: storageQuota,
      trafficUploadQuota: uploadQuota,
      trafficDownloadQuota: downloadQuota,
    }).then((res) => {
      this.props.updateQuota({
        quota_total: res.data.quota_total,
        traffic_upload_quota: res.data.traffic_upload_quota,
        traffic_download_quota: res.data.traffic_download_quota,
      });
      this.props.toggleDialog();
    }).catch((error) => {
      let errorMsg = Utils.getErrorMsg(error);
      this.setState({
        formErrorMsg: errorMsg,
        submitBtnDisabled: false
      });
    });
  };

  render() {
    const { storageInputValue, uploadInputValue, downloadInputValue, formErrorMsg, submitBtnDisabled } = this.state;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('Set user quota')}</h5>
              <button type="button" className="close" onClick={this.props.toggleDialog} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              <React.Fragment>
                <label className="mb-1">{gettext('Storage quota')}</label>
                <InputGroup>
                  <input type="text" className="form-control" value={storageInputValue} onChange={this.handleInputChange('storageInputValue')} />
                  <InputGroupAddon addonType="append">
                    <InputGroupText>MB</InputGroupText>
                  </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-3">{gettext('Tip: 0 means default limit')}</p>

                <label className="mb-1">{gettext('Monthly upload quota')}</label>
                <InputGroup>
                  <input type="text" className="form-control" value={uploadInputValue} onChange={this.handleInputChange('uploadInputValue')} />
                  <InputGroupAddon addonType="append">
                    <InputGroupText>MB</InputGroupText>
                  </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-3">{gettext('Leave empty to inherit the organization limit.')}</p>

                <label className="mb-1">{gettext('Monthly download quota')}</label>
                <InputGroup>
                  <input type="text" className="form-control" value={downloadInputValue} onChange={this.handleInputChange('downloadInputValue')} />
                  <InputGroupAddon addonType="append">
                    <InputGroupText>MB</InputGroupText>
                  </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-2">{gettext('Leave empty to inherit the organization limit.')}</p>
                {formErrorMsg && <p className="error m-0 mt-2">{formErrorMsg}</p>}
              </React.Fragment>
            </div>
            <div className="modal-footer">
              <button className="btn btn-secondary" onClick={this.props.toggleDialog}>{gettext('Cancel')}</button>
              <button className="btn btn-primary" disabled={submitBtnDisabled} onClick={this.formSubmit}>{gettext('Submit')}</button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

SetOrgUserQuota.propTypes = propTypes;

export default SetOrgUserQuota;
