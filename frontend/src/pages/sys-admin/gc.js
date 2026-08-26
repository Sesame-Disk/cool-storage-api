import React, { Component, Fragment } from 'react';
import moment from 'moment';
import { gettext } from '../../utils/constants';
import { formatRetentionDaysValue, getDeletedLibrariesRetentionMessage, getDeletedOrganizationsRetentionMessage, getDeletedUsersRetentionMessage, getOrgGraceDays, getTrashReposExpireDays, getUserGraceDays } from '../../utils/trash-retention';
import { seafileAPI } from '../../utils/seafile-api';
import { Utils } from '../../utils/utils';
import toaster from '../../components/toast';
import Loading from '../../components/loading';
import MainPanelTopbar from './main-panel-topbar';

import '../../css/sysadmin-gc.css';

class GC extends Component {

    failedItemsSectionRef = React.createRef();

    constructor(props) {
        super(props);
        this.state = {
            loadingStatus: true,
            statusError: '',
            status: null,
            runningType: '',
            failedItemOrgList: [],
            failedItemOrgListLoading: false,
            failedItemOrgListError: '',
            orgSearchQuery: '',
            orgSearchResults: [],
            orgSearchLoading: false,
            orgSearchError: '',
            hasSearchedOrgs: false,
            selectedOrg: null,
            failedItemsOrgID: '',
            failedItemsLimit: '100',
            failedItems: [],
            hasLoadedFailedItems: false,
            failedItemsError: '',
            failedItemsLoading: false,
            pendingActionKey: '',
        };
    }

    componentDidMount() {
        this.refreshPage();
    }

    refreshPage = () => {
        this.loadStatus();
        this.loadFailedItemOrganizations();
        if (this.state.failedItemsOrgID.trim()) {
            this.loadFailedItems();
        }
    }

    loadFailedItemOrganizations = () => {
        this.setState({ failedItemOrgListLoading: true, failedItemOrgListError: '' });
        seafileAPI.sysAdminListGCFailedItemOrgs(20).then((res) => {
            this.setState({
                failedItemOrgListLoading: false,
                failedItemOrgList: res.data.organizations || [],
            });
        }).catch((error) => {
            this.setState({
                failedItemOrgListLoading: false,
                failedItemOrgListError: Utils.getErrorMsg(error, true),
            });
        });
    }

    loadStatus = () => {
        this.setState({ loadingStatus: true, statusError: '' });
        seafileAPI.sysAdminGetGCStatus().then((res) => {
            this.setState({
                loadingStatus: false,
                status: res.data,
            });
        }).catch((error) => {
            this.setState({
                loadingStatus: false,
                statusError: Utils.getErrorMsg(error, true),
            });
        });
    };

    loadFailedItems = () => {
        const orgID = this.state.failedItemsOrgID.trim();
        if (!orgID) {
            this.setState({ failedItemsError: gettext('Org ID is required.') });
            return;
        }

        this.setState({ failedItemsLoading: true, failedItemsError: '' });
        seafileAPI.sysAdminListGCFailedItems(orgID, this.state.failedItemsLimit).then((res) => {
            this.setState({
                failedItemsLoading: false,
                failedItems: res.data.items || [],
                hasLoadedFailedItems: true,
            });
            this.loadSelectedOrg(orgID);
        }).catch((error) => {
            this.setState({
                failedItemsLoading: false,
                failedItemsError: Utils.getErrorMsg(error, true),
            });
        });
    };

    loadSelectedOrg = (orgID) => {
        if (!orgID) {
            return;
        }

        if (this.state.selectedOrg && this.state.selectedOrg.org_id === orgID) {
            return;
        }

        seafileAPI.sysAdminGetOrg(orgID).then((res) => {
            this.setState({
                selectedOrg: {
                    org_id: res.data.org_id || orgID,
                    org_name: res.data.org_name || orgID,
                    owner_email: res.data.owner_email || '',
                    status: res.data.status || '',
                }
            });
        }).catch(() => { });
    };

