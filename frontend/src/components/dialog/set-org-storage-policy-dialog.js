import React from 'react';
import PropTypes from 'prop-types';
import { Button, Form, FormGroup, Input, Label } from 'reactstrap';
import { gettext } from '../../utils/constants';

const propTypes = {
    policy: PropTypes.object,
    availableRegions: PropTypes.array,
    toggleDialog: PropTypes.func.isRequired,
    updatePolicy: PropTypes.func.isRequired,
};

class SetOrgStoragePolicyDialog extends React.Component {

    constructor(props) {
        super(props);
        const policy = props.policy || {};
        this.state = {
            dataResidency: policy.data_residency || 'flexible',
            defaultRegion: policy.default_region || '',
            errorMessage: '',
            isSubmitting: false,
        };
    }

    toggle = () => {
        if (this.state.isSubmitting) {
            return;
        }
        this.props.toggleDialog();
    };

    handleResidencyChange = (event) => {
        this.setState({
            dataResidency: event.target.value,
            errorMessage: '',
        });
    };

    handleRegionChange = (event) => {
        this.setState({
            defaultRegion: event.target.value,
            errorMessage: '',
        });
    };

    handleSubmit = () => {
        const { dataResidency, defaultRegion } = this.state;
        if (dataResidency === 'strict' && !defaultRegion.trim()) {
            this.setState({ errorMessage: gettext('Strict data residency requires a default region.') });
            return;
        }

        const payload = {
            data_residency: dataResidency,
            default_region: defaultRegion.trim(),
        };

        this.setState({ isSubmitting: true, errorMessage: '' });
        this.props.updatePolicy(payload).then(() => {
            this.setState({ isSubmitting: false });
            this.props.toggleDialog();
        }).catch((errorMessage) => {
            this.setState({
                isSubmitting: false,
                errorMessage: errorMessage || gettext('Failed to update storage policy.'),
            });
        });
    };

    getRegionLabel = () => {
        return this.state.dataResidency === 'strict' ? gettext('Pinned region') : gettext('Fallback region');
    };

    renderRegionField = () => {
        const { availableRegions = [] } = this.props;
        const { defaultRegion } = this.state;
        if (availableRegions.length > 0) {
            return (
                <Input type="select" value={defaultRegion} onChange={this.handleRegionChange}>
                    <option value="">{gettext('Automatic')}</option>
                    {availableRegions.map((region) => (
                        <option key={region} value={region}>{region}</option>
                    ))}
                </Input>
            );
        }

        return (
            <Input
                type="text"
                value={defaultRegion}
                onChange={this.handleRegionChange}
                placeholder={gettext('Enter a configured region name')}
            />
        );
    };

    render() {
        const { dataResidency, errorMessage, isSubmitting } = this.state;

        return (
            <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
                <div className="modal-dialog modal-dialog-centered">
                    <div className="modal-content">
                        <div className="modal-header">
                            <h5 className="modal-title">{gettext('Set storage policy')}</h5>
                            <button type="button" className="close" onClick={this.toggle} aria-label="Close">
                                <span aria-hidden="true">&times;</span>
                            </button>
                        </div>
                        <div className="modal-body">
                            <Form>
                                <FormGroup>
                                    <Label for="org-storage-policy-mode">{gettext('Data residency')}</Label>
                                    <Input id="org-storage-policy-mode" type="select" value={dataResidency} onChange={this.handleResidencyChange}>
                                        <option value="flexible">{gettext('Flexible')}</option>
                                        <option value="strict">{gettext('Strict')}</option>
                                    </Input>
                                    <p className="text-muted mb-0 mt-2">
                                        {dataResidency === 'strict'
                                            ? gettext('Strict pins new libraries to the selected region.')
                                            : gettext('Flexible prefers the request region first. If that does not resolve, the fallback region is used before the global storage default.')}
                                    </p>
                                </FormGroup>
                                <FormGroup>
                                    <Label for="org-storage-policy-region">{this.getRegionLabel()}</Label>
                                    {this.renderRegionField()}
                                    <p className="text-muted mb-0 mt-2">
                                        {dataResidency === 'strict'
                                            ? gettext('Required for strict data residency. New libraries will always use this region.')
                                            : gettext('Used only when the request region does not resolve to a configured hot region. Leave empty to fall back to the global storage default.')}
                                    </p>
                                </FormGroup>
                                {errorMessage && <p className="error mb-0">{errorMessage}</p>}
                            </Form>
                        </div>
                        <div className="modal-footer">
                            <Button color="secondary" onClick={this.toggle} disabled={isSubmitting}>{gettext('Cancel')}</Button>
                            <Button color="primary" onClick={this.handleSubmit} disabled={isSubmitting}>{gettext('Submit')}</Button>
                        </div>
                    </div>
                </div>
            </div>
        );
    }
}

SetOrgStoragePolicyDialog.propTypes = propTypes;

export default SetOrgStoragePolicyDialog;