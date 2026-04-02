import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { gettext, billingUrl } from '../utils/constants';
import { Utils } from '../utils/utils';
import { subscriptionAPI } from '../utils/subscription-api';
import Loading from './loading';

import '../css/layout.css';
import '../css/subscription.css';

const formatPlanName = (plan) => {
  if (!plan) {
    return gettext('Organization');
  }
  return plan
    .split(/[-_]/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ');
};

const formatBillingCycle = (billingCycle) => {
  if (billingCycle === 'annual') {
    return gettext('Annual');
  }
  if (billingCycle === 'monthly') {
    return gettext('Monthly');
  }
  return billingCycle || '--';
};

const formatBytesOrUnlimited = (bytes) => {
  if (bytes > 0) {
    return Utils.bytesToSize(bytes);
  }
  return gettext('Unlimited');
};

const formatUsageAndLimit = (used, limit) => {
  return `${Utils.bytesToSize(used || 0)} / ${formatBytesOrUnlimited(limit)}`;
};

const formatUserLimit = (currentUsers, maxUsers) => {
  if (maxUsers > 0) {
    return `${currentUsers || 0} / ${maxUsers}`;
  }
  return `${currentUsers || 0} / ${gettext('Unlimited')}`;
};

const quotaUsagePercent = (used, limit) => {
  if (limit <= 0) {
    return gettext('Unlimited');
  }
  return `${((used || 0) / limit * 100).toFixed(1)}%`;
};

const propTypes = {
  handleContentScroll: PropTypes.func,
};

class Subscription extends Component {

  constructor(props) {
    super(props);
    this.state = {
      isLoading: true,
      errorMsg: '',
      subscriptionData: null,
      fallbackDetails: null,
    };
  }

  getSubscription = () => {
    subscriptionAPI.getSubscription().then((res) => {
      const data = res.data || {};

      if (Object.prototype.hasOwnProperty.call(data, 'storage_quota') || Object.prototype.hasOwnProperty.call(data, 'traffic_quota')) {
        this.setState({
          isLoading: false,
          errorMsg: '',
          subscriptionData: data,
          fallbackDetails: null,
        });
        return;
      }

      const subscription = data.subscription;
      if (!subscription) {
        this.setState({
          isLoading: false,
          subscriptionData: null,
          fallbackDetails: null,
        });
      } else {
        let isActive = subscription.is_active;
        let plan = subscription.plan || {};
        let storageQuota = isActive ? subscription.asset_quota : plan.asset_quota;
        this.setState({
          isLoading: false,
          subscriptionData: null,
          fallbackDetails: {
            planName: plan.name || gettext('Organization'),
            userLimit: subscription.user_limit > 0 ? String(subscription.user_limit) : gettext('Unlimited'),
            storageSummary: storageQuota > 0 ? `${storageQuota} GB` : gettext('Unlimited'),
            billingSummary: isActive ? subscription.term_end : '--',
          },
        });
      }
    }).catch(error => {
      let errorMsg = Utils.getErrorMsg(error);
      this.setState({
        isLoading: false,
        errorMsg: errorMsg,
      });
    });
  };

  componentDidMount() {
    this.getSubscription();
  }

  renderQuotaSummary = (label, used, limit, isLast = false) => {
    return (
      <dd className={`order-item${isLast ? ' order-item-bottom rounded-0' : ''}`}>
        <span className="order-into">{label}</span>
        <span className="order-value">{formatUsageAndLimit(used, limit)}</span>
      </dd>
    );
  };

  renderCurrentSubscription = () => {
    const { subscriptionData } = this.state;
    const storageQuota = subscriptionData.storage_quota || 0;
    const storageUsed = subscriptionData.storage_used || 0;
    const trafficQuota = subscriptionData.traffic_quota || 0;
    const combinedUsed = subscriptionData.traffic_combined_used || 0;
    const uploadQuota = subscriptionData.traffic_upload_quota || 0;
    const uploadUsed = subscriptionData.traffic_upload_used || 0;
    const downloadQuota = subscriptionData.traffic_download_quota || 0;
    const downloadUsed = subscriptionData.traffic_download_used || 0;
    const currentUsers = subscriptionData.current_users || 0;
    const maxUsers = subscriptionData.max_users || 0;

    return (
      <Fragment>
        <div id="current-plan" className="subscription-info">
          <h3 className="subscription-info-heading">{gettext('Current Plan')}</h3>
          <p className="mb-2">{formatPlanName(subscriptionData.plan)}</p>
        </div>
        <div id="user-limit" className="subscription-info">
          <h3 className="subscription-info-heading">{gettext('User Limit')}</h3>
          <p className="mb-2">{formatUserLimit(currentUsers, maxUsers)}</p>
        </div>
        <div id="asset-quota" className="subscription-info">
          <h3 className="subscription-info-heading">{gettext('Storage')}</h3>
          <dl className="items-dl mb-0">
            {this.renderQuotaSummary(gettext('Used'), storageUsed, storageQuota)}
            <dd className="order-item order-item-bottom rounded-0">
              <span className="order-into">{gettext('Usage')}</span>
              <span className="order-value">{quotaUsagePercent(storageUsed, storageQuota)}</span>
            </dd>
          </dl>
        </div>
        <div id="traffic-quota" className="subscription-info">
          <h3 className="subscription-info-heading">{gettext('Traffic')}</h3>
          <dl className="items-dl mb-0">
            {trafficQuota > 0 && this.renderQuotaSummary(gettext('Combined Monthly Traffic'), combinedUsed, trafficQuota)}
            {this.renderQuotaSummary(gettext('Monthly Upload Traffic'), uploadUsed, uploadQuota)}
            {this.renderQuotaSummary(gettext('Monthly Download Traffic'), downloadUsed, downloadQuota)}
            <dd className="order-item order-item-bottom rounded-0">
              <span className="order-into">{gettext('Traffic Reset')}</span>
              <span className="order-value">{subscriptionData.traffic_reset_date || '--'}</span>
            </dd>
          </dl>
        </div>
        <div id="current-subscription-period" className="subscription-info">
          <h3 className="subscription-info-heading">{gettext('Billing Cycle')}</h3>
          <p className="mb-2">{formatBillingCycle(subscriptionData.billing_cycle)}</p>
        </div>
      </Fragment>
    );
  };

  render() {
    const { isLoading, errorMsg, subscriptionData, fallbackDetails } = this.state;
    if (isLoading) {
      return <Loading />;
    }
    if (errorMsg) {
      return <p className="text-center mt-8 error">{errorMsg}</p>;
    }
    return (
      <Fragment>
        <div className="content subscription-content position-relative" onScroll={this.props.handleContentScroll}>
          {subscriptionData ? this.renderCurrentSubscription() : fallbackDetails && (
            <Fragment>
              <div id="current-plan" className="subscription-info">
                <h3 className="subscription-info-heading">{gettext('Current Plan')}</h3>
                <p className="mb-2">{fallbackDetails.planName}</p>
              </div>
              <div id="user-limit" className="subscription-info">
                <h3 className="subscription-info-heading">{gettext('User Limit')}</h3>
                <p className="mb-2">{fallbackDetails.userLimit}</p>
              </div>
              <div id="asset-quota" className="subscription-info">
                <h3 className="subscription-info-heading">{gettext('Storage')}</h3>
                <p className="mb-2">{fallbackDetails.storageSummary}</p>
              </div>
              <div id="current-subscription-period" className="subscription-info">
                <h3 className="subscription-info-heading">{gettext('Billing Cycle')}</h3>
                <p className="mb-2">{fallbackDetails.billingSummary}</p>
              </div>
            </Fragment>
          )}
          <div id="product-price" className="subscription-info">
            <h3 className="subscription-info-heading">{gettext('Billing Details')}</h3>
            <p className="mb-2 text-secondary">{gettext('Plan changes and billing are managed in the billing service.')}</p>
            {billingUrl && (
              <p className="mb-2">
                <a rel="noopener noreferrer" target="_blank" href={billingUrl}>{gettext('Manage Billing')}</a>
              </p>
            )}
          </div>
        </div>
      </Fragment>
    );
  }
}

Subscription.propTypes = propTypes;

export default Subscription;
