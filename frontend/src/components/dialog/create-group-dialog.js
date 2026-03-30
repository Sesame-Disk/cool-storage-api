import React from 'react';
import PropTypes from 'prop-types';
import { gettext } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { Input, Button } from 'reactstrap';
import { Utils } from '../../utils/utils';
import UpgradeCallout from '../common/upgrade-callout';
import { getUpgradeState } from '../../utils/upgrade-state';

class CreateGroupDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      groupName: '',
      errorMsg: '',
      isSubmitBtnActive: false,
    };
  }

  handleGroupChange = (event) => {
    let name = event.target.value;

    if (!name.trim()) {
      this.setState({ isSubmitBtnActive: false });
    } else {
      this.setState({ isSubmitBtnActive: true });
    }
    this.setState({
      groupName: name
    });
    if (this.state.errorMsg) {
      this.setState({
        errorMsg: ''
      });
    }
  };

  handleSubmitGroup = () => {
    let name = this.state.groupName.trim();
    if (name) {
      let that = this;
      seafileAPI.createGroup(name).then((res) => {
        that.props.onCreateGroup();
      }).catch((error) => {
        let errorMsg = Utils.getErrorMsg(error);
        this.setState({ errorMsg: errorMsg });
      });
    } else {
      this.setState({
        errorMsg: gettext('Name is required')
      });
    }
    this.setState({
      groupName: '',
    });
  };

  handleKeyDown = (e) => {
    if (e.keyCode === 13) {
      this.handleSubmitGroup();
      e.preventDefault();
    }
  };

  render() {
    const { isSingleMemberPlan } = getUpgradeState();
    const canAddGroup = window.app.pageOptions.canAddGroup;
    const groupCreationLocked = !canAddGroup && isSingleMemberPlan;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('New Group')}</h5>
              <button type="button" className="close" onClick={this.props.toggleAddGroupModal} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              {groupCreationLocked && (
                <UpgradeCallout
                  title={gettext('Upgrade required to create groups')}
                  description={gettext('Your current plan only supports one member. Upgrade to create groups and collaborate across your organization.')}
                  className="mb-3"
                />
              )}
              <label htmlFor="groupName">{gettext('Name')}</label>
              <Input
                type="text"
                id="groupName"
                value={this.state.groupName}
                onChange={this.handleGroupChange}
                onKeyDown={this.handleKeyDown}
                autoFocus={true}
                disabled={groupCreationLocked}
              />
              <span className="error">{this.state.errorMsg}</span>
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={this.props.toggleAddGroupModal}>{gettext('Cancel')}</Button>
              <Button color="primary" onClick={this.handleSubmitGroup} disabled={groupCreationLocked || !this.state.isSubmitBtnActive}>{gettext('Submit')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

const CreateGroupDialogPropTypes = {
  toggleAddGroupModal: PropTypes.func.isRequired,
  onCreateGroup: PropTypes.func.isRequired,
  showAddGroupModal: PropTypes.bool.isRequired,
};

CreateGroupDialog.propTypes = CreateGroupDialogPropTypes;

export default CreateGroupDialog;
