import React, { Component, Fragment } from 'react';
import Subscription from '../../components/subscription';
import { gettext } from '../../utils/constants';

class SubscriptionView extends Component {
  render() {
    return (
      <Fragment>
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <div className="cur-view-path">
              <h2 className="sf-heading">{gettext('Subscription')}</h2>
            </div>
            <div className="pt-2 h-100 o-auto">
              <Subscription isOrgContext={false} />
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

export default SubscriptionView;
