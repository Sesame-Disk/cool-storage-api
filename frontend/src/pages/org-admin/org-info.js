import React, { Component, Fragment } from 'react';
import { seafileAPI } from '../../utils/seafile-api';
import { gettext, orgMemberQuotaEnabled } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import toaster from '../../components/toast';
import SetOrgTrafficQuota from '../../components/dialog/set-org-traffic-quota';
import MainPanelTopbar from './main-panel-topbar';

class OrgInfo extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      isSetTrafficQuotaDialogOpen: false,
      org_name: '',
      plan: '',
      billing_cycle: '',
      storage_quota: 0,
      storage_usage: 0,
      traffic_quota: 0,
      traffic_combined_used: 0,
      traffic_upload_quota: 0,
      traffic_upload_used: 0,
      traffic_download_quota: 0,
      traffic_download_used: 0,
      max_users: 0,
      member_quota: 0,
      member_usage: 0,
      active_members: 0
    };
  }

  componentDidMount() {
    seafileAPI.orgAdminGetOrgInfo().then(res => {
      this.setState({
        loading: false,
        org_name: res.data.org_name,
        plan: res.data.plan,
        billing_cycle: res.data.billing_cycle,
        storage_quota: res.data.storage_quota,
        storage_usage: res.data.storage_usage,
        traffic_quota: res.data.traffic_quota,
        traffic_combined_used: res.data.traffic_combined_used,
        traffic_upload_quota: res.data.traffic_upload_quota,
        traffic_upload_used: res.data.traffic_upload_used,
        traffic_download_quota: res.data.traffic_download_quota,
        traffic_download_used: res.data.traffic_download_used,
        max_users: res.data.max_users,
        member_quota: res.data.member_quota,
        member_usage: res.data.member_usage,
        active_members: res.data.active_members
      });
    }).catch((error) => {
      this.setState({
        loading: false,
        errorMsg: Utils.getErrorMsg(error, true)
      });
    });
  }

  toggleSetTrafficQuotaDialog = () => {
    this.setState({ isSetTrafficQuotaDialogOpen: !this.state.isSetTrafficQuotaDialogOpen });
  };

  showEditIcon = (action) => {
    return (
      <span
        title={gettext('Edit')}
        className="fa fa-pencil-alt attr-action-icon"
        onClick={action}>
      </span>
    );
  };

  updateTrafficQuota = (updates) => {
    return seafileAPI.orgAdminUpdateOrgInfo(updates).then(() => {
      this.setState(updates);
      toaster.success(gettext('Successfully updated organization traffic quotas.'));
    }).catch((error) => {
      const errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
      return Promise.reject(errMessage);
    });
  };

  render() {
    const { loading, errorMsg, isSetTrafficQuotaDialogOpen } = this.state;
    if (loading) {
      return (
        <Fragment>
          <MainPanelTopbar />
          <div className="main-panel-center flex-row">
            <div className="cur-view-container">
              <div className="cur-view-content">
                <p>{gettext('Loading...')}</p>
              </div>
            </div>
          </div>
        </Fragment>
      );
    }

    if (errorMsg) {
      return (
        <Fragment>
          <MainPanelTopbar />
          <div className="main-panel-center flex-row">
            <div className="cur-view-container">
              <div className="cur-view-content">
                <p className="error text-center mt-4">{errorMsg}</p>
              </div>
            </div>
          </div>
        </Fragment>
      );
    }

    const memberQuota = this.state.max_users > 0 ? this.state.max_users : this.state.member_quota;
    const formatQuota = (used, quota) => {
      return quota > 0 ? `${Utils.bytesToSize(used)} / ${Utils.bytesToSize(quota)}` : `${Utils.bytesToSize(used)} / ${gettext('Unlimited')}`;
    };

    return (
      <Fragment>
        <MainPanelTopbar />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <div className="cur-view-path">
              <h3 className="sf-heading">{gettext('Info')}</h3>
            </div>
            <div className="cur-view-content">
              <dl>
                <strong>{this.state.org_name}</strong>
                <dt>{gettext('Plan')}</dt>
                <dd>{this.state.plan || '--'}</dd>

                <dt>{gettext('Billing Cycle')}</dt>
                <dd>{this.state.billing_cycle || '--'}</dd>

                <dt>{gettext('Space Used')}</dt>

                {(this.state.storage_quota > 0) ? <dd>{Utils.bytesToSize(this.state.storage_usage)} / {Utils.bytesToSize(this.state.storage_quota)}</dd> : <dd>{Utils.bytesToSize(this.state.storage_usage)}</dd>}

                <dt>{gettext('Combined Monthly Traffic')}</dt>
                <dd>
                  {formatQuota(this.state.traffic_combined_used, this.state.traffic_quota)}
                  {this.showEditIcon(this.toggleSetTrafficQuotaDialog)}
                </dd>

                <dt>{gettext('Monthly Upload Traffic')}</dt>
                <dd>
                  {formatQuota(this.state.traffic_upload_used, this.state.traffic_upload_quota)}
                  {this.showEditIcon(this.toggleSetTrafficQuotaDialog)}
                </dd>

                <dt>{gettext('Monthly Download Traffic')}</dt>
                <dd>
                  {formatQuota(this.state.traffic_download_used, this.state.traffic_download_quota)}
                  {this.showEditIcon(this.toggleSetTrafficQuotaDialog)}
                </dd>

                {orgMemberQuotaEnabled ? <dt>{gettext('Active Users')} / {gettext('Total Users')} / {gettext('Limits')}</dt> : <dt>{gettext('Active Users')} / {gettext('Total Users')}</dt>}

                {orgMemberQuotaEnabled ? <dd>{(this.state.active_members > 0) ? this.state.active_members : '--'} / {(this.state.member_usage > 0) ? this.state.member_usage : '--'} / {(memberQuota > 0) ? memberQuota : '--'}</dd> : <dd>{this.state.active_members > 0 ? this.state.active_members : '--'} / {this.state.member_usage > 0 ? this.state.member_usage : '--'}</dd>}

              </dl>
              {isSetTrafficQuotaDialogOpen &&
                <SetOrgTrafficQuota
                  trafficQuota={this.state.traffic_quota}
                  trafficUploadQuota={this.state.traffic_upload_quota}
                  trafficDownloadQuota={this.state.traffic_download_quota}
                  updateQuota={this.updateTrafficQuota}
                  toggleDialog={this.toggleSetTrafficQuotaDialog}
                />
              }
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

export default OrgInfo;
