import React from 'react';
import PropTypes from 'prop-types';
import { InputGroup, InputGroupAddon, InputGroupText } from 'reactstrap';
import { gettext } from '../../utils/constants';
import { parseGigabytesInput, quotaBytesToGigabyteInput } from '../../utils/quota-units';

const propTypes = {
    trafficQuota: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
    trafficUploadQuota: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
    trafficDownloadQuota: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
    updateQuota: PropTypes.func.isRequired,
    toggleDialog: PropTypes.func.isRequired,
};

class SetOrgTrafficQuota extends React.Component {
    constructor(props) {
        super(props);
        this.state = {
            trafficInputValue: quotaBytesToGigabyteInput(this.props.trafficQuota),
            uploadInputValue: quotaBytesToGigabyteInput(this.props.trafficUploadQuota),
            downloadInputValue: quotaBytesToGigabyteInput(this.props.trafficDownloadQuota),
            formErrorMsg: '',
            submitBtnDisabled: false,
        };
    }

    handleInputChange = (field) => (e) => {
        this.setState({
            [field]: e.target.value,
            formErrorMsg: '',
        });
    };

    formSubmit = () => {
        const trafficQuota = parseGigabytesInput(this.state.trafficInputValue, 0);
        const trafficUploadQuota = parseGigabytesInput(this.state.uploadInputValue, 0);
        const trafficDownloadQuota = parseGigabytesInput(this.state.downloadInputValue, 0);

        if (trafficQuota === null || trafficUploadQuota === null || trafficDownloadQuota === null) {
            this.setState({ formErrorMsg: gettext('Please enter a valid non-negative number.') });
            return;
        }

        this.setState({ submitBtnDisabled: true });
        this.props.updateQuota({
            traffic_quota: trafficQuota,
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

    renderField(label, value, fieldName, tip) {
        return (
            <React.Fragment>
                <label className="mb-1">{label}</label>
                <InputGroup>
                    <input type="text" className="form-control" value={value} onChange={this.handleInputChange(fieldName)} />
                    <InputGroupAddon addonType="append">
                        <InputGroupText>GB</InputGroupText>
                    </InputGroupAddon>
                </InputGroup>
                <p className="small text-secondary mt-2 mb-3">{tip}</p>
            </React.Fragment>
        );
    }

    render() {
        const { trafficInputValue, uploadInputValue, downloadInputValue, formErrorMsg, submitBtnDisabled } = this.state;

        return (
            <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
                <div className="modal-dialog modal-dialog-centered">
                    <div className="modal-content">
                        <div className="modal-header">
                            <h5 className="modal-title">{gettext('Set organization traffic quotas')}</h5>
                            <button type="button" className="close" onClick={this.props.toggleDialog} aria-label="Close">
                                <span aria-hidden="true">&times;</span>
                            </button>
                        </div>
                        <div className="modal-body">
                            {this.renderField(gettext('Combined monthly traffic'), trafficInputValue, 'trafficInputValue', gettext('0 means unlimited.'))}
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

SetOrgTrafficQuota.propTypes = propTypes;

export default SetOrgTrafficQuota;