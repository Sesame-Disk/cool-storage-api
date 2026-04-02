import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import MainPanelTopbar from './main-panel-topbar';
import Subscription from '../../components/subscription';
import { gettext } from '../../utils/constants';

const propTypes = {
  onCloseSidePanel: PropTypes.func,
};

class OrgSubscription extends Component {
  render() {
    return (
      <Fragment>
        <MainPanelTopbar onCloseSidePanel={this.props.onCloseSidePanel} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <div className="cur-view-path">
              <h2 className="sf-heading">{gettext('Subscription')}</h2>
            </div>
            <div className="cur-view-content pt-2">
              <Subscription />
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

OrgSubscription.propTypes = propTypes;

export default OrgSubscription;
