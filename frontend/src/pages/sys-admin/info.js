import React, { Component, Fragment } from 'react';
import { seafileAPI } from '../../utils/seafile-api';
import { gettext, isPro } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import Loading from '../../components/loading';
import MainPanelTopbar from './main-panel-topbar';

import '../../css/system-info.css';

class Info extends Component {

  constructor(props) {
    super(props);
    this.state = {
      loading: true,
      errorMsg: '',
      sysInfo: {}
    };
  }

  componentDidMount() {
    seafileAPI.sysAdminGetSysInfo().then((res) => {
      this.setState({
        loading: false,
        sysInfo: res.data
      });
    }).catch((error) => {
      this.setState({
        loading: false,
        errorMsg: Utils.getErrorMsg(error, true) // true: show login tip if 403
      });
    });
  }

  render() {
    let { org_count,
      repos_count, total_files_count, total_storage, total_devices_count,
      current_connected_devices_count, multi_tenancy_enabled,
      active_users_count, users_count, groups_count,
      traffic_month_total, traffic_month_upload, traffic_month_download,
      traffic_year_total, traffic_year_upload, traffic_year_download } = this.state.sysInfo;
    let { loading, errorMsg } = this.state;
    const formatTrafficBreakdown = (total, upload, download) => {
      return `${Utils.bytesToSize(total || 0)} / ${Utils.bytesToSize(upload || 0)} / ${Utils.bytesToSize(download || 0)}`;
    };
    const deviceInfo = total_devices_count == null || current_connected_devices_count == null
      ? gettext('Not available')
      : `${total_devices_count} / ${current_connected_devices_count}`;

    return (
      <Fragment>
        <MainPanelTopbar {...this.props} />
        <div className="main-panel-center flex-row">
          <div className="cur-view-container system-admin-info">
            <h2 className="heading">{gettext('Info')}</h2>
            <div className="content">
              {loading && <Loading />}
              {errorMsg && <p className="error text-center mt-4">{errorMsg}</p>}
              {(!loading && !errorMsg) &&
                <dl className="flex-1 m-0">
                  <dt className="info-item-heading">{gettext('Libraries')} / {gettext('Files')}</dt>
                  <dd className="info-item-content">{repos_count} / {total_files_count}</dd>

                  <dt className="info-item-heading">{gettext('Storage Used')}</dt>
                  <dd className="info-item-content">{Utils.bytesToSize(total_storage)}</dd>

                  <dt className="info-item-heading">{gettext('This Month Traffic')} ({gettext('Total')} / {gettext('Upload')} / {gettext('Download')})</dt>
                  <dd className="info-item-content">{formatTrafficBreakdown(traffic_month_total, traffic_month_upload, traffic_month_download)}</dd>

                  <dt className="info-item-heading">{gettext('This Year Traffic')} ({gettext('Total')} / {gettext('Upload')} / {gettext('Download')})</dt>
                  <dd className="info-item-content">{formatTrafficBreakdown(traffic_year_total, traffic_year_upload, traffic_year_download)}</dd>

                  <dt className="info-item-heading">{gettext('Total Devices')} / {gettext('Current Connected Devices')}</dt>
                  <dd className="info-item-content">{deviceInfo}</dd>

                  {isPro ?
                    <Fragment>
                      <dt className="info-item-heading">{gettext('Activated Users')} / {gettext('Total Users')}</dt>
                      <dd className="info-item-content">{active_users_count} / {users_count}</dd>
                    </Fragment> :
                    <Fragment>
                      <dt className="info-item-heading">{gettext('Activated Users')} / {gettext('Total Users')}</dt>
                      <dd className="info-item-content">{active_users_count} / {users_count}</dd>
                    </Fragment>
                  }

                  <dt className="info-item-heading">{gettext('Groups')}</dt>
                  <dd className="info-item-content">{groups_count}</dd>

                  {multi_tenancy_enabled &&
                    <Fragment>
                      <dt className="info-item-heading">{gettext('Organizations')}</dt>
                      <dd className="info-item-content">{org_count}</dd>
                    </Fragment>
                  }
                </dl>
              }
            </div>
          </div>
        </div>
      </Fragment>
    );
  }
}

export default Info;
