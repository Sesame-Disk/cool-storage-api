import React from 'react';
import PropTypes from 'prop-types';
import { Button, Input, Form, FormGroup, Label, Alert } from 'reactstrap';
import { gettext } from '../../../utils/constants';
import { seafileAPI } from '../../../utils/seafile-api';
import UserSelect from '../../user-select';

const propTypes = {
  createRepo: PropTypes.func.isRequired,
  toggleDialog: PropTypes.func.isRequired,
};

class SysAdminCreateRepoDialog extends React.Component {
  constructor(props) {
    super(props);
    this.state = {
      repoName: '',
      ownerEmail: '',
      errMessage: '',
      isSubmitBtnActive: false
    };
  }

  handleRepoNameChange = (e) => {
    const value = e.target.value;
    this.setState({
      repoName: value,
      isSubmitBtnActive: value.trim()
    });
  };

  handleSubmit = () => {
    const { repoName, ownerEmail } = this.state;
    this.props.createRepo(repoName.trim(), ownerEmail);
    this.toggle();
  };

  handleSelectChange = (option) => {
    // option can be null
    this.setState({
      ownerEmail: option ? option.email : ''
    });
  };

  handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      this.handleSubmit();
      e.preventDefault();
    }
  };

  toggle = () => {
    this.props.toggleDialog();
  };

  render() {
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content">
        <div className="modal-header">
              <h5 className="modal-title">{gettext('New Library')}</h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
        <div className="modal-body">
          <Form>
            <FormGroup>
              <Label for="repoName">{gettext('Name')}</Label>
              <Input
                id="repoName"
                onKeyDown={this.handleKeyDown}
                value={this.state.repoName}
                onChange={this.handleRepoNameChange}
                autoFocus={true}
              />
            </FormGroup>
            <FormGroup>
              <Label for="userSelect">
                {gettext('Owner')}
                <span className="small text-secondary">{gettext('(If left blank, owner will be admin)')}</span>
              </Label>
              <UserSelect
                id="userSelect"
                isMulti={false}
                className="reviewer-select"
                placeholder={gettext('Select a user')}
                onSelectChange={this.handleSelectChange}
                searchFunc={(query) => seafileAPI.sysAdminSearchUsersForSelect(query)}
              />
            </FormGroup>
          </Form>
          {this.state.errMessage && <Alert color="danger">{this.state.errMessage}</Alert>}
        </div>
        <div className="modal-footer">
          <Button color="secondary" onClick={this.toggle}>{gettext('Cancel')}</Button>
          <Button color="primary" onClick={this.handleSubmit} disabled={!this.state.isSubmitBtnActive}>{gettext('Submit')}</Button>
        </div>
      </div>
          </div>
        </div>
    );
  }
}

SysAdminCreateRepoDialog.propTypes = propTypes;

export default SysAdminCreateRepoDialog;
