import React, { Fragment } from 'react';
import { Button, FormGroup, Input, Label, Modal, ModalBody, ModalFooter, ModalHeader } from 'reactstrap';
import moment from 'moment';
import copy from 'copy-to-clipboard';
import { gettext } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import { seafileAPI } from '../../utils/seafile-api';
import toaster from '../toast';
import CommonOperationConfirmationDialog from '../dialog/common-operation-confirmation-dialog';
import { getSettingsPageOptions } from './page-options';

moment.locale(window.app && window.app.config && window.app.config.lang ? window.app.config.lang : 'en');

class APIKeys extends React.Component {
    constructor(props) {
        super(props);
        this.state = {
            loading: true,
            errorMsg: '',
            apiKeys: [],
            isCreateDialogOpen: false,
            isRevealDialogOpen: false,
            isRevokeDialogOpen: false,
            isSubmitting: false,
            keyToRevoke: null,
            createdToken: '',
            createForm: this.getDefaultCreateForm()
        };
    }

    componentDidMount() {
        this.loadAPIKeys();
    }

    getDefaultCreateForm = () => {
        return {
            label: '',
            scope: 'read-write',
            expiration: '90'
        };
    };

    loadAPIKeys = () => {
        this.setState({ loading: true, errorMsg: '' });
        seafileAPI.listAPIKeys().then((res) => {
            this.setState({
                loading: false,
                apiKeys: Array.isArray(res.data) ? res.data : [],
                errorMsg: ''
            });
        }).catch((error) => {
            this.setState({
                loading: false,
                errorMsg: Utils.getErrorMsg(error, true)
            });
        });
    };

    canGrantAdminScope = () => {
        const role = getSettingsPageOptions().userRole;
        return role === 'superadmin' || role === 'owner' || role === 'admin';
    };

    toggleCreateDialog = () => {
        this.setState((prevState) => ({
            isCreateDialogOpen: !prevState.isCreateDialogOpen,
            isSubmitting: false,
            createForm: prevState.isCreateDialogOpen ? prevState.createForm : this.getDefaultCreateForm()
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
        const { label, scope, expiration } = this.state.createForm;
        const expiresInDays = expiration === 'never' ? null : parseInt(expiration, 10);

        this.setState({ isSubmitting: true });
        seafileAPI.createAPIKey(label.trim(), scope, expiresInDays).then((res) => {
            const createdKey = Object.assign({}, res.data, { last_used_at: null });
            this.setState((prevState) => ({
                apiKeys: [createdKey].concat(prevState.apiKeys),
                createdToken: res.data.key,
                isSubmitting: false,
                isCreateDialogOpen: false,
                isRevealDialogOpen: true,
                createForm: this.getDefaultCreateForm()
            }));
            toaster.success(gettext('API key created.'));
        }).catch((error) => {
            this.setState({ isSubmitting: false });
            toaster.danger(Utils.getErrorMsg(error));
        });
    };

    revokeAPIKey = () => {
        const keyToRevoke = this.state.keyToRevoke;
        if (!keyToRevoke) {
            return;
        }

        seafileAPI.revokeAPIKey(keyToRevoke.key_hash).then(() => {
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

    renderBody() {
        const {
            apiKeys, createForm, isCreateDialogOpen, isRevealDialogOpen,
            isRevokeDialogOpen, keyToRevoke, createdToken, isSubmitting,
            loading, errorMsg
        } = this.state;
        const canGrantAdminScope = this.canGrantAdminScope();

        return (
            <Fragment>
                <div className="d-flex justify-content-between align-items-start mb-4">
                    <div>
                        <p className="text-muted mb-0">{gettext('Create scoped API keys for CLI, desktop, and service integrations. For Seafile-compatible clients, use your email as username and the API key as password.')}</p>
                    </div>
                    <Button className="btn btn-primary ml-3" onClick={this.toggleCreateDialog}>
                        {gettext('Create API Key')}
                    </Button>
                </div>

                {errorMsg &&
                    <div className="alert alert-danger d-flex justify-content-between align-items-center" role="alert">
                        <span>{errorMsg}</span>
                        <Button color="secondary" size="sm" onClick={this.loadAPIKeys}>{gettext('Retry')}</Button>
                    </div>
                }

                <div className="table-responsive border rounded api-keys-table-wrap">
                    <table className="table table-hover mb-0 api-keys-table">
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
                            {loading &&
                                <tr>
                                    <td colSpan="7" className="text-center text-muted py-4">{gettext('Loading...')}</td>
                                </tr>
                            }
                            {!loading && apiKeys.length === 0 &&
                                <tr>
                                    <td colSpan="7" className="text-center text-muted py-4">{gettext('No API keys')}</td>
                                </tr>
                            }
                            {!loading && apiKeys.map((item) => (
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
                            <Label for="self-api-key-label">{gettext('Label')}</Label>
                            <Input id="self-api-key-label" value={createForm.label} maxLength="120" onChange={(event) => this.updateCreateField('label', event.target.value)} />
                        </FormGroup>
                        <FormGroup>
                            <Label for="self-api-key-scope">{gettext('Scope')}</Label>
                            <Input id="self-api-key-scope" type="select" value={createForm.scope} onChange={(event) => this.updateCreateField('scope', event.target.value)}>
                                <option value="read">read</option>
                                <option value="read-write">read-write</option>
                                <option value="admin" disabled={!canGrantAdminScope}>admin</option>
                            </Input>
                            {!canGrantAdminScope &&
                                <small className="form-text text-muted">{gettext('Admin scope requires an admin-capable user.')}</small>
                            }
                        </FormGroup>
                        <FormGroup>
                            <Label for="self-api-key-expiration">{gettext('Expiration')}</Label>
                            <Input id="self-api-key-expiration" type="select" value={createForm.expiration} onChange={(event) => this.updateCreateField('expiration', event.target.value)}>
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
        return (
            <div id="api-keys" className="setting-item">
                <h3 className="setting-item-heading">{gettext('API Keys')}</h3>
                {this.renderBody()}
            </div>
        );
    }
}

export default APIKeys;
