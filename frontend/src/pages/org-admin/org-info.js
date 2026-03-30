import React, { Component, Fragment } from 'react';
import { Button } from 'reactstrap';
import { seafileAPI } from '../../utils/seafile-api';
import { gettext, isOrgOwner, orgID, orgMemberQuotaEnabled, subscriptionDetailsUrl, username } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import MainPanelTopbar from './main-panel-topbar';
import TransferOrgOwnershipDialog from '../../components/dialog/transfer-org-ownership-dialog';
import toaster from '../../components/toast';

class OrgInfo extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      org_name: '',
      plan: '',
      billing_cycle: '',
      repos_count: 0,
      groups_count: 0,
      total_files_count: 0,
      storage_quota: 0,
      storage_usage: 0,
      traffic_quota: 0,
      traffic_combined_used: 0,
      traffic_upload_quota: 0,
      traffic_upload_used: 0,
      traffic_download_quota: 0,
      traffic_download_used: 0,
      traffic_year_total: 0,
      traffic_year_upload: 0,
      traffic_year_download: 0,
      max_users: 0,
      member_quota: 0,
      member_usage: 0,
      active_members: 0,
      isTransferOwnershipDialogOpen: false,
      canTransferOwnership: isOrgOwner,
    };
  }

  toggleTransferOwnershipDialog = () => {
    this.setState({ isTransferOwnershipDialogOpen: !this.state.isTransferOwnershipDialogOpen });
  };

  searchOrgAdmins = (query) => {
    return seafileAPI.orgAdminSearchUser(orgID, query, 1, 25, 'active').then((res) => {
      const users = (res.data.user_list || []).filter((user) => user.is_org_staff);
      return { data: { users } };
    });
  };

  transferOwnership = (newOwnerEmail) => {
    seafileAPI.orgAdminTransferOwnership(orgID, newOwnerEmail).then(() => {
      this.setState({
        isTransferOwnershipDialogOpen: false,
        canTransferOwnership: false,
      });
      toaster.success(gettext('Organization ownership transferred successfully.'));
    }).catch((error) => {
      toaster.danger(Utils.getErrorMsg(error));
    });
  };

  componentDidMount() {
    seafileAPI.orgAdminGetOrgInfo().then(res => {
      this.setState({
        loading: false,
        org_name: res.data.org_name,
        plan: res.data.plan,
        billing_cycle: res.data.billing_cycle,
        repos_count: res.data.repos_count,
        groups_count: res.data.groups_count,
        total_files_count: res.data.total_files_count,
        storage_quota: res.data.storage_quota,
        storage_usage: res.data.storage_usage,
        traffic_quota: res.data.traffic_quota,
        traffic_month_total: res.data.traffic_month_total,
        traffic_month_upload: res.data.traffic_month_upload,
        traffic_month_download: res.data.traffic_month_download,
        traffic_combined_used: res.data.traffic_combined_used,
        traffic_upload_quota: res.data.traffic_upload_quota,
        traffic_upload_used: res.data.traffic_upload_used,
        traffic_download_quota: res.data.traffic_download_quota,
        traffic_download_used: res.data.traffic_download_used,
        traffic_year_total: res.data.traffic_year_total,
        traffic_year_upload: res.data.traffic_year_upload,
        traffic_year_download: res.data.traffic_year_download,
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

  render() {
    const { loading, errorMsg, canTransferOwnership, isTransferOwnershipDialogOpen } = this.state;
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
    const formatTrafficBreakdown = (total, upload, download) => {
      return `${Utils.bytesToSize(total || 0)} / ${Utils.bytesToSize(upload || 0)} / ${Utils.bytesToSize(download || 0)}`;
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
              {canTransferOwnership && (
                <div className="mb-3">
                  <Button color="outline-primary" onClick={this.toggleTransferOwnershipDialog}>
                    {gettext('Transfer ownership')}
                  </Button>
                </div>
              )}
              <dl>
                <strong>{this.state.org_name}</strong>
                <dt>{gettext('Plan')}</dt>
                <dd>{this.state.plan || '--'}</dd>

                <dt>{gettext('Billing Cycle')}</dt>
                <dd>{this.state.billing_cycle || '--'}</dd>

                <dt>{gettext('Libraries')} / {gettext('Files')} / {gettext('Groups')}</dt>
                <dd>{this.state.repos_count || 0} / {this.state.total_files_count || 0} / {this.state.groups_count || 0}</dd>

                <dt>{gettext('Space Used')}</dt>

                {(this.state.storage_quota > 0) ? <dd>{Utils.bytesToSize(this.state.storage_usage)} / {Utils.bytesToSize(this.state.storage_quota)}</dd> : <dd>{Utils.bytesToSize(this.state.storage_usage)}</dd>}

                <dt>{gettext('Combined Monthly Traffic')}</dt>
                <dd>{formatQuota(this.state.traffic_combined_used, this.state.traffic_quota)}</dd>

                <dt>{gettext('Monthly Upload Traffic')}</dt>
                <dd>{formatQuota(this.state.traffic_upload_used, this.state.traffic_upload_quota)}</dd>

                <dt>{gettext('Monthly Download Traffic')}</dt>
                <dd>{formatQuota(this.state.traffic_download_used, this.state.traffic_download_quota)}</dd>

                <dt>{gettext('This Month Traffic')} ({gettext('Total')} / {gettext('Upload')} / {gettext('Download')})</dt>
                <dd>{formatTrafficBreakdown(this.state.traffic_month_total, this.state.traffic_month_upload, this.state.traffic_month_download)}</dd>

                <dt>{gettext('This Year Traffic')} ({gettext('Total')} / {gettext('Upload')} / {gettext('Download')})</dt>
                <dd>{formatTrafficBreakdown(this.state.traffic_year_total, this.state.traffic_year_upload, this.state.traffic_year_download)}</dd>

                {orgMemberQuotaEnabled ? <dt>{gettext('Active Users')} / {gettext('Total Users')} / {gettext('Limits')}</dt> : <dt>{gettext('Active Users')} / {gettext('Total Users')}</dt>}

                {orgMemberQuotaEnabled ? <dd>{(this.state.active_members > 0) ? this.state.active_members : '--'} / {(this.state.member_usage > 0) ? this.state.member_usage : '--'} / {(memberQuota > 0) ? memberQuota : '--'}</dd> : <dd>{this.state.active_members > 0 ? this.state.active_members : '--'} / {this.state.member_usage > 0 ? this.state.member_usage : '--'}</dd>}

                {subscriptionDetailsUrl && (
                  <Fragment>
                    <dt>{gettext('Billing Details')}</dt>
                    <dd>
                      <a rel="noopener noreferrer" target="_blank" href={subscriptionDetailsUrl}>{gettext('View Details')}</a>
                    </dd>
                  </Fragment>
                )}

              </dl>
            </div>
          </div>
        </div>
        {isTransferOwnershipDialogOpen && (
          <TransferOrgOwnershipDialog
            currentOwner={username}
            searchFunc={this.searchOrgAdmins}
            onSubmit={this.transferOwnership}
            toggleDialog={this.toggleTransferOwnershipDialog}
          />
        )}
      </Fragment>
    );
  }
}

export default OrgInfo;
