import React from 'react';
import PropTypes from 'prop-types';
import { Button } from 'reactstrap';
import UserSelect from '../../user-select';
import { gettext } from '../../../utils/constants';
import { seafileAPI } from '../../../utils/seafile-api';

const propTypes = {
  toggle: PropTypes.func.isRequired,
  addAdminInBatch: PropTypes.func.isRequired,
};

class SysAdminBatchAddAdminDialog extends React.Component {
  constructor(props) {
    super(props);
    this.state = {
      options: null,
      isSubmitBtnActive: false
    };
  }

  toggle = () => {
    this.props.toggle();
  };

  handleSelectChange = (options) => {
    this.setState({
      options: options,
      isSubmitBtnActive: options.length > 0
    });
  };

  handleSubmit = () => {
    this.props.addAdminInBatch(this.state.options.map(item => item.email));
    this.toggle();
  };

  render() {
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content">
        <div className="modal-header">
              <h5 className="modal-title">{gettext('Add Admin')}</h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
        <div className="modal-body">
          <UserSelect
            isMulti={true}
            className="reviewer-select"
            placeholder={gettext('Search users')}
            onSelectChange={this.handleSelectChange}
            searchFunc={(query) => seafileAPI.sysAdminSearchUsersForSelect(query)}
          />
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

SysAdminBatchAddAdminDialog.propTypes = propTypes;

export default SysAdminBatchAddAdminDialog;
