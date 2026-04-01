import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { seafileAPI } from '../../../utils/seafile-api';
import { Utils } from '../../../utils/utils';
import Loading from '../../../components/loading';
import MainPanelTopbar from '../main-panel-topbar';
import OrgNav from './org-nav';
import OrgLinksPanel from './org-links-panel';

class OrgLinks extends Component {

    constructor(props) {
        super(props);
        this.state = {
            loading: true,
            errorMsg: '',
            orgName: ''
        };
    }

    componentDidMount() {
        seafileAPI.sysAdminGetOrg(this.props.orgID).then((res) => {
            this.setState({
                loading: false,
                orgName: res.data.org_name || ''
            });
        }).catch((error) => {
            this.setState({
                loading: false,
                errorMsg: Utils.getErrorMsg(error, true)
            });
        });
    }

    render() {
        return (
            <Fragment>
                <MainPanelTopbar {...this.props} />
                <div className="main-panel-center flex-row">
                    <div className="cur-view-container">
                        <OrgNav
                            currentItem="links"
                            orgID={this.props.orgID}
                            orgName={this.state.orgName}
                        />
                        <div className="cur-view-content">
                            {this.state.loading ? <Loading /> : this.state.errorMsg ? (
                                <p className="error text-center">{this.state.errorMsg}</p>
                            ) : (
                                <OrgLinksPanel orgID={this.props.orgID} />
                            )}
                        </div>
                    </div>
                </div>
            </Fragment>
        );
    }
}

OrgLinks.propTypes = {
    orgID: PropTypes.string,
};

export default OrgLinks;