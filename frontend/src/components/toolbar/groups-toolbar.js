import React from 'react';
import PropTypes from 'prop-types';
import MediaQuery from 'react-responsive';
import CommonToolbar from './common-toolbar';
import { Button } from 'reactstrap';
import { gettext } from '../../utils/constants';
import { getUpgradeState } from '../../utils/upgrade-state';
import UpgradeCallout from '../common/upgrade-callout';

const propTypes = {
  searchPlaceholder: PropTypes.string,
  onShowSidePanel: PropTypes.func.isRequired,
  onSearchedClick: PropTypes.func.isRequired,
  toggleAddGroupModal: PropTypes.func.isRequired,
};

class GroupsToolbar extends React.Component {
  render() {
    let { onShowSidePanel, onSearchedClick } = this.props;
    // Check permission dynamically (not from import, as it updates after API call)
    const userCanAddGroup = window.app.pageOptions.canAddGroup;
    const { isSingleMemberPlan } = getUpgradeState();
    return (
      <div className="main-panel-north border-left-show">
        <div className="cur-view-toolbar">
          <span title="Side Nav Menu" aria-label="Open menu" role="button" tabIndex={0} onClick={onShowSidePanel} className="sf2-icon-menu side-nav-toggle hidden-md-up d-md-none"></span>
          {userCanAddGroup && (
            <div className="operation">
              <MediaQuery query="(min-width: 768px)">
                <Button color="btn btn-secondary operation-item" onClick={this.props.toggleAddGroupModal}>
                  <i className="fas fa-plus-square text-secondary mr-1"></i>{gettext('New Group')}
                </Button>
              </MediaQuery>
              <MediaQuery query="(max-width: 767.8px)">
                <span className="sf2-icon-plus mobile-toolbar-icon" title={gettext('New Group')} onClick={this.props.toggleAddGroupModal}></span>
              </MediaQuery>
            </div>
          )}
        </div>
        {!userCanAddGroup && isSingleMemberPlan && (
          <UpgradeCallout
            title={gettext('Groups require a collaborative plan')}
            description={gettext('Your current plan only supports one member. Upgrade to create groups and collaborate with other users.')}
            ctaText={gettext('Upgrade Plan')}
            className="mx-3 mt-3 mb-0"
          />
        )}
        <CommonToolbar searchPlaceholder={this.props.searchPlaceholder} onSearchedClick={onSearchedClick} />
      </div>
    );
  }
}

GroupsToolbar.propTypes = propTypes;

export default GroupsToolbar;
