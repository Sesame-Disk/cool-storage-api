import React from 'react';
import PropTypes from 'prop-types';
import { Button, Form, FormGroup, Label, Input } from 'reactstrap';
import { gettext } from '../../utils/constants';

const propTypes = {
  toggle: PropTypes.func.isRequired,
  handleSubmit: PropTypes.func.isRequired,
};

class AddOrgUserDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      email: '',
      name: '',
      errMessage: '',
      isAddingUser: false,
    };
  }

  handleSubmit = () => {
    let isValid = this.validateInputParams();
    if (isValid) {
      let { email, name } = this.state;
      this.setState({ isAddingUser: true });
      this.props.handleSubmit(email, name.trim());
    }
  };



  inputEmail = (e) => {
    let email = e.target.value.trim();
    this.setState({ email: email });
  };

  inputName = (e) => {
    let name = e.target.value;
    this.setState({ name: name });
  };



  toggle = () => {
    this.props.toggle();
  };

  validateInputParams() {
    let errMessage;
    let email = this.state.email;
    if (!email.length) {
      errMessage = gettext('email is required');
      this.setState({ errMessage: errMessage });
      return false;
    }
    let name = this.state.name.trim();
    if (!name.length) {
      errMessage = gettext('Name is required');
      this.setState({ errMessage: errMessage });
      return false;
    }

    return true;
  }

  render() {
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('Add User')}</h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              <Form>
                <FormGroup>
                  <Label for="userEmail">{gettext('Email')}</Label>
                  <Input id="userEmail" value={this.state.email || ''} onChange={this.inputEmail} />
                </FormGroup>
                <FormGroup>
                  <Label for="userName">{gettext('Name')}</Label>
                  <Input id="userName" value={this.state.name || ''} onChange={this.inputName} />
                </FormGroup>

              </Form>
              {this.state.errMessage && <Label className="err-message">{this.state.errMessage}</Label>}
            </div>
            <div className="modal-footer">
              <Button color="primary" disabled={this.state.isAddingUser} onClick={this.handleSubmit} className={this.state.isAddingUser ? 'btn-loading' : ''}>{gettext('Submit')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

AddOrgUserDialog.propTypes = propTypes;

export default AddOrgUserDialog;
