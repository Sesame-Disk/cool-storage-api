import React, { Component } from 'react';
import PropTypes from 'prop-types';
import { Button } from 'reactstrap';
import { gettext } from '../../utils/constants';

const propTypes = {
  actionURL: PropTypes.string.isRequired,
  toggle: PropTypes.func.isRequired
};

class ConfirmDeleteAccount extends Component {

  action = () => {
    window.location.assign(this.props.actionURL);
  };

  render() {
    const { toggle } = this.props;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('Delete Account')}</h5>
              <button type="button" className="close" onClick={toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              <p>{gettext('Really want to delete your account?')}</p>
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={toggle}>{gettext('Cancel')}</Button>
              <Button color="primary" onClick={this.action}>{gettext('Delete')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

ConfirmDeleteAccount.propTypes = propTypes;

export default ConfirmDeleteAccount;
