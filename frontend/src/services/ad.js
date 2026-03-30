/* eslint-disable */
import React, { useState, useEffect } from 'react';
import { seafileAPI } from '../utils/seafile-api';
import { billingUrl, gettext } from '../utils/constants';
import { Utils } from '../utils/utils';
import toaster from '../components/toast';
import PropTypes from 'prop-types';
import { featureRequiresUpgrade, getHardPlanLimits, getUpgradeState, hasUpgradeFeatures } from '../utils/upgrade-state';
import './ad.css';

const HARD_PLAN_LIMITS = getHardPlanLimits();

function Ad(props) {
    const [error, setError] = useState(null);
    const [isLoaded, setIsLoaded] = useState(false);
    const [adsContent, setAdsContent] = useState('');

    let param = '';

    const concatParam = (key, arg) => {
        if (param === '') {
            param = '?' + key + '=' + arg;
        } else {
            param += '&' + key + '=' + arg;
        }

        return param;
    }

    if (props.zone)
        param = concatParam('zone', props.zone);
    if (props.size)
        param = concatParam('size', props.size);
    if (props.category)
        param = concatParam('category', props.category);

    useEffect(() => {
        fetch("https://dash.sesamedisk.com/ads/" + param)
            //fetch("https://test.sesamedisk.com:8003/ads/" + param)
            //fetch('http://192.168.100.100:8000/ads/' + param)
            .then(response => response.text())
            .then((result) => {
                setAdsContent(result);
                setIsLoaded(true);
            },
                (error) => {
                    setIsLoaded(true);
                    setError(error);
                }
            );
    }, []);

    if (error) {
        return null;
    } else if (!isLoaded) {
        return null;
    } else {
        return <div className={`divAds${props.center ? ' center-ads' : ''}`}
            dangerouslySetInnerHTML={{ __html: adsContent }} />;
    }
}

export default function InsertAd(props) {
    // Show ads for orgs that have locked features (free/restricted plan) or
    // for special flat-message category regardless of plan.
    const showAds = hasUpgradeFeatures() || props.category === 'Flat_message';

    if (showAds) {
        return <Ad zone={props.zone} size={props.size} category={props.category} center={props.center} />;
    } else return null;
}

InsertAd.propTypes = {
    zone: PropTypes.string,
    size: PropTypes.string,
    category: PropTypes.string,
    center: PropTypes.bool
}

InsertAd.defaultProps = {
    zone: '',
    size: '',
    category: '',
    center: false
}

export function InternalAd() {
    if (!hasUpgradeFeatures()) {
        return null
    }

    const [totalLinks, setTotalLinks] = useState(null);
    const [dismissed, setDismissed] = useState(false);
    const upgradeState = getUpgradeState();
    const shareLinkExpireDaysMax = upgradeState.shareLinkExpireDaysMax || HARD_PLAN_LIMITS.shareLinkExpireDaysMax;

    useEffect(() => {
        seafileAPI.listShareLinks({ page: 1 }).then((res) => {
            setTotalLinks(res.data.length);
        }).catch(() => {
            // Si falla, no mostramos el contador
        });
    }, []);

    if (dismissed) {
        return null;
    }

    const linksUsed = totalLinks !== null ? `${totalLinks}/${HARD_PLAN_LIMITS.maxShareLinks}` : `${HARD_PLAN_LIMITS.maxShareLinks}`;

    return (
        <div className='internal-ad internal-ad-compact'>
            <button
                className='internal-ad-close'
                onClick={() => setDismissed(true)}
                aria-label='Close'
            >
                ×
            </button>
            <p className='internal-ad-header'>
                <i className="fa fa-info-circle" />
                <strong>{gettext('Plan limits')}</strong>
                {totalLinks !== null && ` • ${linksUsed} links used`}
            </p>
            <p className='internal-ad-text'>
                {gettext('This plan currently limits sharing to %(shareCount)s share links, %(uploadCount)s upload link, and %(expireDays)s-day expiration. Upgrade to remove these limits.')
                    .replace('%(shareCount)s', HARD_PLAN_LIMITS.maxShareLinks)
                    .replace('%(uploadCount)s', HARD_PLAN_LIMITS.maxUploadLinks)
                    .replace('%(expireDays)s', shareLinkExpireDaysMax)}
            </p>
            <p>
                <a href={billingUrl} className='btn btn-sm btn-outline-primary' target='_blank' rel='noopener noreferrer'>{gettext('View Plans & Pricing')}</a>
            </p>
        </div>
    )
}

