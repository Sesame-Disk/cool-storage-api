import React from 'react';
import PropTypes from 'prop-types';
import { InputGroup, InputGroupAddon, InputGroupText } from 'reactstrap';
import { gettext } from '../../../utils/constants';
import { parseGigabytesInput, quotaBytesToGigabyteInput } from '../../../utils/quota-units';
import { Utils } from '../../../utils/utils';

const propTypes = {
    quotaTotal: PropTypes.number,
    trafficUploadQuota: PropTypes.number,
    trafficDownloadQuota: PropTypes.number,
    orgStorageQuota: PropTypes.number,
    orgTrafficQuota: PropTypes.number,
    orgTrafficUploadQuota: PropTypes.number,
    orgTrafficDownloadQuota: PropTypes.number,
    toggleDialog: PropTypes.func.isRequired,
    updateQuota: PropTypes.func.isRequired,
};

class SetUserTrafficQuotasDialog extends React.Component {
    constructor(props) {
        super(props);
        this.state = {
            storageInputValue: quotaBytesToGigabyteInput(props.quotaTotal),
            uploadInputValue: quotaBytesToGigabyteInput(props.trafficUploadQuota),
            downloadInputValue: quotaBytesToGigabyteInput(props.trafficDownloadQuota),
            formErrorMsg: '',
            submitBtnDisabled: false,
        };
    }

    handleInputChange = (field) => (event) => {
        this.setState({
            [field]: event.target.value,
            formErrorMsg: '',
        });
    };

    formatOrgLimit = (quota) => {
        if (!quota || quota <= 0) return null;
        return Utils.bytesToSize(quota);
    };

    exceedsOrgLimit = (valueBytes, orgLimitBytes) => {
        return orgLimitBytes > 0 && valueBytes > 0 && valueBytes > orgLimitBytes;
    };

    renderField(label, value, fieldName, tip, orgLimit, orgCombinedLimit) {
        const limitStr = this.formatOrgLimit(orgLimit);
        const combinedStr = this.formatOrgLimit(orgCombinedLimit);
        return (
            <React.Fragment>
                <label className="mb-1">{label}</label>
                <InputGroup>
                    <input type="text" className="form-control" value={value} onChange={this.handleInputChange(fieldName)} />
                    <InputGroupAddon addonType="append">
                        <InputGroupText>GB</InputGroupText>
                    </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-3">
                    {tip}
                    {limitStr && ` — ${gettext('Organization limit')}: ${limitStr}`}
                    {!limitStr && combinedStr && ` — ${gettext('Organization combined limit')}: ${combinedStr}`}
                </p>
            </React.Fragment>
        );
    }

    formSubmit = () => {
        const { orgStorageQuota, orgTrafficQuota, orgTrafficUploadQuota, orgTrafficDownloadQuota } = this.props;
        const quotaTotal = parseGigabytesInput(this.state.storageInputValue, 0);
        const trafficUploadQuota = parseGigabytesInput(this.state.uploadInputValue, 0);
        const trafficDownloadQuota = parseGigabytesInput(this.state.downloadInputValue, 0);

        if (quotaTotal === null || trafficUploadQuota === null || trafficDownloadQuota === null) {
            this.setState({ formErrorMsg: gettext('Please enter a valid non-negative number.') });
            return;
        }

        if (this.exceedsOrgLimit(quotaTotal, orgStorageQuota)) {
            this.setState({ formErrorMsg: gettext('Storage quota exceeds organization limit.') });
            return;
        }
        if (this.exceedsOrgLimit(trafficUploadQuota, orgTrafficUploadQuota)) {
            this.setState({ formErrorMsg: gettext('Upload quota exceeds organization limit.') });
            return;
        }
        if (this.exceedsOrgLimit(trafficDownloadQuota, orgTrafficDownloadQuota)) {
            this.setState({ formErrorMsg: gettext('Download quota exceeds organization limit.') });
            return;
        }
        if (this.exceedsOrgLimit(trafficUploadQuota, orgTrafficQuota)) {
            this.setState({ formErrorMsg: gettext('Upload quota exceeds organization combined traffic limit.') });
            return;
        }
        if (this.exceedsOrgLimit(trafficDownloadQuota, orgTrafficQuota)) {
            this.setState({ formErrorMsg: gettext('Download quota exceeds organization combined traffic limit.') });
            return;
        }
        if (orgTrafficQuota > 0 && trafficUploadQuota > 0 && trafficDownloadQuota > 0 && trafficUploadQuota + trafficDownloadQuota > orgTrafficQuota) {
            this.setState({ formErrorMsg: gettext('Upload + download quota sum exceeds organization combined traffic limit.') });
            return;
        }

        this.setState({ submitBtnDisabled: true });
        this.props.updateQuota({
            quota_total: quotaTotal,
            traffic_upload_quota: trafficUploadQuota,
            traffic_download_quota: trafficDownloadQuota,
        }).then(() => {
            this.props.toggleDialog();
        }).catch((errorMsg) => {
            this.setState({
                formErrorMsg: errorMsg || gettext('Failed to update quota settings.'),
                submitBtnDisabled: false,
            });
        });
    };

    render() {
        const { storageInputValue, uploadInputValue, downloadInputValue, formErrorMsg, submitBtnDisabled } = this.state;

        return (
            <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0, 0, 0, 0.5)' }}>
                <div className="modal-dialog modal-dialog-centered">
                    <div className="modal-content">
                        <div className="modal-header">
                            <h5 className="modal-title">{gettext('Set user quotas')}</h5>
                            <button type="button" className="close" onClick={this.props.toggleDialog} aria-label="Close">
                                <span aria-hidden="true">&times;</span>
                            </button>
                        </div>
                        <div className="modal-body">
                            {this.renderField(gettext('Storage quota'), storageInputValue, 'storageInputValue', gettext('0 means the organization limit.'), this.props.orgStorageQuota, null)}
                            {this.renderField(gettext('Monthly upload quota'), uploadInputValue, 'uploadInputValue', gettext('0 means the organization limit.'), this.props.orgTrafficUploadQuota, this.props.orgTrafficQuota)}
                            {this.renderField(gettext('Monthly download quota'), downloadInputValue, 'downloadInputValue', gettext('0 means the organization limit.'), this.props.orgTrafficDownloadQuota, this.props.orgTrafficQuota)}
                            {formErrorMsg && <p className="error m-0 mt-2">{formErrorMsg}</p>}
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

SetUserTrafficQuotasDialog.propTypes = propTypes;

export default SetUserTrafficQuotasDialog;