import React from 'react';
import PropTypes from 'prop-types';
import { InputGroup, InputGroupAddon, InputGroupText } from 'reactstrap';
import { gettext } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { parseGigabytesInput, quotaBytesToGigabyteInput } from '../../utils/quota-units';
import { Utils } from '../../utils/utils';

const propTypes = {
  orgID: PropTypes.string,
  email: PropTypes.string.isRequired,
  quotaTotal: PropTypes.oneOfType([PropTypes.string, PropTypes.number]).isRequired,
  trafficUploadQuota: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  trafficDownloadQuota: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  orgStorageQuota: PropTypes.number,
  orgTrafficQuota: PropTypes.number, // combined upload+download
  orgTrafficUploadQuota: PropTypes.number,
  orgTrafficDownloadQuota: PropTypes.number,
  updateQuota: PropTypes.func.isRequired,
  toggleDialog: PropTypes.func.isRequired
};

class SetOrgUserQuota extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      storageInputValue: quotaBytesToGigabyteInput(this.props.quotaTotal),
      uploadInputValue: quotaBytesToGigabyteInput(this.props.trafficUploadQuota),
      downloadInputValue: quotaBytesToGigabyteInput(this.props.trafficDownloadQuota),
      submitBtnDisabled: false
    };
  }

  handleInputChange = (field) => (e) => {
    this.setState({
      [field]: e.target.value
    });
  };

  exceedsOrgLimit = (valueBytes, orgLimitBytes) => {
    return orgLimitBytes > 0 && valueBytes > 0 && valueBytes > orgLimitBytes;
  };

  formSubmit = () => {
    const { orgID, email, orgStorageQuota, orgTrafficQuota, orgTrafficUploadQuota, orgTrafficDownloadQuota } = this.props;
    const storageQuota = parseGigabytesInput(this.state.storageInputValue, 0);
    const uploadQuota = parseGigabytesInput(this.state.uploadInputValue, -1);
    const downloadQuota = parseGigabytesInput(this.state.downloadInputValue, -1);

    if (storageQuota === null || uploadQuota === null || downloadQuota === null) {
      this.setState({ formErrorMsg: gettext('Please enter a valid non-negative number.') });
      return false;
    }

    if (this.exceedsOrgLimit(storageQuota, orgStorageQuota)) {
      this.setState({ formErrorMsg: gettext('Storage quota exceeds organization limit.') });
      return false;
    }
    if (this.exceedsOrgLimit(uploadQuota, orgTrafficUploadQuota)) {
      this.setState({ formErrorMsg: gettext('Upload quota exceeds organization limit.') });
      return false;
    }
    if (this.exceedsOrgLimit(downloadQuota, orgTrafficDownloadQuota)) {
      this.setState({ formErrorMsg: gettext('Download quota exceeds organization limit.') });
      return false;
    }
    // Combined traffic limit: each value and their sum.
    if (this.exceedsOrgLimit(uploadQuota, orgTrafficQuota)) {
      this.setState({ formErrorMsg: gettext('Upload quota exceeds organization combined traffic limit.') });
      return false;
    }
    if (this.exceedsOrgLimit(downloadQuota, orgTrafficQuota)) {
      this.setState({ formErrorMsg: gettext('Download quota exceeds organization combined traffic limit.') });
      return false;
    }
    if (orgTrafficQuota > 0 && uploadQuota > 0 && downloadQuota > 0 && uploadQuota + downloadQuota > orgTrafficQuota) {
      this.setState({ formErrorMsg: gettext('Upload + download quota sum exceeds organization combined traffic limit.') });
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

  formatOrgLimit = (quota) => {
    if (!quota || quota <= 0) return null;
    return Utils.bytesToSize(quota);
  };

  render() {
    const { storageInputValue, uploadInputValue, downloadInputValue, formErrorMsg, submitBtnDisabled } = this.state;
    const { orgStorageQuota, orgTrafficQuota, orgTrafficUploadQuota, orgTrafficDownloadQuota } = this.props;
    const storageLimitStr = this.formatOrgLimit(orgStorageQuota);
    const combinedLimitStr = this.formatOrgLimit(orgTrafficQuota);
    const uploadLimitStr = this.formatOrgLimit(orgTrafficUploadQuota);
    const downloadLimitStr = this.formatOrgLimit(orgTrafficDownloadQuota);
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
                    <InputGroupText>GB</InputGroupText>
                  </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-3">
                  {gettext('Tip: 0 means default limit')}
                  {storageLimitStr && ` — ${gettext('Organization limit')}: ${storageLimitStr}`}
                </p>

                <label className="mb-1">{gettext('Monthly upload quota')}</label>
                <InputGroup>
                  <input type="text" className="form-control" value={uploadInputValue} onChange={this.handleInputChange('uploadInputValue')} />
                  <InputGroupAddon addonType="append">
                    <InputGroupText>GB</InputGroupText>
                  </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-3">
                  {gettext('Leave empty to inherit the organization limit.')}
                  {uploadLimitStr && ` ${gettext('Organization limit')}: ${uploadLimitStr}`}
                  {!uploadLimitStr && combinedLimitStr && ` ${gettext('Organization combined limit')}: ${combinedLimitStr}`}
                </p>

                <label className="mb-1">{gettext('Monthly download quota')}</label>
                <InputGroup>
                  <input type="text" className="form-control" value={downloadInputValue} onChange={this.handleInputChange('downloadInputValue')} />
                  <InputGroupAddon addonType="append">
                    <InputGroupText>GB</InputGroupText>
                  </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-2">
                  {gettext('Leave empty to inherit the organization limit.')}
                  {downloadLimitStr && ` ${gettext('Organization limit')}: ${downloadLimitStr}`}
                  {!downloadLimitStr && combinedLimitStr && ` ${gettext('Organization combined limit')}: ${combinedLimitStr}`}
                </p>
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
