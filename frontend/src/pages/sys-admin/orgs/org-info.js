import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { Row, Col } from 'reactstrap';
import { Utils } from '../../../utils/utils';
import { seafileAPI } from '../../../utils/seafile-api';
import { gettext, serviceURL } from '../../../utils/constants';
import toaster from '../../../components/toast';
import Loading from '../../../components/loading';
import SysAdminSetOrgQuotaDialog from '../../../components/dialog/sysadmin-dialog/set-org-traffic-quotas';
import SysAdminSetOrgNameDialog from '../../../components/dialog/sysadmin-dialog/sysadmin-set-org-name-dialog';
import SysAdminSetOrgMaxUserNumberDialog from '../../../components/dialog/sysadmin-dialog/sysadmin-set-org-max-user-number-dialog';
import SetOrgStoragePolicyDialog from '../../../components/dialog/set-org-storage-policy-dialog';
import TransferOrgOwnershipDialog from '../../../components/dialog/transfer-org-ownership-dialog';
import MainPanelTopbar from '../main-panel-topbar';
import OrgNav from './org-nav';
import { formatStoragePolicyLabel } from '../../../utils/storage-policy';

class Content extends Component {

  constructor(props) {
    super(props);
    this.state = {
      isSetQuotaDialogOpen: false,
      isSetNameDialogOpen: false,
      isSetMaxUserNumberDialogOpen: false,
      isSetStoragePolicyDialogOpen: false,
      isTransferOwnershipDialogOpen: false
    };
  }

  toggleSetQuotaDialog = () => {
    this.setState({ isSetQuotaDialogOpen: !this.state.isSetQuotaDialogOpen });
  };

  toggleSetNameDialog = () => {
    this.setState({ isSetNameDialogOpen: !this.state.isSetNameDialogOpen });
  };

  toggleSetMaxUserNumberDialog = () => {
    this.setState({ isSetMaxUserNumberDialogOpen: !this.state.isSetMaxUserNumberDialogOpen });
  };

  toggleSetStoragePolicyDialog = () => {
    this.setState({ isSetStoragePolicyDialogOpen: !this.state.isSetStoragePolicyDialogOpen });
  };

