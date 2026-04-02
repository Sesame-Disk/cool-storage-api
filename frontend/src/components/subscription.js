import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import toaster from './toast';
import { InputGroup, InputGroupAddon, InputGroupText, Input, Button } from 'reactstrap';
import { gettext, serviceURL, subscriptionDetailsUrl } from '../utils/constants';
import { Utils } from '../utils/utils';
import { BYTES_IN_GB } from '../utils/quota-units';
import { subscriptionAPI } from '../utils/subscription-api';
import Loading from './loading';

import '../css/layout.css';
import '../css/subscription.css';

const isOrgContext = window.app?.pageOptions?.isOrgContext ?? true;

const formatPlanName = (plan, orgContext) => {
  if (!plan) {
    return orgContext ? gettext('Organization') : gettext('Personal');
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

const PlansPropTypes = {
  plans: PropTypes.array.isRequired,
  onPay: PropTypes.func.isRequired,
  paymentType: PropTypes.string.isRequired,
  handleContentScroll: PropTypes.func.isRequired,
};

class Plans extends Component {
  constructor(props) {
    super(props);
    this.state = {
      currentPlan: props.plans[0],
      assetQuotaUnitCount: 1,
      count: 1,
    };
  }

  togglePlan = (plan) => {
    this.setState({ currentPlan: plan }, () => {
    });
  };

  onPay = () => {
    let { paymentType } = this.props;
    let { currentPlan, assetQuotaUnitCount, count } = this.state;
    let totalAmount, assetQuota, newUserCount;

    // parse
    if (paymentType === 'paid') {
      newUserCount = currentPlan.count;
      totalAmount = currentPlan.total_amount;
    } else if (paymentType === 'extend_time') {
      newUserCount = currentPlan.count;
      assetQuota = currentPlan.asset_quota;
      totalAmount = currentPlan.total_amount;
    } else if (paymentType === 'add_user') {
      newUserCount = count;
      totalAmount = count * currentPlan.price_per_user;
    } else if (paymentType === 'buy_quota') {
      assetQuota = (assetQuotaUnitCount) * currentPlan.asset_quota_unit;
      totalAmount = assetQuotaUnitCount * currentPlan.price_per_asset_quota_unit;
    } else {
      toaster.danger(gettext('Internal Server Error'));
      return;
    }

    this.props.onPay(currentPlan.plan_id, newUserCount, assetQuota, totalAmount);
  };

  onCountInputChange = (e) => {
    let { currentPlan } = this.state;
    if (!currentPlan.can_custom_count) {
      return;
    }
    let count = e.target.value.replace(/^(0+)|[^\d]+/g, '');
    if (count < 1) {
      count = 1;
    } else if (count > 9999) {
      count = 9999;
    }
    this.setState({ count: count });
  };

  onAssetQuotaUnitCountInputChange = (e) => {
    let { currentPlan } = this.state;
    if (!currentPlan.can_custom_asset_quota) {
      return;
    }
    let count = e.target.value.replace(/^(0+)|[^\d]+/g, '');
    if (count < 1) {
      count = 1;
    } else if (count > 9999) {
      count = 9999;
    }
    this.setState({ assetQuotaUnitCount: count });
  };

  renderPaidOrExtendTime = () => {
    let { plans, paymentType } = this.props;
    let { currentPlan } = this.state;
    let boughtQuota = 0;
    if (paymentType === 'extend_time') {
      boughtQuota = currentPlan.asset_quota - 100;
    }
    let totalAmount = currentPlan.total_amount;
    let originalTotalAmount = totalAmount;
    return (
      <div className='d-flex flex-column subscription-container'>
        <span className="subscription-subtitle">{gettext('Choose Plan')}</span>
        <dl className='items-dl'>
          {plans.map((item, index) => {
            let selectedCss = item.plan_id === currentPlan.plan_id ? 'plan-selected' : '';
            let countDescription = '￥' + item.price_per_user;
            if (isOrgContext) {
              countDescription += '/每用户';
            }
            return (
              <dd key={index} className={`plan-description-item ${selectedCss}`} onClick={this.togglePlan.bind(this, item)}>
                <span className='plan-name'>{item.name}</span>
                <span className='plan-description'>{countDescription}</span>
              </dd>
            );
          })}
        </dl>

        {paymentType === 'extend_time' && boughtQuota > 0 &&
          <Fragment>
            <span className="subscription-subtitle">{gettext('Additional Storage')}</span>
            <dl className='items-dl'>
              <dd className='order-item order-item-top order-item-bottom subscription-list'>
                <span className='order-into'>{currentPlan.asset_quota_unit + 'GB x ' + (boughtQuota / currentPlan.asset_quota_unit)}</span>
                {/* 续费时候需要减去附赠的100GB */}
                <span className='order-value'>{'￥' + (boughtQuota / currentPlan.asset_quota_unit) * currentPlan.price_per_asset_quota_unit}</span>
              </dd>
            </dl>
          </Fragment>
        }

        <span className="subscription-subtitle">{gettext('Summary')}</span>
        <dl className='items-dl'>
          <div>
            <dd className='order-item order-item-top'>
              <span className='order-into'>{gettext('Selected Plan')}</span>
              <span className='order-value'>{currentPlan.name}</span>
            </dd>
            {isOrgContext &&
              <dd className='order-item'>
                <span className='order-into'>{gettext('Users')}</span>
                <span className='order-value'>{`${currentPlan.count} ${gettext('users')}`}</span>
              </dd>
            }
            <dd className='order-item'>
              <span className='order-into'>{gettext('Available Storage')}</span>
              <span className='order-value'>{`100GB (${gettext('included')})` + (boughtQuota > 0 ? ` + ${boughtQuota}GB (${gettext('extra')})` : '')}</span>
            </dd>
            <dd className='order-item order-item-bottom rounded-0'>
              <span className='order-into'>{gettext('Expiration')}</span>
              <span className='order-value'>{currentPlan.new_term_end}</span>
            </dd>
            <dd className='order-item order-item-bottom subscription-list'>
              <span className='order-into'>{gettext('Total Amount')}</span>
              <span className='order-price'>
                {originalTotalAmount !== totalAmount &&
                  <span style={{ fontSize: 'small', textDecoration: 'line-through', color: '#9a9a9a' }}>{'￥' + originalTotalAmount}</span>
                }
                <span>{'￥' + totalAmount + ' '}</span>
              </span>
            </dd>
          </div>
        </dl>
        <Button className='subscription-submit' color="primary" onClick={this.onPay}>{gettext('Submit Order')}</Button>
      </div>
    );
  };

  renderAddUser = () => {
    let { currentPlan, count } = this.state;
    let operationIntro = gettext('Add Users');
    let originalTotalAmount = count * currentPlan.price_per_user;
    let totalAmount = originalTotalAmount;
    return (
      <div className='d-flex flex-column subscription-container price-version-container-header subscription-add-user'>
        <div className="price-version-container-top"></div>
        <h3 className='user-quota-plan-name py-5'>{currentPlan.name}</h3>
        <span className='py-2 mb-0 text-orange font-500 text-center'>
          {'¥ '}<span className="price-version-plan-price">{currentPlan.price}</span>{' ' + currentPlan.description}
        </span>
        <InputGroup style={{ marginBottom: '5px' }} className='user-numbers'>
          <InputGroupAddon addonType="prepend">
            <InputGroupText>{operationIntro}</InputGroupText>
          </InputGroupAddon>
          <Input
            className="py-2"
            placeholder={operationIntro}
            title={operationIntro}
            type="number"
            value={count || 1}
            min="1"
            max="9999"
            disabled={!currentPlan.can_custom_count}
            onChange={this.onCountInputChange}
          />
        </InputGroup>
        <span className='py-2 text-orange mb-0 font-500 price-version-plan-whole-price text-center'>
          {gettext('Total') + ' ¥ ' + totalAmount}
          {originalTotalAmount !== totalAmount &&
            <span style={{ fontSize: 'small', textDecoration: 'line-through', color: '#9a9a9a' }}>{' ￥' + originalTotalAmount}</span>
          }
        </span>
        <span className='py-2 mb-0 text-lg-size font-500 price-version-plan-valid-day text-center'>{gettext('Valid Until') + ' ' + currentPlan.new_term_end}</span>
        <span className='subscription-notice text-center py-5'>{gettext('When the remaining subscription time is shorter than the selected plan, added users are charged proportionally by day.')}</span>
        <Button className='subscription-submit' onClick={this.onPay} color="primary">{gettext('Buy Now')}</Button>
      </div>
    );
  };

  renderBuyQuota = () => {
    let { currentPlan, assetQuotaUnitCount } = this.state;
    let operationIntro = gettext('Add Storage');
    let originalTotalAmount = assetQuotaUnitCount * currentPlan.price_per_asset_quota_unit;
    let totalAmount = originalTotalAmount;
    return (
      <div className='d-flex flex-column subscription-container price-version-container-header subscription-add-space'>
        <div className="price-version-container-top"></div>
        <h3 className='user-quota-plan-name py-5'>{currentPlan.name}</h3>
        <span className='py-2 mb-0 text-orange font-500 text-center'>
          {'¥ '}<span className="price-version-plan-price">{currentPlan.asset_quota_price}</span>{' ' + currentPlan.asset_quota_description}
        </span>
        <InputGroup style={{ marginBottom: '5px' }} className='space-quota'>
          <InputGroupAddon addonType="prepend">
            <InputGroupText><span className="font-500">{operationIntro}</span></InputGroupText>
          </InputGroupAddon>
          <Input
            className="py-2"
            placeholder={operationIntro}
            title={operationIntro}
            type="number"
            value={assetQuotaUnitCount || 1}
            min="1"
            max="9999"
            disabled={!currentPlan.can_custom_asset_quota}
            onChange={this.onAssetQuotaUnitCountInputChange}
          />
          <InputGroupAddon addonType='append'>
            <InputGroupText><span className="font-500">{' x ' + currentPlan.asset_quota_unit + 'GB'}</span></InputGroupText>
          </InputGroupAddon>
        </InputGroup>
        <span className='py-4 text-orange mb-0 font-500 price-version-plan-whole-price text-center'>
          {gettext('Total') + ' ¥ ' + totalAmount}
          {originalTotalAmount !== totalAmount &&
            <span style={{ fontSize: 'small', textDecoration: 'line-through', color: '#9a9a9a' }}>{' ￥' + originalTotalAmount}</span>
          }
        </span>
        <span className='py-2 mb-0 text-lg-size font-500 price-version-plan-valid-day text-center'>{gettext('Valid Until') + ' ' + currentPlan.new_term_end}</span>
        <span className='subscription-notice text-center py-5'>{gettext('When the remaining subscription time is shorter than the selected plan, added storage is charged proportionally by day.')}</span>
        <Button className='subscription-submit' onClick={this.onPay} color="primary">{gettext('Buy Now')}</Button>
      </div>
    );
  };

  render() {
    let { paymentType } = this.props;
    if (paymentType === 'paid' || paymentType === 'extend_time') {
      return this.renderPaidOrExtendTime();
    } else if (paymentType === 'add_user') {
      return this.renderAddUser();
    } else if (paymentType === 'buy_quota') {
      return this.renderBuyQuota();
    } else {
      toaster.danger(gettext('Internal Server Error'));
      return;
    }
  }
}

Plans.propTypes = PlansPropTypes;

const PlansDialogPropTypes = {
  isOrgContext: PropTypes.bool.isRequired,
  paymentType: PropTypes.string.isRequired,
  paymentTypeTrans: PropTypes.string.isRequired,
  toggleDialog: PropTypes.func.isRequired,
};

class PlansDialog extends Component {

  constructor(props) {
    super(props);
    this.state = {
      isLoading: true,
      isWaiting: false,
      planList: [],
      paymentSourceList: [],
    };
  }

  getPlans = () => {
    subscriptionAPI.getSubscriptionPlans(this.props.paymentType).then((res) => {
      this.setState({
        planList: res.data.plan_list,
        paymentSourceList: res.data.payment_source_list,
        isLoading: false,
      });
    }).catch(error => {
      let errorMsg = Utils.getErrorMsg(error);
      this.setState({
        isLoading: false,
        errorMsg: errorMsg,
      });
    });
  };

  onPay = (planID, count, asset_quota, totalAmount) => {
    this.setState({ isWaiting: true });
    let payUrl = serviceURL + '/subscription/pay/?payment_source=' + this.state.paymentSourceList[0] +
      '&payment_type=' + this.props.paymentType + '&plan_id=' + planID +
      '&total_amount=' + totalAmount;
    if (count) {
      payUrl += '&count=' + count;
    }
    if (asset_quota) {
      payUrl += '&asset_quota=' + asset_quota;
    }
    window.open(payUrl);
  };

  onReload = () => {
    window.location.reload();
  };

  componentDidMount() {
    this.getPlans();
  }

  render() {
    const { isLoading, isWaiting, planList } = this.state;
    const { toggleDialog, paymentTypeTrans } = this.props;

    if (isLoading) {
      return (
        <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content">
              <div className="modal-header">
                <h5 className="modal-title">{paymentTypeTrans}</h5>
                <button type="button" className="close" onClick={toggleDialog} aria-label="Close">
                  <span aria-hidden="true">&times;</span>
                </button>
              </div>
              <div className="modal-body">
                <Loading />
              </div>
            </div>
          </div>
        </div>
      );
    }
    if (isWaiting) {
      return (
        <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
          <div className="modal-dialog modal-dialog-centered">
            <div className="modal-content">
              <div className="modal-header">
                <h5 className="modal-title">{paymentTypeTrans}</h5>
                <button type="button" className="close" onClick={this.onReload} aria-label="Close">
                  <span aria-hidden="true">&times;</span>
                </button>
              </div>
              <div className="modal-body">
                <div>{gettext('Has the payment been completed?')}</div>
              </div>
              <div className="modal-footer">
                <button className="btn btn-outline-primary" onClick={this.onReload}>{gettext('Yes')}</button>
              </div>
            </div>
          </div>
        </div>
      );
    }
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{paymentTypeTrans}</h5>
              <button type="button" className="close" onClick={toggleDialog} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              <div className="d-flex justify-content-between">
                <Plans
                  plans={planList}
                  onPay={this.onPay}
                  paymentType={this.props.paymentType}
                />
              </div>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

PlansDialog.propTypes = PlansDialogPropTypes;

const propTypes = {
  isOrgContext: PropTypes.bool.isRequired,
  handleContentScroll: PropTypes.func,
};

class Subscription extends Component {

  constructor(props) {
    super(props);
    this.paymentTypeTransMap = {
      paid: gettext('Buy Now'),
      extend_time: gettext('Renew Now'),
      add_user: gettext('Add Users'),
      buy_quota: gettext('Add Storage'),
    };
    this.state = {
      isLoading: true,
      errorMsg: '',
      isDialogOpen: false,
      subscriptionData: null,
      planName: this.props.isOrgContext ? '团队版' : '个人版',
      userLimit: 20,
      assetQuota: 1,
      termEnd: '长期',
      subscription: null,
      paymentTypeList: [],
      currentPaymentType: '',
      errorMsgCode: ''
    };
  }

  getSubscription = () => {
    subscriptionAPI.getSubscription().then((res) => {
      const data = res.data || {};
      const paymentTypeList = Array.isArray(data.payment_type_list) ? data.payment_type_list : [];

      if (Object.prototype.hasOwnProperty.call(data, 'storage_quota') || Object.prototype.hasOwnProperty.call(data, 'traffic_quota')) {
        this.setState({
          isLoading: false,
          errorMsg: '',
          subscriptionData: data,
          paymentTypeList: paymentTypeList,
          subscription: null,
          planName: formatPlanName(data.plan, this.props.isOrgContext),
          userLimit: data.max_users,
          assetQuota: data.storage_quota > 0 ? Math.round(data.storage_quota / BYTES_IN_GB) : 0,
          termEnd: data.traffic_reset_date || '--',
        });
        return;
      }

      const subscription = data.subscription;
      if (!subscription) {
        this.setState({
          isLoading: false,
          subscriptionData: null,
          paymentTypeList: paymentTypeList,
        });
      } else {
        let isActive = subscription.is_active;
        let plan = subscription.plan;
        this.setState({
          isLoading: false,
          subscriptionData: null,
          subscription,
          planName: plan.name,
          userLimit: subscription.user_limit,
          assetQuota: isActive ? subscription.asset_quota : plan.asset_quota,
          termEnd: isActive ? subscription.term_end : '已过期',
          paymentTypeList: paymentTypeList,
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

  toggleDialog = () => {
    this.setState({ isDialogOpen: !this.state.isDialogOpen });
  };

  togglePaymentType = (paymentType) => {
    this.setState({ currentPaymentType: paymentType });
    this.toggleDialog();
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
          <p className="mb-2">{formatPlanName(subscriptionData.plan, this.props.isOrgContext)}</p>
        </div>
        {this.props.isOrgContext &&
          <div id="user-limit" className="subscription-info">
            <h3 className="subscription-info-heading">{gettext('User Limit')}</h3>
            <p className="mb-2">{formatUserLimit(currentUsers, maxUsers)}</p>
          </div>
        }
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
    const { isLoading, errorMsg, planName, userLimit, assetQuota, termEnd,
      isDialogOpen, paymentTypeList, currentPaymentType, subscriptionData } = this.state;
    if (isLoading) {
      return <Loading />;
    }
    if (errorMsg) {
      return <p className="text-center mt-8 error">{errorMsg}</p>;
    }
    return (
      <Fragment>
        <div className="content subscription-content position-relative" onScroll={this.props.handleContentScroll}>
          {subscriptionData ? this.renderCurrentSubscription() : (
            <Fragment>
              <div id="current-plan" className="subscription-info">
                <h3 className="subscription-info-heading">{gettext('Current Plan')}</h3>
                <p className="mb-2">{planName}</p>
              </div>
              {this.props.isOrgContext &&
                <div id="user-limit" className="subscription-info">
                  <h3 className="subscription-info-heading">{gettext('User Limit')}</h3>
                  <p className="mb-2">{userLimit}</p>
                </div>
              }
              <div id="asset-quota" className="subscription-info">
                <h3 className="subscription-info-heading">{gettext('Storage')}</h3>
                <p className="mb-2">{assetQuota ? assetQuota + 'GB' : '1GB'}</p>
              </div>
              <div id="current-subscription-period" className="subscription-info">
                <h3 className="subscription-info-heading">{gettext('Billing Cycle')}</h3>
                <p className="mb-2">{termEnd}</p>
              </div>
            </Fragment>
          )}
          <div id="product-price" className="subscription-info">
            <h3 className="subscription-info-heading">{gettext('Billing Details')}</h3>
            {subscriptionDetailsUrl && (
              <p className="mb-2">
                <a rel="noopener noreferrer" target="_blank" href={subscriptionDetailsUrl}>{gettext('View Details')}</a>
              </p>
            )}
            {subscriptionData && paymentTypeList.length === 0 &&
              <p className="mb-2 text-secondary">{gettext('Plan changes are managed by the billing service.')}</p>
            }
          </div>
          {paymentTypeList.map((item, index) => {
            let name = this.paymentTypeTransMap[item];
            return (
              <button
                key={index}
                className="btn btn-outline-primary mr-4"
                onClick={this.togglePaymentType.bind(this, item)}
              >{name}</button>
            );
          })}
          {!this.state.subscription &&
            <div id="sales-consultant" className="subscription-info mt-6">
              <h3 className="subscription-info-heading">{gettext('Sales Contact')}</h3>
              <img className="mb-2" src="/media/img/qr-sale.png" alt="" width="112"></img>
              <p className="mb-2">{gettext('Scan to contact sales')}</p>
            </div>
          }
        </div>
        {isDialogOpen &&
          <PlansDialog
            paymentType={currentPaymentType}
            paymentTypeTrans={this.paymentTypeTransMap[currentPaymentType]}
            isOrgContext={this.props.isOrgContext}
            toggleDialog={this.toggleDialog}
          />
        }
      </Fragment>
    );
  }
}

Subscription.propTypes = propTypes;

export default Subscription;
