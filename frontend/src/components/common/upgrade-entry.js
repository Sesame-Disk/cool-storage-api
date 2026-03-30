/* eslint-disable */
import React from 'react';
import { billingUrl, gettext, siteRoot } from '../../utils/constants';
import { getUpgradeState } from '../../utils/upgrade-state';
import ChatLauncher from '../../services/chat';

export default function UpgradeEntry({ inSidebar, isOrgStaff }) {
    const {
        canUpgrade,
        isOrgOwner,
        isFeatureLockedOwner,
        isPaidOwnerUpgradeCandidate,
        overQuota,
    } = getUpgradeState();

    const orgInfoUrl = `${siteRoot}org/info/`;
    const highlightClassName = `item ${inSidebar ? 'highlighted-link highlighted-link-p-x' : 'highlight-item'}`;

    if (isFeatureLockedOwner) {
        return <>
            <a
                href={billingUrl}
                title={gettext('Upgrade your plan to unlock more collaboration features.')}
                className={highlightClassName}
                target="_blank"
                rel="noopener noreferrer"
            >
                <span className="sf2-icon-star" style={{ verticalAlign: 'middle' }} /> {gettext('Upgrade')}
            </a>
            {!inSidebar && !isOrgStaff && (
                <a
                    href={orgInfoUrl}
                    title={gettext('Review the collaboration and member-management features available to your organization.')}
                    className="item highlight-item"
                >
                    <span className="sf2-icon-star" style={{ verticalAlign: 'middle' }} /> {gettext('Organization Admin')}
                </a>
            )}
            <ChatLauncher linkClassName="item" showNewBadge={true} inSidebar={inSidebar} />
        </>;
    }

    if (isPaidOwnerUpgradeCandidate && overQuota) {
        return <>
            <a
                href={billingUrl}
                title={gettext('You have reached a quota limit. Open Billing to increase or adjust your plan.')}
                className={`item highlighted-link${inSidebar ? ' highlighted-link-p-x' : ''}`}
                style={{ color: '#b42318' }}
                target="_blank"
                rel="noopener noreferrer"
            >
                {gettext('Billing')}
            </a>
            <ChatLauncher linkClassName="item" showNewBadge={true} inSidebar={inSidebar} />
        </>;
    }

    if (canUpgrade && isOrgOwner) {
        return <>
            <a href={billingUrl} className="item" target="_blank" rel="noopener noreferrer">{gettext('Billing')}</a>
            <ChatLauncher linkClassName="item" showNewBadge={true} inSidebar={inSidebar} />
        </>;
    }

    return <ChatLauncher linkClassName="item" showNewBadge={true} inSidebar={inSidebar} />;
}