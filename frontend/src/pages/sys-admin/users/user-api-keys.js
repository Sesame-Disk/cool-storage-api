import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import { Button, FormGroup, Input, Label, Modal, ModalBody, ModalFooter, ModalHeader } from 'reactstrap';
import moment from 'moment';
import copy from 'copy-to-clipboard';
import { normalizeEmailRouteParam } from '../../../utils/email-route';
import { gettext } from '../../../utils/constants';
import { Utils } from '../../../utils/utils';
import { seafileAPI } from '../../../utils/seafile-api';
import toaster from '../../../components/toast';
import Loading from '../../../components/loading';
import CommonOperationConfirmationDialog from '../../../components/dialog/common-operation-confirmation-dialog';
import MainPanelTopbar from '../main-panel-topbar';
import Nav from './user-nav';

moment.locale(window.app && window.app.config && window.app.config.lang ? window.app.config.lang : 'en');

class UserAPIKeys extends Component {
    constructor(props) {
        super(props);
        this.state = {
            loading: true,
            errorMsg: '',
            userInfo: {},
            apiKeys: [],
            isCreateDialogOpen: false,
            isRevealDialogOpen: false,
            isRevokeDialogOpen: false,
            keyToRevoke: null,
            createdToken: '',
            createForm: {
                label: '',
                scope: 'read',
                expiration: '90'
            },
            isSubmitting: false
        };
    }

    componentDidMount() {
        const email = normalizeEmailRouteParam(this.props.email);
        seafileAPI.sysAdminGetUser(email).then((res) => {
            const userInfo = res.data;
            if (!userInfo.is_platform_org) {
                this.setState({
                    loading: false,
                    userInfo,
                    apiKeys: []
                });
                return null;
            }
            return seafileAPI.sysAdminListUserAPIKeys(email).then((keysRes) => {
                this.setState({
                    loading: false,
                    userInfo,
                    apiKeys: keysRes.data
                });
            });
        }).catch((error) => {
            this.setState({
                loading: false,
                errorMsg: Utils.getErrorMsg(error, true)
            });
        });
    }

    toggleCreateDialog = () => {
        this.setState((prevState) => ({
            isCreateDialogOpen: !prevState.isCreateDialogOpen,
            isSubmitting: false,
            createForm: prevState.isCreateDialogOpen ? prevState.createForm : {
                label: '',
                scope: this.canGrantAdminScope() ? 'admin' : 'read',
                expiration: '90'
            }
        }));
    };

    toggleRevealDialog = () => {
        this.setState((prevState) => ({
            isRevealDialogOpen: !prevState.isRevealDialogOpen,
            createdToken: prevState.isRevealDialogOpen ? '' : prevState.createdToken
        }));
    };

    toggleRevokeDialog = (keyToRevoke = null) => {
        this.setState((prevState) => ({
            isRevokeDialogOpen: !prevState.isRevokeDialogOpen,
            keyToRevoke: prevState.isRevokeDialogOpen ? null : keyToRevoke
        }));
    };

    canGrantAdminScope = () => {
        const role = this.state.userInfo.role;
        return role === 'superadmin' || role === 'owner' || role === 'admin';
    };

    isPlatformUser = () => {
        return Boolean(this.state.userInfo.is_platform_org);
    };

    isTargetActive = () => {
        return this.state.userInfo.status === 'active';
    };

    updateCreateField = (field, value) => {
        this.setState((prevState) => ({
            createForm: Object.assign({}, prevState.createForm, { [field]: value })
        }));
    };

    copyCreatedToken = () => {
        copy(this.state.createdToken);
        toaster.success(gettext('API key is copied to the clipboard.'));
    };

