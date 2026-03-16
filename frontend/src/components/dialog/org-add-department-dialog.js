import React from 'react';
import PropTypes from 'prop-types';
import { Button, Input, Form, FormGroup, Label } from 'reactstrap';
import { gettext, orgID } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { Utils } from '../../utils/utils';

const propTypes = {
  groupID: PropTypes.string,
  parentGroupID: PropTypes.string,
  toggle: PropTypes.func.isRequired,
  onDepartChanged: PropTypes.func.isRequired,
};

class AddDepartDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      departName: '',
      errMessage: '',
    };
  }

  handleSubmit = () => {
    let isValid = this.validateName();
    if (isValid) {
      let parentGroup = -1;
      if (this.props.parentGroupID) {
        parentGroup = this.props.parentGroupID;
      }
      seafileAPI.orgAdminAddDepartGroup(orgID, this.state.departName.trim(), parentGroup).then((res) => {
        this.props.toggle();
        this.props.onDepartChanged();
      }).catch(error => {
        let errorMsg = Utils.getErrorMsg(error);
        this.setState({ errMessage: errorMsg });
      });
    }
  };

  validateName = () => {
    let errMessage = '';
    const name = this.state.departName.trim();
    if (!name.length) {
      errMessage = gettext('Name is required');
      this.setState({ errMessage: errMessage });
      return false;
    }
    return true;
  };

  handleChange = (e) => {
    this.setState({
      departName: e.target.value,
    });
  };

  handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      this.handleSubmit();
      e.preventDefault();
    }
  };

  render() {
    let header = this.props.parentGroupID ? gettext('New Sub-department') : gettext('New Department');
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content">
        <div className="modal-header">
              <h5 className="modal-title">{header}</h5>
              <button type="button" className="close" onClick={this.props.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
        <div className="modal-body">
          <Form>
            <FormGroup>
              <Label for="departName">{gettext('Name')}</Label>
              <Input
                id="departName"
                onKeyDown={this.handleKeyDown}
                value={this.state.departName}
                onChange={this.handleChange}
                autoFocus={true}
              />
            </FormGroup>
          </Form>
          { this.state.errMessage && <p className="error">{this.state.errMessage}</p> }
        </div>
        <div className="modal-footer">
          <Button color="primary" onClick={this.handleSubmit}>{gettext('Submit')}</Button>
        </div>
      </div>
          </div>
        </div>
    );
  }
}

AddDepartDialog.propTypes = propTypes;

export default AddDepartDialog;
