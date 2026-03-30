/* eslint-disable */
import React, { useState } from 'react';
import { gettext, siteRoot } from '../utils/constants';
import { getUpgradeState } from '../utils/upgrade-state';

/**
 * QuotaBanner — top-of-page dismissible banner shown when the org has exceeded
 * a storage or traffic quota.
 *
 * Reads live from window.app.pageOptions (populated asynchronously by app.js),
 * so it always reflects the current quota state after the account/info response
 * arrives. Because this is a function component rendered inside App, it will
 * re-render when the parent state updates after loadUserPermissions completes.
 *
 * Visibility rules:
 *  - Storage over quota → show storage banner (all users, upgrade CTA for owner)
 *  - Traffic over quota → show traffic banner (all users, upgrade CTA for owner)
 *  - Both → show two banners
 *  - Neither → render nothing
 */
export default function QuotaBanner() {
  const [dismissedStorage, setDismissedStorage] = useState(false);
  const [dismissedTraffic, setDismissedTraffic] = useState(false);

  const { storageInfo, trafficInfo, isOrgOwner, trafficResetDate } = getUpgradeState();

  const storageOver = storageInfo.over_quota === true && !dismissedStorage;
  const trafficOver = trafficInfo.over_quota === true && !dismissedTraffic;

  if (!storageOver && !trafficOver) return null;

  const billingUrl = `${siteRoot}billing/`;

  return (
    <>
      {storageOver && (
        <div id="quota-banner-storage" className="d-flex justify-content-between align-items-center quota-banner quota-banner-error">
          <p className="m-0">
            {gettext('Storage quota exceeded. New uploads are blocked until space is freed or the plan is updated.')}
            {' '}
            {isOrgOwner
              ? <a href={billingUrl}>{gettext('Manage Billing')}</a>
              : gettext('Contact your organization owner to resolve it.')}
          </p>
          <button
            className="close sf2-icon-x1"
            title={gettext('Close')}
            aria-label={gettext('Close')}
            onClick={() => setDismissedStorage(true)}
          />
        </div>
      )}
      {trafficOver && (
        <div id="quota-banner-traffic" className="d-flex justify-content-between align-items-center quota-banner quota-banner-error">
          <p className="m-0">
            {gettext('Traffic quota exceeded. Uploads and downloads are blocked until the limit resets or billing is updated.')}
            {trafficResetDate ? ` ${gettext('Reset date:')} ${trafficResetDate}.` : ''}
            {' '}
            {isOrgOwner
              ? <a href={billingUrl}>{gettext('Manage Billing')}</a>
              : gettext('Contact your organization owner to resolve it.')}
          </p>
          <button
            className="close sf2-icon-x1"
            title={gettext('Close')}
            aria-label={gettext('Close')}
            onClick={() => setDismissedTraffic(true)}
          />
        </div>
      )}
    </>
  );
}
