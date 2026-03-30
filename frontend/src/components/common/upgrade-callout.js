import React from 'react';
import PropTypes from 'prop-types';
import { billingUrl, gettext } from '../../utils/constants';

const propTypes = {
    title: PropTypes.string.isRequired,
    description: PropTypes.string.isRequired,
    ctaText: PropTypes.string,
    ctaHref: PropTypes.string,
    note: PropTypes.string,
    className: PropTypes.string,
};

export default function UpgradeCallout({
    title,
    description,
    ctaText = gettext('Open Billing'),
    ctaHref = billingUrl,
    note = '',
    className = '',
}) {
    return (
        <div className={`alert alert-warning d-flex flex-wrap align-items-start justify-content-between mb-3 ${className}`.trim()} role="alert">
            <div className="mr-3">
                <div className="font-weight-bold mb-1">{title}</div>
                <div>{description}</div>
                {note && <div className="small text-secondary mt-2">{note}</div>}
            </div>
            {ctaHref && ctaText && (
                <a
                    className="btn btn-sm btn-primary mt-2 mt-md-0"
                    href={ctaHref}
                    rel="noopener noreferrer"
                    target="_blank"
                >
                    {ctaText}
                </a>
            )}
        </div>
    );
}

UpgradeCallout.propTypes = propTypes;