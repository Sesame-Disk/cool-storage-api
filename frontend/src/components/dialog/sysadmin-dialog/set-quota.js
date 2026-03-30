import React from 'react';
import PropTypes from 'prop-types';
import { Button, Form, FormGroup, Input, InputGroup, InputGroupAddon, InputGroupText } from 'reactstrap';
import { gettext } from '../../../utils/constants';
import { parseGigabytesInput } from '../../../utils/quota-units';

const propTypes = {
  toggle: PropTypes.func.isRequired,
  updateQuota: PropTypes.func.isRequired
};

class SetQuotaDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      quota: '',
      isSubmitBtnActive: false
    };
  }

  toggle = () => {
    this.props.toggle();
  };

  handleQuotaChange = (e) => {
    const value = e.target.value;
    this.setState({
      quota: value,
      isSubmitBtnActive: value.trim() !== ''
    });
  };

  handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      this.handleSubmit();
      e.preventDefault();
    }
  };

  handleSubmit = () => {
    const quotaBytes = parseGigabytesInput(this.state.quota.trim(), 0);
    if (quotaBytes === null) {
      return;
    }

    this.props.updateQuota(quotaBytes);
    this.toggle();
  };

  render() {
    const { quota, isSubmitBtnActive } = this.state;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('Set Quota')}</h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              <Form>
                <FormGroup>
                  <InputGroup>
                    <Input
                      type="text"
                      className="form-control"
                      value={quota}
                      onKeyDown={this.handleKeyDown}
                      onChange={this.handleQuotaChange}
                    />
                    <InputGroupAddon addonType="append">
                      <InputGroupText>GB</InputGroupText>
                    </InputGroupAddon>
                  </InputGroup>
                  <p className="small text-secondary mt-2 mb-2">
                    {gettext('An integer that is greater than or equal to 0.')}
                    <br />
                    {gettext('Tip: 0 means default limit')}
                  </p>
                </FormGroup>
              </Form>
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={this.toggle}>{gettext('Cancel')}</Button>
              <Button color="primary" onClick={this.handleSubmit} disabled={!isSubmitBtnActive}>{gettext('Submit')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

SetQuotaDialog.propTypes = propTypes;

export default SetQuotaDialog;