    createAPIKey = () => {
        const email = normalizeEmailRouteParam(this.props.email);
        const { label, scope, expiration } = this.state.createForm;
        const expiresInDays = expiration === 'never' ? null : parseInt(expiration, 10);

        this.setState({ isSubmitting: true });
        seafileAPI.sysAdminCreateUserAPIKey(email, label.trim(), scope, expiresInDays).then((res) => {
            const createdKey = Object.assign({}, res.data, {
                last_used_at: null
            });
            this.setState((prevState) => ({
                apiKeys: [createdKey].concat(prevState.apiKeys),
                createdToken: res.data.key,
                isSubmitting: false,
                isCreateDialogOpen: false,
                isRevealDialogOpen: true,
                createForm: {
                    label: '',
                    scope: this.canGrantAdminScope() ? 'admin' : 'read',
                    expiration: '90'
                }
            }));
            toaster.success(gettext('API key created.'));
        }).catch((error) => {
            this.setState({ isSubmitting: false });
            toaster.danger(Utils.getErrorMsg(error));
        });
    };

    revokeAPIKey = () => {
        const email = normalizeEmailRouteParam(this.props.email);
        const keyToRevoke = this.state.keyToRevoke;
        if (!keyToRevoke) {
            return;
        }

        seafileAPI.sysAdminRevokeUserAPIKey(email, keyToRevoke.key_hash).then(() => {
            this.setState((prevState) => ({
                apiKeys: prevState.apiKeys.filter((item) => item.key_hash !== keyToRevoke.key_hash),
                keyToRevoke: null,
                isRevokeDialogOpen: false
            }));
            toaster.success(gettext('API key revoked.'));
        }).catch((error) => {
            toaster.danger(Utils.getErrorMsg(error));
        });
    };

    formatTimestamp = (value, emptyText = '--') => {
        if (!value) {
            return emptyText;
        }
        return moment(value).format('YYYY-MM-DD HH:mm:ss');
    };

