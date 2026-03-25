import React from 'react';
import PropTypes from 'prop-types';
import { InputGroup, InputGroupAddon, InputGroupText } from 'reactstrap';
import { gettext } from '../../../utils/constants';

const MB = 1000 * 1000;

const propTypes = {
    quotaTotal: PropTypes.number,
    trafficUploadQuota: PropTypes.number,
    trafficDownloadQuota: PropTypes.number,
    toggleDialog: PropTypes.func.isRequired,
    updateQuota: PropTypes.func.isRequired,
};

const quotaBytesToInputValue = (quota) => {
    if (quota === undefined || quota === null || quota <= 0) {
        return '';
    }
    return String(Math.round(quota / MB));
};

class SetUserTrafficQuotasDialog extends React.Component {
    constructor(props) {
        super(props);
        this.state = {
            storageInputValue: quotaBytesToInputValue(props.quotaTotal),
            uploadInputValue: quotaBytesToInputValue(props.trafficUploadQuota),
            downloadInputValue: quotaBytesToInputValue(props.trafficDownloadQuota),
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

    parseMegabytes = (value) => {
        const trimmed = `${value || ''}`.trim();
        if (trimmed === '') {
            return 0;
        }

        const parsed = Number(trimmed);
        if (!Number.isFinite(parsed) || parsed < 0) {
            return null;
        }

        return Math.round(parsed * MB);
    };

    renderField(label, value, fieldName, tip) {
        return (
            <React.Fragment>
                <label className="mb-1">{label}</label>
                <InputGroup>
                    <input type="text" className="form-control" value={value} onChange={this.handleInputChange(fieldName)} />
                    <InputGroupAddon addonType="append">
                        <InputGroupText>MB</InputGroupText>
                    </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-3">{tip}</p>
            </React.Fragment>
        );
    }

    formSubmit = () => {
        const quotaTotal = this.parseMegabytes(this.state.storageInputValue);
        const trafficUploadQuota = this.parseMegabytes(this.state.uploadInputValue);
        const trafficDownloadQuota = this.parseMegabytes(this.state.downloadInputValue);

        if (quotaTotal === null || trafficUploadQuota === null || trafficDownloadQuota === null) {
            this.setState({ formErrorMsg: gettext('Please enter a valid non-negative number.') });
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
                            {this.renderField(gettext('Storage quota'), storageInputValue, 'storageInputValue', gettext('0 means default limit.'))}
                            {this.renderField(gettext('Monthly upload quota'), uploadInputValue, 'uploadInputValue', gettext('0 means unlimited.'))}
                            {this.renderField(gettext('Monthly download quota'), downloadInputValue, 'downloadInputValue', gettext('0 means unlimited.'))}
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