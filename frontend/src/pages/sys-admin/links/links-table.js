import React, { Component, Fragment } from 'react';
import PropTypes from 'prop-types';
import moment from 'moment';
import { gettext } from '../../../utils/constants';
import EmptyTip from '../../../components/empty-tip';
import Loading from '../../../components/loading';
import Paginator from '../../../components/paginator';
import ShareAdminLinkEnhanced from '../../../components/dialog/share-admin-link-enhanced';
import UserLink from '../user-link';

class LinksContent extends Component {

    getPreviousPage = () => {
        this.props.getByPage(this.props.currentPage - 1);
    };

    getNextPage = () => {
        this.props.getByPage(this.props.currentPage + 1);
    };

    renderSortHeader = (field, label) => {
        const { enableSort, sortBy, sortOrder, sortItems } = this.props;
        if (!enableSort) {
            return label;
        }

        const initialSortIcon = <span className="fas fa-sort"></span>;
        const sortIcon = <span className={`fas ${sortOrder === 'asc' ? 'fa-caret-up' : 'fa-caret-down'}`}></span>;
        return (
            <button
                type="button"
                className="d-inline-block table-sort-op btn btn-link p-0 border-0 align-baseline"
                onClick={() => sortItems(field)}
            >
                {label} {sortBy === field ? sortIcon : initialSortIcon}
            </button>
        );
    };

    render() {
        const {
            loading,
            errorMsg,
            items,
            currentPage,
            hasNextPage,
            perPage,
            resetPerPage,
            activeFilter,
            expiredFilter,
            setActiveFilter,
            setExpiredFilter,
            emptyTitle,
            onDelete,
            onToggleActive,
        } = this.props;

        if (loading) {
            return <Loading />;
        }

        if (errorMsg) {
            return <p className="error text-center">{errorMsg}</p>;
        }

        const emptyTip = (
            <EmptyTip>
                <h2>{emptyTitle}</h2>
            </EmptyTip>
        );

        const table = (
            <Fragment>
                <table className="table-hover">
                    <thead>
                        <tr>
                            <th width="14%">{gettext('Name')}</th>
                            <th width="14%">{gettext('Repo')}</th>
                            <th width="12%">{gettext('Token')}</th>
                            <th width="12%">{gettext('Owner')}</th>
                            <th width="8%">{gettext('Active')}</th>
                            <th width="8%">{gettext('Protected')}</th>
                            <th width="13%">{this.renderSortHeader('ctime', gettext('Created At'))}</th>
                            <th width="8%">{this.renderSortHeader('view_cnt', gettext('Visits'))}</th>
                            <th width="7%">{gettext('Expired')}</th>
                            <th width="5%">{/* Operations */}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {items.map((item, index) => (
                            <LinksRow
                                key={index}
                                item={item}
                                onDelete={onDelete}
                                onToggleActive={onToggleActive}
                            />
                        ))}
                    </tbody>
                </table>
                <Paginator
                    gotoPreviousPage={this.getPreviousPage}
                    gotoNextPage={this.getNextPage}
                    currentPage={currentPage}
                    hasNextPage={hasNextPage}
                    curPerPage={perPage}
                    resetPerPage={resetPerPage}
                />
            </Fragment>
        );

        return (
            <Fragment>
                <div className="d-flex align-items-center mb-2 flex-wrap">
                    <span className="mr-2">{gettext('Active')}</span>
                    {['all', 'active', 'inactive'].map(active => {
                        const labels = {
                            all: gettext('All'),
                            active: gettext('Active'),
                            inactive: gettext('Inactive')
                        };
                        const activeSelected = activeFilter === active;
                        return (
                            <button
                                key={`active-${active}`}
                                className={`btn btn-sm mr-2 mb-1 ${activeSelected ? 'btn-primary' : 'btn-outline-secondary'}`}
                                onClick={() => setActiveFilter(active)}
                            >
                                {labels[active]}
                            </button>
                        );
                    })}
                    <span className="mr-2 ml-2">{gettext('Expired')}</span>
                    {['all', 'expired', 'not_expired'].map(expired => {
                        const labels = {
                            all: gettext('All'),
                            expired: gettext('Expired'),
                            not_expired: gettext('Not Expired')
                        };
                        const expiredSelected = expiredFilter === expired;
                        return (
                            <button
                                key={`expired-${expired}`}
                                className={`btn btn-sm mr-2 mb-1 ${expiredSelected ? 'btn-primary' : 'btn-outline-secondary'}`}
                                onClick={() => setExpiredFilter(expired)}
                            >
                                {labels[expired]}
                            </button>
                        );
                    })}
                </div>
                {items.length ? table : emptyTip}
            </Fragment>
        );
    }
}