/**
 * EvalProFunc — wraps `fn` so it shows an upgrade toast when the org has
 * locked features. Pass `featureKey` (e.g. "add_group") to show a targeted
 * message; omit it for a generic upgrade prompt.
 */
export const EvalProFunc = (fn, { manOrg = false, shareLinks = false, featureKey = null } = {}) => {
    if (featureKey ? !featureRequiresUpgrade(featureKey) : !hasUpgradeFeatures()) return fn

    return () => {
        toaster.warning(gettext('Upgrade your plan to use this feature.'), {
            duration: 10,
            description: (
                <div className='mt-3 toast-upgrade-info'>
                    {manOrg && (
                        <>
                            <ul className='features-list'>
                                <li>
                                    {gettext('Add and manage users in your organization')}
                                </li>
                                <li>
                                    {gettext('Secure file and library sharing with granular permissions')}
                                </li>
                                <li>
                                    {gettext('Create teams and groups for collaboration')}
                                </li>
                                <li>
                                    {gettext('Access audit logs, activity tracking, and priority support')}
                                </li>
                            </ul>
                            <div className='upgrade-limits-info'>
                                <p><strong>{gettext('Current plan limits:')}</strong></p>
                                <p className='limits-comparison'>
                                    <span className='limit-free'>{gettext('Storage and traffic quotas are enforced by your current plan.')}</span>
                                    <span className='limit-pro'>{gettext('Upgrade for larger quotas and broader collaboration features.')}</span>
                                </p>
                            </div>
                        </>
                    )}
                    {shareLinks && (
                        <div className='upgrade-limits-info'>
                            <p><strong>{gettext('Sharing limits reached')}</strong></p>
                            <p className='limits-comparison'>
                                <span className='limit-free'>
                                    {gettext('Current plan: %(shareCount)s share links, %(uploadCount)s upload link, %(expireDays)s-day expiration')
                                        .replace('%(shareCount)s', HARD_PLAN_LIMITS.maxShareLinks)
                                        .replace('%(uploadCount)s', HARD_PLAN_LIMITS.maxUploadLinks)
                                        .replace('%(expireDays)s', HARD_PLAN_LIMITS.shareLinkExpireDaysMax)}
                                </span>
                                <span className='limit-pro'>{gettext('Upgrade for more sharing capacity and longer-lived links.')}</span>
                            </p>
                        </div>
                    )}
                    <a href={billingUrl} className='btn btn-sm btn-outline-primary' target='_blank' rel='noopener noreferrer'>{gettext('View Plans & Pricing')}</a>
                </div>
            ),
        })
    }
}

let isCheckingQuota = false;

export const EvalQuotaShareLinks = (fn) => {
    if (!hasUpgradeFeatures()) return fn

    return () => {
        if (isCheckingQuota) return;

        isCheckingQuota = true;

        seafileAPI.listShareLinks({ page: 1 }).then((res) => {
            if (res.data.length >= HARD_PLAN_LIMITS.maxShareLinks) {
                const newFn = EvalProFunc(fn, { shareLinks: true })
                if (newFn) {
                    newFn()
                }
            } else {
                fn()
            }
        }).catch(error => {
            let errMessage = Utils.getErrorMsg(error);
            toaster.danger(errMessage);
        }).finally(() => {
            isCheckingQuota = false;
        });
    }
}

export const EvalQuotaUploadLinks = (fn) => {
    if (!hasUpgradeFeatures()) return fn

    return () => {
        if (isCheckingQuota) return;

        isCheckingQuota = true;

        seafileAPI.listUserUploadLinks().then((res) => {
            if (res.data.length >= HARD_PLAN_LIMITS.maxUploadLinks) {
                const newFn = EvalProFunc(fn, { shareLinks: true })
                if (newFn) {
                    newFn()
                }
            } else {
                fn()
            }
        }).catch(error => {
            let errMessage = Utils.getErrorMsg(error);
            toaster.danger(errMessage);
        }).finally(() => {
            isCheckingQuota = false;
        });
    }
}