    searchOrganizations = (event) => {
        if (event) {
            event.preventDefault();
        }

        const query = this.state.orgSearchQuery.trim();
        if (!query) {
            this.setState({
                hasSearchedOrgs: false,
                orgSearchResults: [],
                orgSearchError: '',
            });
            return;
        }

        this.setState({
            orgSearchLoading: true,
            orgSearchError: '',
            hasSearchedOrgs: true,
        });
        seafileAPI.sysAdminSearchOrgs(query).then((res) => {
            this.setState({
                orgSearchLoading: false,
                orgSearchResults: res.data.organizations || res.data.organization_list || [],
            });
        }).catch((error) => {
            this.setState({
                orgSearchLoading: false,
                orgSearchError: Utils.getErrorMsg(error, true),
                orgSearchResults: [],
            });
        });
    };

    selectOrganization = (org) => {
        this.setState({
            selectedOrg: org,
            failedItemsOrgID: org.org_id,
            failedItemsError: '',
        }, () => {
            this.loadFailedItems();
            window.requestAnimationFrame(this.scrollToFailedItemsSection);
        });
    };

    scrollToFailedItemsSection = () => {
        const node = this.failedItemsSectionRef.current;
        if (!node) return;
        const scroller = node.closest('.cur-view-content');
        if (!scroller) {
            node.scrollIntoView({ block: 'nearest' });
            return;
        }
        const nodeTop = node.offsetTop - scroller.offsetTop;
        const visibleTop = scroller.scrollTop;
        const visibleBottom = visibleTop + scroller.clientHeight;
        if (nodeTop < visibleTop || nodeTop > visibleBottom - 80) {
            scroller.scrollTo({ top: Math.max(0, nodeTop - 8) });
        }
    };

    runGC = (type) => {
        this.setState({ runningType: type });
        seafileAPI.sysAdminRunGC(type).then((res) => {
            this.setState({ runningType: '' });
            toaster.success(res.data.message || gettext('GC run started.'), { duration: 2 });
            this.loadStatus();
            this.loadFailedItemOrganizations();
        }).catch((error) => {
            this.setState({ runningType: '' });
            toaster.danger(Utils.getErrorMsg(error, true), { duration: 3 });
        });
    };

    updateFailedItemsField = (event) => {
        const { name, value } = event.target;
        this.setState((prevState) => {
            const nextState = { [name]: value };
            if (name === 'failedItemsOrgID' && prevState.selectedOrg && prevState.selectedOrg.org_id !== value.trim()) {
                nextState.selectedOrg = null;
            }
            return nextState;
        });
    };

    handleFailedItemsSubmit = (event) => {
        event.preventDefault();
        this.loadFailedItems();
    };

    getFailedItemActionKey = (action, item) => {
        return `${action}:${item.org_id || item.orgID}:${item.failed_at || item.failedAt}:${item.item_type || item.itemType}:${item.item_id || item.itemID}`;
    };

    getFailedItemPayload = (item) => {
        const payload = {
            org_id: item.org_id || item.orgID,
            failed_at: item.failed_at || item.failedAt,
            item_type: item.item_type || item.itemType,
            item_id: item.item_id || item.itemID,
            identity_at: item.identity_at || item.identityAt,
        };
        const candidate = item.block_gc_candidate_identity || item.blockGCCandidateIdentity;
        if (candidate) {
            const target = candidate.target || candidate.Target || {};
            payload.candidate_storage_class = target.storage_class || target.StorageClass;
            payload.candidate_storage_key = target.storage_key || target.StorageKey;
            payload.candidate_at = candidate.candidate_at || candidate.CandidateAt;
        }
        return payload;
    };

    operateFailedItem = (action, item) => {
        const actionLabel = action === 'delete' ? gettext('delete') : gettext('requeue');
        if (!window.confirm(gettext('Are you sure you want to {actionLabel} this failed item?').replace('{actionLabel}', actionLabel))) {
            return;
        }

        const actionKey = this.getFailedItemActionKey(action, item);
        const payload = this.getFailedItemPayload(item);
        const request = action === 'delete'
            ? seafileAPI.sysAdminDeleteGCFailedItem(payload)
            : seafileAPI.sysAdminRequeueGCFailedItem(payload);

        this.setState({ pendingActionKey: actionKey });
        request.then(() => {
            this.setState({ pendingActionKey: '' });
            toaster.success(action === 'delete' ? gettext('Failed item deleted.') : gettext('Failed item requeued.'), { duration: 2 });
            this.loadFailedItems();
            this.loadStatus();
            this.loadFailedItemOrganizations();
        }).catch((error) => {
            this.setState({ pendingActionKey: '' });
            toaster.danger(Utils.getErrorMsg(error, true), { duration: 3 });
        });
    };

