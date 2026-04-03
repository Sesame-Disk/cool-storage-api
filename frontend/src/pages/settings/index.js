import React from 'react';
import { gettext } from '../../utils/constants';
import SettingsContent from '../../components/user-settings/settings-content';

const SettingsPage = () => {
    return (
        <div className="main-panel-center user-settings-page">
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

export default SettingsPage;