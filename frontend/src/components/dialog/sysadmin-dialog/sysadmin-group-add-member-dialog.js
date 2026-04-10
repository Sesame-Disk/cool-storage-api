import React from 'react';
import PropTypes from 'prop-types';
import { Button } from 'reactstrap';
import { gettext } from '../../../utils/constants';
import { seafileAPI } from '../../../utils/seafile-api';
import UserSelect from '../../user-select';

const propTypes = {
  toggle: PropTypes.func.isRequired,
  addMembers: PropTypes.func.isRequired,
  orgId: PropTypes.string,
};

class SysAdminGroupAddMemberDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      selectedOptions: null,
      isSubmitBtnDisabled: true
    };
  }

  handleSelectChange = (options) => {
    this.setState({
      selectedOptions: options,
      isSubmitBtnDisabled: !options.length
    });
  };

  addMembers = () => {
    let emails = this.state.selectedOptions.map(item => item.email);
    this.props.addMembers(emails);
    this.props.toggle();
  };

  render() {
    const { isSubmitBtnDisabled } = this.state;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content">
        <div className="modal-header">
              <h5 className="modal-title">{gettext('Add Member')}</h5>
              <button type="button" className="close" onClick={this.props.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
        <div className="modal-body">
          <UserSelect
            ref="userSelect"
            isMulti={true}
            className="reviewer-select"
            placeholder={gettext('Search users')}
            onSelectChange={this.handleSelectChange}
            searchFunc={(query) => seafileAPI.sysAdminSearchUsersForSelect(query, this.props.orgId)}
          />
        </div>
        <div className="modal-footer">
          <Button color="secondary" onClick={this.props.toggle}>{gettext('Cancel')}</Button>
          <Button color="primary" onClick={this.addMembers} disabled={isSubmitBtnDisabled}>{gettext('Submit')}</Button>
        </div>
      </div>
          </div>
        </div>
    );
  }
}

SysAdminGroupAddMemberDialog.propTypes = propTypes;

export default SysAdminGroupAddMemberDialog;