    formatTimestamp = (value) => {
        if (value == null) {
            return gettext('Never');
        }

        if (typeof value === 'string') {
            const trimmedValue = value.trim();
            if (!trimmedValue || trimmedValue.toLowerCase() === 'never') {
                return gettext('Never');
            }
            const timestamp = moment.parseZone(trimmedValue, moment.ISO_8601, true);
            if (!timestamp.isValid()) {
                return trimmedValue;
            }
            return timestamp.local().format('YYYY-MM-DD HH:mm:ss');
        }

        const timestamp = moment(value);
        if (!timestamp.isValid()) {
            return String(value);
        }
        return timestamp.format('YYYY-MM-DD HH:mm:ss');
    };

    formatSnapshotAge = (value) => {
        if (value === -1) {
            return gettext('Never reconciled');
        }
        if (value == null) {
            return '--';
        }
        return `${value}s`;
    };

    getOperationalHints = (status) => {
        if (!status) {
            return [];
        }

        const hints = [];

        if (status.last_scan_error) {
            hints.push({
                tone: 'danger',
                title: gettext('Scanner needs attention'),
                description: gettext('The last scanner run ended with an aggregated error. Review the message below before forcing more manual scans.'),
            });
        }

        if (status.snapshot_age_seconds === -1) {
            hints.push({
                tone: 'warning',
                title: gettext('No reconciled snapshot yet'),
                description: gettext('Queue and failed-item totals are still waiting for the first reconciliation pass on this leader.'),
            });
        } else if (status.snapshot_age_seconds > 600) {
            hints.push({
                tone: 'warning',
                title: gettext('Admin totals are stale'),
                description: gettext('The reconciled snapshot is older than 10 minutes. If this persists, check reconcile activity and leadership health.'),
            });
        }

        if ((status.failed_items_total || 0) > 0) {
            hints.push({
                tone: 'info',
                title: gettext('Failed items pending review'),
                description: gettext('Use the DLQ table below to inspect the last error, requeue, or delete stuck items explicitly.'),
            });
        }

        if ((status.dirty_orgs_total || 0) > 0 && (status.queue_size || 0) === 0) {
            hints.push({
                tone: 'info',
                title: gettext('Reconciliation backlog detected'),
                description: gettext('Dirty organizations remain even though the live queue is empty. A reconcile run should refresh the admin snapshot.'),
            });
        }

        return hints;
    };

    renderStatusCard = (label, value, description, extraClassName) => {
        return (
            <div className={`gc-admin-stat-card ${extraClassName || ''}`}>
                <div className="gc-admin-stat-label">{label}</div>
                <div className="gc-admin-stat-value">{value}</div>
                {description && <div className="gc-admin-stat-description">{description}</div>}
            </div>
        );
    };

    renderOperationalHints = (status) => {
        const hints = this.getOperationalHints(status);
        if (hints.length === 0) {
            return null;
        }

        return (
            <div className="gc-admin-hints-list mt-3">
                {hints.map((hint, index) => (
                    <div key={`${hint.tone}-${index}`} className={`gc-admin-hint gc-admin-hint-${hint.tone}`}>
                        <div className="gc-admin-hint-title">{hint.title}</div>
                        <div className="gc-admin-hint-description">{hint.description}</div>
                    </div>
                ))}
            </div>
        );
    };

