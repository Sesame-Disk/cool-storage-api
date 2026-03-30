import React from 'react';
import PropTypes from 'prop-types';
import { Button } from 'reactstrap';
import { gettext } from '../../utils/constants';
import UserSelect from '../user-select';

const propTypes = {
    onSubmit: PropTypes.func.isRequired,
    searchFunc: PropTypes.func.isRequired,
    toggleDialog: PropTypes.func.isRequired,
    currentOwner: PropTypes.string,
};

class TransferOrgOwnershipDialog extends React.Component {
    constructor(props) {
        super(props);
        this.state = {
            selectedOption: null,
            submitBtnDisabled: true,
        };
    }

    handleSelectChange = (option) => {
        this.setState({
            selectedOption: option,
            submitBtnDisabled: !option,
        });
    };

    submit = () => {
        if (!this.state.selectedOption?.email) {
            return;
        }

        this.props.onSubmit(this.state.selectedOption.email);
    };

    render() {
        const { currentOwner } = this.props;
        const ownerNote = currentOwner
            ? gettext('Current owner: %(owner)s').replace('%(owner)s', currentOwner)
            : gettext('The new owner must already be an organization admin.');

        return (
            <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
                <div className="modal-dialog modal-dialog-centered">
                    <div className="modal-content">
                        <div className="modal-header">
                            <h5 className="modal-title">{gettext('Transfer organization ownership')}</h5>
                            <button type="button" className="close" onClick={this.props.toggleDialog} aria-label="Close">
                                <span aria-hidden="true">&times;</span>
                            </button>
                        </div>
                        <div className="modal-body">
                            <p className="text-secondary mb-3">{ownerNote}</p>
                            <p className="small text-secondary mt-n2 mb-3">
                                {gettext('After the transfer, the new owner gains billing and ownership privileges and the previous owner becomes an admin.')}
                            </p>
                            <UserSelect
                                ref="userSelect"
                                isMulti={false}
                                className="reviewer-select"
                                placeholder={gettext('Search organization admins')}
                                onSelectChange={this.handleSelectChange}
                                searchFunc={this.props.searchFunc}
                            />
                        </div>
                        <div className="modal-footer">
                            <Button color="secondary" onClick={this.props.toggleDialog}>{gettext('Cancel')}</Button>
                            <Button color="primary" onClick={this.submit} disabled={this.state.submitBtnDisabled}>{gettext('Transfer ownership')}</Button>
                        </div>
                    </div>
                </div>
            </div>
        );
    }
}

TransferOrgOwnershipDialog.propTypes = propTypes;

export default TransferOrgOwnershipDialog;