  toggleTransferOwnershipDialog = () => {
    this.setState({ isTransferOwnershipDialogOpen: !this.state.isTransferOwnershipDialogOpen });
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

  render() {
    const { loading, errorMsg } = this.props;
    if (loading) {
      return <Loading />;
    } else if (errorMsg) {
      return <p className="error text-center">{errorMsg}</p>;
    } else {
      const {
        org_name, users_count, max_user_number, groups_count,
        quota, quota_usage, traffic_quota, traffic_upload_quota, traffic_download_quota,
        traffic_combined_used, traffic_upload_used, traffic_download_used,
        plan, billing_cycle, owner_email, owner_name, enable_saml_login, metadata_url, domain,
        storage_policy, available_storage_regions, available_storage_region_labels,
      } = this.props.orgInfo;
      const { isSetQuotaDialogOpen, isSetNameDialogOpen, isSetMaxUserNumberDialogOpen, isSetStoragePolicyDialogOpen, isTransferOwnershipDialogOpen } = this.state;
      const formatTrafficQuota = (used, limit) => {
        return `${Utils.bytesToSize(used || 0)} / ${limit > 0 ? Utils.bytesToSize(limit) : gettext('Unlimited')}`;
      };
      return (
        <Fragment>
          <dl className="m-0">
            <dt className="info-item-heading">{gettext('Name')}</dt>
            <dd className="info-item-content">
              {org_name}
              {this.showEditIcon(this.toggleSetNameDialog)}
            </dd>

            <dt className="info-item-heading">{gettext('Owner')}</dt>
            <dd className="info-item-content">
              {owner_name || owner_email || '--'}
              <button type="button" className="btn btn-link btn-sm ml-2 p-0 align-baseline" onClick={this.toggleTransferOwnershipDialog}>
                {gettext('Transfer ownership')}
              </button>
            </dd>

            <dt className="info-item-heading">{gettext('Number of members')}</dt>
            <dd className="info-item-content">{users_count}</dd>

            {max_user_number > 0 &&
              <Fragment>
                <dt className="info-item-heading">{gettext('Max number of members')}</dt>
                <dd className="info-item-content">
                  {max_user_number}
                  {this.showEditIcon(this.toggleSetMaxUserNumberDialog)}
                </dd>
              </Fragment>
            }

            <dt className="info-item-heading">{gettext('Number of groups')}</dt>
            <dd className="info-item-content">{groups_count}</dd>

            <dt className="info-item-heading">{gettext('Plan')}</dt>
            <dd className="info-item-content">{plan || '--'}</dd>

            <dt className="info-item-heading">{gettext('Billing Cycle')}</dt>
            <dd className="info-item-content">{billing_cycle || '--'}</dd>

            <dt className="info-item-heading">{gettext('Storage Policy')}</dt>
            <dd className="info-item-content">
              {formatStoragePolicyLabel(storage_policy, available_storage_region_labels, gettext)}
              {this.showEditIcon(this.toggleSetStoragePolicyDialog)}
            </dd>

            <dt className="info-item-heading">{gettext('Space Used')}</dt>
            <dd className="info-item-content">
              {`${Utils.bytesToSize(quota_usage)} / ${quota > 0 ? Utils.bytesToSize(quota) : '--'}`}
              {this.showEditIcon(this.toggleSetQuotaDialog)}
            </dd>

            <dt className="info-item-heading">{gettext('Combined Monthly Traffic')}</dt>
            <dd className="info-item-content">{formatTrafficQuota(traffic_combined_used, traffic_quota)}</dd>

            <dt className="info-item-heading">{gettext('Monthly Upload Traffic')}</dt>
            <dd className="info-item-content">{formatTrafficQuota(traffic_upload_used, traffic_upload_quota)}</dd>

            <dt className="info-item-heading">{gettext('Monthly Download Traffic')}</dt>
            <dd className="info-item-content">{formatTrafficQuota(traffic_download_used, traffic_download_quota)}</dd>
            {enable_saml_login &&
              <Fragment>
                <dt className="info-item-heading">{gettext('SAML Config')}</dt>
                <dd className="info-item-content">
                  <Row className="my-4">
                    <Col md="4">Identifier (Entity ID)</Col>
                    <Col md="6">{`${serviceURL}/org/custom/${this.props.orgID}/saml2/metadata/`}</Col>
                  </Row>
                </dd>
                <dd className="info-item-content">
                  <Row className="my-4">
                    <Col md="4">Reply URL (Assertion Consumer Service URL)</Col>
                    <Col md="6">{`${serviceURL}/org/custom/${this.props.orgID}/saml2/acs/`}</Col>
                  </Row>
                </dd>
                <dd className="info-item-content">
                  <Row className="my-4">
                    <Col md="4">SAML App Federation Metadata URL</Col>
                    <Col md="6">{metadata_url}</Col>
                  </Row>
                </dd>
                <dd className="info-item-content">
                  <Row className="my-4">
                    <Col md="4">{gettext('Email Domain')}</Col>
                    <Col md="6">{domain}</Col>
                  </Row>
                </dd>
              </Fragment>
            }
          </dl>
          {isSetQuotaDialogOpen &&
            <SysAdminSetOrgQuotaDialog
              storageQuota={quota}
              trafficQuota={traffic_quota}
              trafficUploadQuota={traffic_upload_quota}
              trafficDownloadQuota={traffic_download_quota}
              updateQuota={this.props.updateQuota}
              toggleDialog={this.toggleSetQuotaDialog}
            />
          }
          {isSetNameDialogOpen &&
            <SysAdminSetOrgNameDialog
              name={org_name}
              updateName={this.props.updateName}
              toggle={this.toggleSetNameDialog}
            />
          }
          {isSetMaxUserNumberDialogOpen &&
            <SysAdminSetOrgMaxUserNumberDialog
              value={max_user_number}
              updateValue={this.props.updateMaxUserNumber}
              toggle={this.toggleSetMaxUserNumberDialog}
            />
          }
          {isSetStoragePolicyDialogOpen &&
            <SetOrgStoragePolicyDialog
              policy={storage_policy}
              availableRegions={available_storage_regions || []}
              availableRegionLabels={available_storage_region_labels || {}}
              updatePolicy={this.props.updateStoragePolicy}
              toggleDialog={this.toggleSetStoragePolicyDialog}
            />
          }
          {isTransferOwnershipDialogOpen &&
            <TransferOrgOwnershipDialog
              currentOwner={owner_email}
              searchFunc={this.props.searchOrgAdmins}
              onSubmit={this.props.transferOwnership}
              toggleDialog={this.toggleTransferOwnershipDialog}
            />
          }
        </Fragment>
      );
    }
  }
}

Content.propTypes = {
  loading: PropTypes.bool.isRequired,
  errorMsg: PropTypes.string.isRequired,
  items: PropTypes.array.isRequired,
  getDeviceErrorsListByPage: PropTypes.func,
  resetPerPage: PropTypes.func,
  curPerPage: PropTypes.number,
  orgID: PropTypes.string,
  orgInfo: PropTypes.object,
  updateQuota: PropTypes.func.isRequired,
  updateName: PropTypes.func.isRequired,
  updateMaxUserNumber: PropTypes.func.isRequired,
  updateStoragePolicy: PropTypes.func.isRequired,
  searchOrgAdmins: PropTypes.func.isRequired,
  transferOwnership: PropTypes.func.isRequired,
};

class OrgInfo extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      orgInfo: {}
    };
  }

  componentDidMount() {
    seafileAPI.sysAdminGetOrg(this.props.orgID).then((res) => {
      this.setState({
        loading: false,
        orgInfo: res.data
      });
    }).catch((error) => {
      this.setState({
        loading: false,
        errorMsg: Utils.getErrorMsg(error, true) // true: show login tip if 403
      });
    });
  }

  updateQuota = (updates) => {
    return seafileAPI.sysAdminUpdateOrg(this.props.orgID, updates).then(() => {
      this.setState((prevState) => ({
        orgInfo: Object.assign({}, prevState.orgInfo, {
          quota: updates.storage_quota,
          storage_quota: updates.storage_quota,
          traffic_quota: updates.traffic_quota,
          traffic_upload_quota: updates.traffic_upload_quota,
          traffic_download_quota: updates.traffic_download_quota,
        })
      }));
      toaster.success(gettext('Successfully updated organization quotas.'));
    }).catch((error) => {
      const errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
      return Promise.reject(errMessage);
    });
  };

  updateName = (orgName) => {
    const data = { name: orgName };
    seafileAPI.sysAdminUpdateOrg(this.props.orgID, data).then(() => {
      this.setState((prevState) => ({
        orgInfo: Object.assign({}, prevState.orgInfo, { org_name: orgName })
      }));
      toaster.success(gettext('Successfully set name.'));
    }).catch((error) => {
      const errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  updateMaxUserNumber = (newValue) => {
    const data = { max_users: parseInt(newValue, 10) };
    seafileAPI.sysAdminUpdateOrg(this.props.orgID, data).then(() => {
      this.setState((prevState) => ({
        orgInfo: Object.assign({}, prevState.orgInfo, {
          max_user_number: newValue,
          max_users: newValue,
        })
      }));
      toaster.success(gettext('Successfully set max number of members.'));
    }).catch((error) => {
      const errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  updateStoragePolicy = (storagePolicy) => {
    return seafileAPI.sysAdminUpdateOrg(this.props.orgID, { storage_policy: storagePolicy }).then(() => {
      this.setState((prevState) => ({
        orgInfo: Object.assign({}, prevState.orgInfo, { storage_policy: storagePolicy })
      }));
      toaster.success(gettext('Successfully updated storage policy.'));
    }).catch((error) => {
      const errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
      return Promise.reject(errMessage);
    });
  };

  searchOrgAdmins = (query) => {
    return seafileAPI.sysAdminSearchUsers(query, 1, 25, this.props.orgID).then((res) => {
      const users = (res.data.users || res.data.user_list || []).filter((user) => user.is_staff || user.is_org_staff || user.role === 'owner');
      return { data: { users } };
    });
  };

  transferOwnership = (newOwnerEmail) => {
    return seafileAPI.sysAdminTransferOrgOwnership(this.props.orgID, newOwnerEmail).then(() => {
      return seafileAPI.sysAdminGetOrg(this.props.orgID).then((res) => {
        this.setState({ orgInfo: res.data });
        toaster.success(gettext('Organization ownership transferred successfully.'));
      });
    }).catch((error) => {
      const errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
      return Promise.reject(errMessage);
    });
  };

  render() {
    const { orgInfo } = this.state;
    return (
      <Fragment>
        <MainPanelTopbar {...this.props} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container">
            <OrgNav currentItem="info" orgID={this.props.orgID} orgName={orgInfo.org_name} />
            <div className="cur-view-content">
              <Content
                orgID={this.props.orgID}
                loading={this.state.loading}
                errorMsg={this.state.errorMsg}
                orgInfo={this.state.orgInfo}
                updateQuota={this.updateQuota}
                updateName={this.updateName}
                updateMaxUserNumber={this.updateMaxUserNumber}
                updateStoragePolicy={this.updateStoragePolicy}
                searchOrgAdmins={this.searchOrgAdmins}
                transferOwnership={this.transferOwnership}
              />
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

OrgInfo.propTypes = {
  orgID: PropTypes.string,
  orgInfo: PropTypes.object,
};

export default OrgInfo;