    renderFailedItemsTable = () => {
        const { failedItems, hasLoadedFailedItems, pendingActionKey } = this.state;

        if (!hasLoadedFailedItems) {
            return null;
        }

        if (failedItems.length === 0) {
            return <p className="gc-admin-empty">{gettext('No failed items found for this organization.')}</p>;
        }

        return (
            <div className="gc-admin-table-wrap">
                <table className="table table-hover gc-admin-table">
                    <thead>
                        <tr>
                            <th>{gettext('Failed')}</th>
                            <th>{gettext('Queued')}</th>
                            <th>{gettext('Type')}</th>
                            <th>{gettext('Item')}</th>
                            <th>{gettext('Retry')}</th>
                            <th>{gettext('Library')}</th>
                            <th>{gettext('Storage')}</th>
                            <th>{gettext('Last error')}</th>
                            <th>{gettext('Operations')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {failedItems.map((item, index) => {
                            const requeueKey = this.getFailedItemActionKey('requeue', item);
                            const deleteKey = this.getFailedItemActionKey('delete', item);
                            const isBusy = pendingActionKey === requeueKey || pendingActionKey === deleteKey;

                            return (
                                <tr key={`${item.failed_at || item.failedAt}-${item.item_id || item.itemID}-${index}`}>
                                    <td>{this.formatTimestamp(item.failed_at || item.failedAt)}</td>
                                    <td>{this.formatTimestamp(item.queued_at || item.queuedAt)}</td>
                                    <td>{item.item_type || item.itemType}</td>
                                    <td className="gc-admin-break-word">{item.item_id || item.itemID}</td>
                                    <td>{item.retry_count || item.retryCount || 0}</td>
                                    <td className="gc-admin-break-word">{item.library_id || item.libraryID || '--'}</td>
                                    <td>{item.storage_class || item.storageClass || '--'}</td>
                                    <td className="gc-admin-break-word">{item.last_error || item.lastError || '--'}</td>
                                    <td>
                                        <div className="gc-admin-inline-actions">
                                            <button
                                                type="button"
                                                className="btn btn-outline-primary btn-sm"
                                                disabled={isBusy}
                                                onClick={() => this.operateFailedItem('requeue', item)}
                                            >
                                                {pendingActionKey === requeueKey ? gettext('Working...') : gettext('Requeue')}
                                            </button>
                                            <button
                                                type="button"
                                                className="btn btn-outline-danger btn-sm"
                                                disabled={isBusy}
                                                onClick={() => this.operateFailedItem('delete', item)}
                                            >
                                                {pendingActionKey === deleteKey ? gettext('Working...') : gettext('Delete')}
                                            </button>
                                        </div>
                                    </td>
                                </tr>
                            );
                        })}
                    </tbody>
                </table>
            </div>
        );
    };

    renderOrgSearchResults = () => {
        const { hasSearchedOrgs, orgSearchLoading, orgSearchResults } = this.state;

        if (orgSearchLoading) {
            return <Loading />;
        }

        if (!hasSearchedOrgs) {
            return null;
        }

        if (orgSearchResults.length === 0) {
            return <p className="gc-admin-empty mb-0">{gettext('No organizations matched this search.')}</p>;
        }

        return (
            <div className="gc-admin-table-wrap mt-3">
                <table className="table table-hover gc-admin-table">
                    <thead>
                        <tr>
                            <th>{gettext('Organization')}</th>
                            <th>{gettext('Owner')}</th>
                            <th>{gettext('Status')}</th>
                            <th>{gettext('ID')}</th>
                            <th>{gettext('Operations')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {orgSearchResults.map((org) => {
                            return (
                                <tr key={org.org_id}>
                                    <td>{org.org_name || '--'}</td>
                                    <td>{org.owner_email || '--'}</td>
                                    <td>{org.status || '--'}</td>
                                    <td className="gc-admin-break-word">{org.org_id}</td>
                                    <td>
                                        <button
                                            type="button"
                                            className="btn btn-outline-primary btn-sm"
                                            onClick={() => this.selectOrganization(org)}
                                        >
                                            {gettext('Load failed items')}
                                        </button>
                                    </td>
                                </tr>
                            );
                        })}
                    </tbody>
                </table>
            </div>
        );
    };

    renderFailedItemOrgList = () => {
        const { failedItemOrgList, failedItemOrgListLoading } = this.state;

        if (failedItemOrgListLoading) {
            return <Loading />;
        }

        if (failedItemOrgList.length === 0) {
            return <p className="gc-admin-empty mb-0">{gettext('No organizations currently have failed GC items.')}</p>;
        }

        return (
            <div className="gc-admin-table-wrap mt-3">
                <table className="table table-hover gc-admin-table">
                    <thead>
                        <tr>
                            <th>{gettext('Organization')}</th>
                            <th>{gettext('Failed items')}</th>
                            <th>{gettext('Updated')}</th>
                            <th>{gettext('ID')}</th>
                            <th>{gettext('Operations')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {failedItemOrgList.map((org) => (
                            <tr key={org.org_id}>
                                <td>{org.org_name || org.org_id}</td>
                                <td>{org.failed_items_total || 0}</td>
                                <td>{this.formatTimestamp(org.updated_at)}</td>
                                <td className="gc-admin-break-word">{org.org_id}</td>
                                <td>
                                    <button
                                        type="button"
                                        className="btn btn-outline-primary btn-sm"
                                        onClick={() => this.selectOrganization(org)}
                                    >
                                        {gettext('Open DLQ')}
                                    </button>
                                </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>
        );
    };

    render() {
        const {
            failedItemsError,
            failedItemOrgList,
            failedItemOrgListError,
            failedItemOrgListLoading,
            failedItemsLimit,
            failedItemsLoading,
            failedItemsOrgID,
            hasSearchedOrgs,
            loadingStatus,
            orgSearchError,
            orgSearchLoading,
            orgSearchQuery,
            runningType,
            selectedOrg,
            status,
            statusError,
        } = this.state;

        const runButtonsDisabled = runningType !== '' || !status || !status.enabled;

        return (
            <Fragment>
                <MainPanelTopbar {...this.props}>
                    <div className="gc-admin-action-buttons">
                        <button
                            type="button"
                            className="btn btn-secondary"
                            disabled={loadingStatus}
                            onClick={this.refreshPage}
                        >
                            {gettext('Refresh')}
                        </button>
                        <button
                            type="button"
                            className="btn btn-primary"
                            disabled={runButtonsDisabled}
                            onClick={() => this.runGC('worker')}
                        >
                            {runningType === 'worker' ? gettext('Starting...') : gettext('Run worker')}
                        </button>
                        <button
                            type="button"
                            className="btn btn-outline-primary"
                            disabled={runButtonsDisabled}
                            onClick={() => this.runGC('scanner')}
                        >
                            {runningType === 'scanner' ? gettext('Starting...') : gettext('Run scanner')}
                        </button>
                    </div>
                </MainPanelTopbar>
                <div className="main-panel-center flex-row">
                    <div className="cur-view-container gc-admin-page">
                        <div className="cur-view-path">
                            <h3 className="sf-heading">{gettext('Garbage Collection')}</h3>
                        </div>
                        <div className="cur-view-content">
                            <div className="gc-admin-section">
                                <div className="gc-admin-section-header">
                                    <div>
                                        <h4 className="gc-admin-section-title">{gettext('Runtime status')}</h4>
                                        <p className="gc-admin-helper mb-0">{gettext('Manual runs are only available when the GC service is enabled on the current leader.')}</p>
                                    </div>
                                </div>
                                {loadingStatus && <Loading />}
                                {statusError && <p className="error text-center mt-4">{statusError}</p>}
                                {(!loadingStatus && !statusError && status) &&
                                    <Fragment>
                                        {(() => {
                                            const userGraceDays = getUserGraceDays('sys');
                                            const orgGraceDays = getOrgGraceDays('sys');
                                            const trashReposExpireDays = getTrashReposExpireDays('sys');

                                            return (
                                                <div className="gc-admin-alert gc-admin-alert-info mb-3">
                                                    <div className="gc-admin-alert-title">{gettext('Retention policy')}</div>
                                                    <div className="gc-admin-alert-body">
                                                        <div>{gettext('Deleted users')}: {formatRetentionDaysValue(userGraceDays)}. {getDeletedUsersRetentionMessage('sys')}</div>
                                                        <div>{gettext('Deleted organizations')}: {formatRetentionDaysValue(orgGraceDays)}. {getDeletedOrganizationsRetentionMessage('sys')}</div>
                                                        <div>{gettext('Deleted libraries')}: {formatRetentionDaysValue(trashReposExpireDays)}. {getDeletedLibrariesRetentionMessage('sys')}</div>
                                                    </div>
                                                </div>
                                            );
                                        })()}
                                        {status.last_scan_error &&
                                            <div className="gc-admin-alert gc-admin-alert-danger mb-3">
                                                <div className="gc-admin-alert-title">{gettext('Last scanner error')}</div>
                                                <div className="gc-admin-alert-meta">{gettext('Last scan attempt')}: {this.formatTimestamp(status.last_scan_attempt || status.last_scan_run)}</div>
                                                <div className="gc-admin-alert-body gc-admin-break-word">{status.last_scan_error}</div>
                                            </div>
                                        }
                                        <div className="gc-admin-summary-grid">
                                            {this.renderStatusCard(gettext('Enabled'), status.enabled ? gettext('Yes') : gettext('No'), gettext('Service availability on this node'), status.enabled ? 'is-positive' : 'is-negative')}
                                            {this.renderStatusCard(gettext('Dry run'), status.dry_run ? gettext('Yes') : gettext('No'), gettext('Deletes are skipped when dry run is enabled'))}
                                            {this.renderStatusCard(gettext('Queue size'), status.queue_size || 0, gettext('Current queue depth snapshot'))}
                                            {this.renderStatusCard(gettext('Failed items'), status.failed_items_total || 0, gettext('Dead-letter queue snapshot'))}
                                            {this.renderStatusCard(gettext('Dirty orgs'), status.dirty_orgs_total || 0, gettext('Pending reconciliation backlog'))}
                                            {this.renderStatusCard(gettext('Blocks deleted'), status.blocks_deleted_total || 0, gettext('Worker runtime counter'))}
                                            {this.renderStatusCard(gettext('Snapshot age'), this.formatSnapshotAge(status.snapshot_age_seconds), gettext('Age of the last reconciliation snapshot'))}
                                            {this.renderStatusCard(gettext('Grace period'), `${status.grace_period_seconds || 0}s`, gettext('Configured retention delay before delete'))}
                                            {this.renderStatusCard(gettext('User grace window'), formatRetentionDaysValue(getUserGraceDays('sys')), getDeletedUsersRetentionMessage('sys'))}
                                            {this.renderStatusCard(gettext('Org grace window'), formatRetentionDaysValue(getOrgGraceDays('sys')), getDeletedOrganizationsRetentionMessage('sys'))}
                                            {this.renderStatusCard(gettext('Library trash retention'), formatRetentionDaysValue(getTrashReposExpireDays('sys')), getDeletedLibrariesRetentionMessage('sys'))}
                                        </div>
                                        <div className="gc-admin-status-grid mt-3">
                                            {this.renderStatusCard(gettext('Last worker run'), this.formatTimestamp(status.last_worker_run), null)}
                                            {this.renderStatusCard(gettext('Last scan attempt'), this.formatTimestamp(status.last_scan_attempt || status.last_scan_run), gettext('Updated after every scanner attempt, even when the scan reports an error.'))}
                                            {this.renderStatusCard(gettext('Last successful scan'), this.formatTimestamp(status.last_scan_success), gettext('Most recent scanner attempt that completed without an aggregated error.'))}
                                            {this.renderStatusCard(gettext('Scanner error state'), status.last_scan_error ? gettext('Error recorded') : gettext('Clear'), status.last_scan_error || gettext('No aggregated scanner error is currently recorded.'), status.last_scan_error ? 'is-negative' : 'is-positive')}
                                            {this.renderStatusCard(gettext('Last reconcile run'), this.formatTimestamp(status.last_reconcile_run), null)}
                                        </div>
                                        {this.renderOperationalHints(status)}
                                    </Fragment>
                                }
                            </div>

                            <div className="gc-admin-section">
                                <div className="gc-admin-section-header">
                                    <div>
                                        <h4 className="gc-admin-section-title">{gettext('Organizations with failed items')}</h4>
                                        <p className="gc-admin-helper mb-0">{gettext('This list is built from reconciled GC org stats and gives you a direct entry point into each DLQ.')}</p>
                                    </div>
                                </div>
                                {failedItemOrgListError && <p className="error mt-3 mb-0">{failedItemOrgListError}</p>}
                                {(!failedItemOrgListLoading || failedItemOrgList.length > 0) && this.renderFailedItemOrgList()}
                                {failedItemOrgListLoading && failedItemOrgList.length === 0 && <div className="mt-3"><Loading /></div>}
                            </div>

                            <div className="gc-admin-section" ref={this.failedItemsSectionRef}>
                                <div className="gc-admin-section-header">
                                    <div>
                                        <h4 className="gc-admin-section-title">{gettext('Failed items')}</h4>
                                        <p className="gc-admin-helper mb-0">{gettext('Search an organization by name, or pick one from the failed-items list, then load and operate on its GC dead-letter queue.')}</p>
                                    </div>
                                </div>
                                <form className="gc-admin-org-search-row" onSubmit={this.searchOrganizations}>
                                    <div>
                                        <label className="gc-admin-field-label" htmlFor="gc-org-search-query">{gettext('Organization name')}</label>
                                        <input
                                            id="gc-org-search-query"
                                            className="form-control"
                                            name="orgSearchQuery"
                                            value={orgSearchQuery}
                                            onChange={this.updateFailedItemsField}
                                            placeholder={gettext('Search organizations by name')}
                                        />
                                    </div>
                                    <div className="gc-admin-filter-submit">
                                        <button type="submit" className="btn btn-secondary" disabled={orgSearchLoading}>
                                            {orgSearchLoading ? gettext('Searching...') : gettext('Search organizations')}
                                        </button>
                                    </div>
                                </form>
                                {orgSearchError && <p className="error mt-3 mb-0">{orgSearchError}</p>}
                                {(hasSearchedOrgs || orgSearchLoading) && this.renderOrgSearchResults()}

                                {selectedOrg &&
                                    <div className="gc-admin-selected-org mt-3">
                                        <div className="gc-admin-selected-org-title">{gettext('Selected organization')}</div>
                                        <div className="gc-admin-selected-org-body">
                                            <span className="gc-admin-selected-org-name">{selectedOrg.org_name || selectedOrg.org_id}</span>
                                            <span>{selectedOrg.owner_email || '--'}</span>
                                            <span>{selectedOrg.status || '--'}</span>
                                            <span className="gc-admin-break-word">{selectedOrg.org_id}</span>
                                        </div>
                                    </div>
                                }

                                <form className="gc-admin-filter-row" onSubmit={this.handleFailedItemsSubmit}>
                                    <div>
                                        <label className="gc-admin-field-label" htmlFor="gc-failed-items-org-id">{gettext('Org ID')}</label>
                                        <input
                                            id="gc-failed-items-org-id"
                                            className="form-control"
                                            name="failedItemsOrgID"
                                            value={failedItemsOrgID}
                                            onChange={this.updateFailedItemsField}
                                            placeholder={gettext('00000000-0000-0000-0000-000000000000')}
                                        />
                                    </div>
                                    <div>
                                        <label className="gc-admin-field-label" htmlFor="gc-failed-items-limit">{gettext('Limit')}</label>
                                        <input
                                            id="gc-failed-items-limit"
                                            className="form-control"
                                            name="failedItemsLimit"
                                            value={failedItemsLimit}
                                            onChange={this.updateFailedItemsField}
                                            inputMode="numeric"
                                        />
                                    </div>
                                    <div className="gc-admin-filter-submit">
                                        <button type="submit" className="btn btn-primary" disabled={failedItemsLoading}>
                                            {failedItemsLoading ? gettext('Loading...') : gettext('Load failed items')}
                                        </button>
                                    </div>
                                </form>
                                {failedItemsError && <p className="error mt-3 mb-0">{failedItemsError}</p>}
                                {!failedItemsLoading && this.renderFailedItemsTable()}
                                {failedItemsLoading && <div className="mt-3"><Loading /></div>}
                            </div>
                        </div>
                    </div>
                </div>
            </Fragment>
        );
    }
}

export default GC;
