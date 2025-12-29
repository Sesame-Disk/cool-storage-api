/* eslint-disable */
import React, { useState, useEffect } from 'react';
import { seafileAPI } from '../utils/seafile-api';
import { Utils } from '../utils/utils';
import toaster from '../components/toast';
import PropTypes from 'prop-types';
import './ad.css';
import { isFreeUser } from '../utils/constants';

const MAX_FREE_SHARELINKS = 3
const MAX_FREE_UPLOADLINKS = 1

function Ad(props) {
    const [error, setError] = useState(null);
    const [isLoaded, setIsLoaded] = useState(false);
    const [adsContent, setAdsContent] = useState('');

    let param = '';

    const concatParam = (key, arg) => {
        if (param == '') {
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
        console.log('Error: ' + error);
        return null;
    } else if (!isLoaded) {
        return null;
    } else {
        return <div className={`divAds${props.center ? ' center-ads' : ''}`}
            dangerouslySetInnerHTML={{ __html: adsContent }} />;
    }
}

export default function InsertAd(props) {
    const { userRole } = window.app.pageOptions;

    const showAds = userRole === 'personalfree' || userRole === 'restricted' || props.category == 'Flat_message';

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
    if (!isFreeUser) {
        return null
    }

    const [totalLinks, setTotalLinks] = useState(null);
    const [dismissed, setDismissed] = useState(false);
    const upgradeLink = "/billing/"

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

    const linksUsed = totalLinks !== null ? `${totalLinks}/${MAX_FREE_SHARELINKS}` : `${MAX_FREE_SHARELINKS}`;

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
                <strong>Free Plan</strong>
                {totalLinks !== null && ` • ${linksUsed} links used`}
            </p>
            <p className='internal-ad-text'>
                Shared links for free accounts are valid for 3 days. Please upgrade to get unlimited sharing/upload links and no expiration date!
            </p>
            <p>
                <a href={upgradeLink} className='btn btn-sm btn-outline-primary'>View Plans & Pricing</a>
            </p>
        </div>
    )
}

export const EvalProFunc = (fn, { manOrg = false, shareLinks = false } = {}) => {
    if (!isFreeUser) return fn

    const upgradeLink = "/billing/"
    return () => {
        toaster.warning("Please upgrade your account to use this feature!", {
            duration: 10,
            description: (
                <div className='mt-3 toast-upgrade-info'>
                    {manOrg && (
                        <>
                            <ul className='features-list'>
                                <li>
                                    ✓ Add and manage users in your organization
                                </li>
                                <li>
                                    ✓ Secure file/library sharing with granular permissions
                                </li>
                                <li>
                                    ✓ Create teams and groups for collaboration
                                </li>
                                <li>
                                    ✓ Audit logs, activity tracking & priority support
                                </li>
                            </ul>
                            <div className='upgrade-limits-info'>
                                <p><strong>Your Free Plan Includes:</strong></p>
                                <p className='limits-comparison'>
                                    <span className='limit-free'>📦 Storage: 2 GB</span>
                                    <span className='limit-free'>🌐 Monthly Traffic: 10 GB</span>
                                    <span className='limit-pro'>💎 Pro Plans: Up to 1000s of GB + on-demand scaling</span>
                                </p>
                            </div>
                        </>
                    )}
                    {shareLinks && (
                        <div className='upgrade-limits-info'>
                            <p><strong>Free Plan Limit Reached</strong></p>
                            <p className='limits-comparison'>
                                <span className='limit-free'>Free: 3 share links, 3 upload link, 3-day expiration</span>
                                <span className='limit-pro'>Pro: Unlimited links, no expiration</span>
                            </p>
                        </div>
                    )}
                    <a href={upgradeLink} className='btn btn-sm btn-outline-primary'>View Plans & Pricing</a>
                </div>
            ),
        })
    }
}

let isCheckingQuota = false;

export const EvalQuotaShareLinks = (fn) => {
    if (!isFreeUser) return fn

    return () => {
        if (isCheckingQuota) return;

        isCheckingQuota = true;

        seafileAPI.listShareLinks({ page: 1 }).then((res) => {
            if (res.data.length >= MAX_FREE_SHARELINKS) {
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
    if (!isFreeUser) return fn

    return () => {
        if (isCheckingQuota) return;

        isCheckingQuota = true;

        seafileAPI.listUserUploadLinks().then((res) => {
            if (res.data.length >= MAX_FREE_UPLOADLINKS) {
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