LinksContent.propTypes = {
    loading: PropTypes.bool.isRequired,
    errorMsg: PropTypes.string.isRequired,
    items: PropTypes.array.isRequired,
    currentPage: PropTypes.number,
    hasNextPage: PropTypes.bool,
    perPage: PropTypes.number,
    resetPerPage: PropTypes.func,
    getByPage: PropTypes.func.isRequired,
    activeFilter: PropTypes.string.isRequired,
    expiredFilter: PropTypes.string.isRequired,
    setActiveFilter: PropTypes.func.isRequired,
    setExpiredFilter: PropTypes.func.isRequired,
    emptyTitle: PropTypes.string.isRequired,
    onDelete: PropTypes.func.isRequired,
    onToggleActive: PropTypes.func.isRequired,
    enableSort: PropTypes.bool,
    sortBy: PropTypes.string,
    sortOrder: PropTypes.string,
    sortItems: PropTypes.func,
};

class LinksRow extends Component {
    constructor(props) {
        super(props);
        this.state = {
            isOpIconShown: false,
            isLinkDialogOpen: false,
        };
    }

    handleMouseOver = () => {
        this.setState({ isOpIconShown: true });
    };

    handleMouseOut = () => {
        this.setState({ isOpIconShown: false });
    };

    toggleActive = () => {
        const { item, onToggleActive } = this.props;
        onToggleActive(item.token, !(item.active === true));
    };

    deleteLink = () => {
        this.props.onDelete(this.props.item.token);
    };

    toggleLinkDialog = () => {
        this.setState({ isLinkDialogOpen: !this.state.isLinkDialogOpen });
    };

    render() {
        const { isOpIconShown, isLinkDialogOpen } = this.state;
        const { item } = this.props;
        const deleteIcon = `action-icon sf2-icon-delete ${isOpIconShown ? '' : 'invisible'}`;
        const toggleIcon = `action-icon ${item.active === false ? 'sf2-icon-reply' : 'sf2-icon-x3'} mr-2 ${isOpIconShown ? '' : 'invisible'}`;
        const viewIcon = `action-icon sf2-icon-link mr-2 ${isOpIconShown && item.link ? '' : 'invisible'}`;

        return (
            <Fragment>
                <tr onMouseOver={this.handleMouseOver} onMouseOut={this.handleMouseOut}>
                    <td>{item.obj_name || item.path || '--'}</td>
                    <td>{item.repo_name || '--'}</td>
                    <td>{item.token}</td>
                    <td><UserLink email={item.creator_email} name={item.creator_name} /></td>
                    <td>
                        <span className={item.active === false ? 'badge badge-warning' : 'badge badge-success'}>
                            {item.active === false ? gettext('Inactive') : gettext('Active')}
                        </span>
                    </td>
                    <td>
                        <span className={item.has_password ? 'badge badge-warning' : 'badge badge-secondary'}>
                            {item.has_password ? gettext('Yes') : gettext('No')}
                        </span>
                    </td>
                    <td>{moment(item.ctime).fromNow()}</td>
                    <td>{item.view_cnt}</td>
                    <td>
                        <span className={item.is_expired ? 'badge badge-danger' : 'badge badge-secondary'}>
                            {item.is_expired ? gettext('Expired') : gettext('No')}
                        </span>
                    </td>
                    <td>
                        <button
                            type="button"
                            className={`${viewIcon} border-0 bg-transparent p-0`}
                            title={gettext('View')}
                            aria-label={gettext('View')}
                            onClick={this.toggleLinkDialog}
                            disabled={!item.link}
                        >
                            <span className="sr-only">{gettext('View')}</span>
                        </button>
                        <button
                            type="button"
                            className={`${toggleIcon} border-0 bg-transparent p-0`}
                            title={item.active === false ? gettext('Activate') : gettext('Deactivate')}
                            aria-label={item.active === false ? gettext('Activate') : gettext('Deactivate')}
                            onClick={this.toggleActive}
                        >
                            <span className="sr-only">{item.active === false ? gettext('Activate') : gettext('Deactivate')}</span>
                        </button>
                        <button
                            type="button"
                            className={`${deleteIcon} border-0 bg-transparent p-0`}
                            title={gettext('Delete')}
                            aria-label={gettext('Delete')}
                            onClick={this.deleteLink}
                        >
                            <span className="sr-only">{gettext('Delete')}</span>
                        </button>
                    </td>
                </tr>
                {isLinkDialogOpen && item.link && (
                    <ShareAdminLinkEnhanced
                        link={item.link}
                        password={item.password || ''}
                        hasPassword={item.has_password === true}
                        viewCount={item.view_cnt}
                        isShareLink={item.link_type !== 'upload'}
                        toggleDialog={this.toggleLinkDialog}
                    />
                )}
            </Fragment>
        );
    }
}

LinksRow.propTypes = {
    item: PropTypes.object.isRequired,
    onDelete: PropTypes.func.isRequired,
    onToggleActive: PropTypes.func.isRequired,
};

export default LinksContent;
