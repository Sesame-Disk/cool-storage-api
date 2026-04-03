import React from 'react';
import { navigate } from '@gatsbyjs/reach-router';
import PropTypes from 'prop-types';
import { gettext } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import { siteRoot, mediaUrl, logoPath, logoHeight, siteTitle } from '../../utils/constants';
import { getToken } from '../../utils/seafile-api';
import CommonToolbar from '../../components/toolbar/common-toolbar';
import SettingsContent from '../../components/user-settings/settings-content';

const SettingsPage = ({ onSearchedClick }) => {
    const handleSearchedClick = onSearchedClick || ((selectedItem) => {
        if (selectedItem.is_dir === true) {
            const url = siteRoot + 'library/' + selectedItem.repo_id + '/' + selectedItem.repo_name + selectedItem.path;
            navigate(url, { repalce: true });
            return;
        }

        const token = getToken();
        const url = siteRoot + 'lib/' + selectedItem.repo_id + '/file' + Utils.encodePath(selectedItem.path) + (token ? '?token=' + encodeURIComponent(token) : '');
        const newWindow = window.open('about:blank');
        newWindow.location.href = url;
    });

    return (
        <div className="main-panel-center user-settings-page">
            <div className="main-panel-north user-settings-page__header d-flex justify-content-between align-items-center">
                <a href={siteRoot} className="user-settings-page__logo-link">
                    <img src={mediaUrl + logoPath} height={logoHeight} style={{ width: 'auto' }} title={siteTitle} alt="logo" />
                </a>
                <CommonToolbar onSearchedClick={handleSearchedClick} />
            </div>
            <div className="cur-view-container">
                <div className="cur-view-path">
                    <h3 className="sf-heading m-0">{gettext('Settings')}</h3>
                </div>
                <div className="cur-view-content p-0">
                    <SettingsContent className="h-100 user-settings-page__content" showHeading={false} />
                </div>
            </div>
        </div>
    );
};

SettingsPage.propTypes = {
    onSearchedClick: PropTypes.func,
};

SettingsPage.defaultProps = {
    onSearchedClick: null,
};

export default SettingsPage;