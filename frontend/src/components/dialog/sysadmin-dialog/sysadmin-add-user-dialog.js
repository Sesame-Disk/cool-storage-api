import React from 'react';
import PropTypes from 'prop-types';
import { Alert, Button, Form, FormGroup, Label, Input, InputGroup, InputGroupAddon } from 'reactstrap';
import { gettext } from '../../../utils/constants';
import { Utils } from '../../../utils/utils';
import toaster from '../../../components/toast';
import SysAdminUserRoleEditor from '../../../components/select-editor/sysadmin-user-role-editor';

const propTypes = {
  availableRoles: PropTypes.array.isRequired,
  dialogTitle: PropTypes.string.isRequired,
  showRole: PropTypes.bool.isRequired,
  toggleDialog: PropTypes.func.isRequired,
  addUser: PropTypes.func.isRequired
};

class SysAdminAddUserDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      errorMsg: '',
      email: '',
      name: '',
      role: 'default',
      isSubmitBtnActive: false
    };
  }

  checkSubmitBtnActive = () => {
    const { email } = this.state;
    let btnActive = !!email.trim();
    this.setState({
      isSubmitBtnActive: btnActive
    });
  };

  toggle = () => {
    this.props.toggleDialog();
  };



  inputEmail = (e) => {
    let email = e.target.value;
    this.setState({
      email: email
    }, this.checkSubmitBtnActive);
  };

  inputName = (e) => {
    let name = e.target.value;
    this.setState({
      name: name
    });
  };

  updateRole = (role) => {
    this.setState({
      role: role
    });
  };

  handleSubmit = () => {
    const { email, name, role } = this.state;
    let data = {
      email: email.trim(),
      name: name.trim()
    };
    if (this.props.showRole) {
      data.role = role;
    }
    this.setState({ isSubmitBtnActive: false });
    this.props.addUser(data).then(() => {
      this.toggle();
    }).catch((error) => {
      let errMsg = Utils.getErrorMsg(error);
      toaster.danger(errMsg);
      this.setState({ isSubmitBtnActive: true });
    });
  };

  render() {
    const { dialogTitle, showRole } = this.props;
    const {
      errorMsg, email, name, role,
      isSubmitBtnActive
    } = this.state;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{dialogTitle || gettext('Add Member')}</h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              <Form autoComplete="off">
                <FormGroup>
                  <Label>{gettext('Email')}</Label>
                  <Input value={email} onChange={this.inputEmail} />
                </FormGroup>
                <FormGroup>
                  <Label>{gettext('Name(optional)')}</Label>
                  <Input type="text" value={name} onChange={this.inputName} />
                </FormGroup>
                {showRole &&
                  <FormGroup>
                    <Label>
                      {gettext('Role')}
                      <span className="small text-secondary ml-1 fas fa-question-circle" title={gettext('You can also add a user as a guest, who will not be allowed to create libraries and groups.')}></span>
                    </Label>
                    <SysAdminUserRoleEditor
                      isTextMode={false}
                      isEditIconShow={false}
                      currentRole={role}
                      roleOptions={this.props.availableRoles}
                      onRoleChanged={this.updateRole}
                    />
                  </FormGroup>
                }

              </Form>
              {errorMsg && <Alert color="danger">{errorMsg}</Alert>}
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

SysAdminAddUserDialog.propTypes = propTypes;

export default SysAdminAddUserDialog;