    renderContent() {
        const { apiKeys, createForm, isCreateDialogOpen, isRevealDialogOpen, isRevokeDialogOpen, keyToRevoke, createdToken, isSubmitting } = this.state;
        const canGrantAdminScope = this.canGrantAdminScope();
        const isPlatformUser = this.isPlatformUser();
        const isTargetActive = this.isTargetActive();

        if (!isPlatformUser) {
            return <p className="text-muted">{gettext('API key admin management is available only for platform users.')}</p>;
        }

        return (
            <Fragment>
                <div className="d-flex justify-content-between align-items-start mb-4">
                    <div>
                        <h4 className="mb-1">{gettext('API Keys')}</h4>
                        <p className="text-muted mb-0">{gettext('Use this surface for internal service actors like Accounts. The target user should belong to the platform organization and have the role required by the requested scope.')}</p>
                    </div>
                    <Button className="btn btn-primary" onClick={this.toggleCreateDialog} disabled={!isTargetActive}>
                        {gettext('Create API Key')}
                    </Button>
                </div>

                {!isTargetActive &&
                    <div className="alert alert-warning">{gettext('This user is not active. Restore or reactivate the user before issuing new API keys.')}</div>
                }

                <div className="border rounded">
                    <table className="table table-hover mb-0">
                        <thead>
                            <tr>
                                <th>{gettext('Label')}</th>
                                <th>{gettext('Prefix')}</th>
                                <th>{gettext('Scope')}</th>
                                <th>{gettext('Created')}</th>
                                <th>{gettext('Last Used')}</th>
                                <th>{gettext('Expires')}</th>
                                <th className="text-right">{gettext('Operations')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {apiKeys.length === 0 &&
                                <tr>
                                    <td colSpan="7" className="text-center text-muted py-4">{gettext('No API keys')}</td>
                                </tr>
                            }
                            {apiKeys.map((item) => (
                                <tr key={item.key_hash}>
                                    <td>{item.label}</td>
                                    <td><code>{item.key_prefix}</code></td>
                                    <td>{item.scope}</td>
                                    <td title={this.formatTimestamp(item.created_at)}>{item.created_at ? moment(item.created_at).fromNow() : '--'}</td>
                                    <td title={this.formatTimestamp(item.last_used_at)}>{item.last_used_at ? moment(item.last_used_at).fromNow() : '--'}</td>
                                    <td>{this.formatTimestamp(item.expires_at)}</td>
                                    <td className="text-right">
                                        <Button color="outline-danger" size="sm" onClick={() => this.toggleRevokeDialog(item)}>{gettext('Revoke')}</Button>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>

                <Modal isOpen={isCreateDialogOpen} toggle={this.toggleCreateDialog} centered>
                    <ModalHeader toggle={this.toggleCreateDialog}>{gettext('Create API Key')}</ModalHeader>
                    <ModalBody>
                        <FormGroup>
                            <Label for="api-key-label">{gettext('Label')}</Label>
                            <Input id="api-key-label" value={createForm.label} maxLength="120" onChange={(event) => this.updateCreateField('label', event.target.value)} />
                        </FormGroup>
                        <FormGroup>
                            <Label for="api-key-scope">{gettext('Scope')}</Label>
                            <Input id="api-key-scope" type="select" value={createForm.scope} onChange={(event) => this.updateCreateField('scope', event.target.value)}>
                                <option value="read">read</option>
                                <option value="read-write">read-write</option>
                                <option value="admin" disabled={!canGrantAdminScope}>admin</option>
                            </Input>
                            {!canGrantAdminScope &&
                                <small className="form-text text-muted">{gettext('Admin scope requires an admin-capable target user.')}</small>
                            }
                        </FormGroup>
                        <FormGroup>
                            <Label for="api-key-expiration">{gettext('Expiration')}</Label>
                            <Input id="api-key-expiration" type="select" value={createForm.expiration} onChange={(event) => this.updateCreateField('expiration', event.target.value)}>
                                <option value="30">30 days</option>
                                <option value="90">90 days</option>
                                <option value="365">1 year</option>
                                <option value="never">{gettext('Never')}</option>
                            </Input>
                        </FormGroup>
                    </ModalBody>
                    <ModalFooter>
                        <Button color="secondary" onClick={this.toggleCreateDialog}>{gettext('Cancel')}</Button>
                        <Button color="primary" disabled={isSubmitting || !createForm.label.trim()} onClick={this.createAPIKey}>{gettext('Create')}</Button>
                    </ModalFooter>
                </Modal>

                <Modal isOpen={isRevealDialogOpen} toggle={this.toggleRevealDialog} centered>
                    <ModalHeader toggle={this.toggleRevealDialog}>{gettext('API Key Created')}</ModalHeader>
                    <ModalBody>
                        <p className="text-danger font-weight-bold">{gettext('Store this API key now. It will not be shown again.')}</p>
                        <Input type="textarea" readOnly={true} value={createdToken} rows="3" />
                    </ModalBody>
                    <ModalFooter>
                        <Button color="secondary" onClick={this.toggleRevealDialog}>{gettext('Close')}</Button>
                        <Button color="primary" onClick={this.copyCreatedToken}>{gettext('Copy')}</Button>
                    </ModalFooter>
                </Modal>

                {isRevokeDialogOpen && keyToRevoke &&
                    <CommonOperationConfirmationDialog
                        title={gettext('Revoke API Key')}
                        message={gettext('Are you sure you want to revoke the API key {placeholder}?').replace('{placeholder}', keyToRevoke.label || keyToRevoke.key_prefix)}
                        confirmBtnText={gettext('Revoke')}
                        executeOperation={this.revokeAPIKey}
                        toggleDialog={() => this.toggleRevokeDialog()}
                    />
                }
            </Fragment>
        );
    }

    render() {
        if (this.state.loading) {
            return <Loading />;
        }

        return (
            <Fragment>
                <MainPanelTopbar {...this.props} />
                <div className="main-panel-center flex-row">
                    <div className="cur-view-container">
                        <Nav currentItem="api-keys" email={this.props.email} userName={this.state.userInfo.name || this.state.userInfo.email || normalizeEmailRouteParam(this.props.email)} />
                        <div className="cur-view-content">
                            {this.state.errorMsg ? <p className="error text-center mt-4">{this.state.errorMsg}</p> : this.renderContent()}
                        </div>
                    </div>
                </div>
            </Fragment>
        );
    }
}

UserAPIKeys.propTypes = {
    email: PropTypes.string,
};

export default UserAPIKeys;