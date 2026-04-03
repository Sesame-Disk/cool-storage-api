import React from 'react';
import PropTypes from 'prop-types';
import { isPro, isDBSqlite3, gettext } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { Utils } from '../../utils/utils';
import toaster from '../toast';
import SideNav from './side-nav';
import UserAvatarForm from './user-avatar-form';
import UserBasicInfoForm from './user-basic-info-form';
import WebAPIAuthToken from './web-api-auth-token';
import WebdavPassword from './webdav-password';
import LanguageSetting from './language-setting';
import ListInAddressBook from './list-in-address-book';
import EmailNotice from './email-notice';
import TwoFactorAuthentication from './two-factor-auth';
import SocialLogin from './social-login';
import SocialLoginDingtalk from './social-login-dingtalk';
import SocialLoginSAML from './social-login-saml';
import DeleteAccount from './delete-account';
import { getSettingsPageOptions, getSettingsRoute } from './page-options';

import './settings-content.css';

function buildUserInfoFromPageOptions() {
    const pageOptions = getSettingsPageOptions();
    const email = pageOptions.username || '';
    if (!email) {
        return null;
    }

    return {
        email,
        contact_email: pageOptions.contactEmail || email,
        login_id: pageOptions.loginID || email,
        name: pageOptions.name || email,
        avatar_url: pageOptions.avatarURL || '/static/img/default-avatar.png',
    };
}

class SettingsContent extends React.Component {
    constructor(props) {
        super(props);
        const pageOptions = getSettingsPageOptions();
        const canUpdatePassword = Boolean(pageOptions.canUpdatePassword);
        const enableGetAuthToken = Boolean(pageOptions.enableGetAuthToken);
        const enableWebdavSecret = Boolean(pageOptions.enableWebdavSecret);
        const enableAddressBook = Boolean(pageOptions.enableAddressBook);
        const twoFactorAuthEnabled = Boolean(pageOptions.twoFactorAuthEnabled);
        const enableWechatWork = Boolean(pageOptions.enableWechatWork);
        const enableDingtalk = Boolean(pageOptions.enableDingtalk);
        const isOrgContext = Boolean(pageOptions.isOrgContext);
        const enableADFS = Boolean(pageOptions.enableADFS);
        const enableMultiADFS = Boolean(pageOptions.enableMultiADFS);
        const enableDeleteAccount = Boolean(pageOptions.enableDeleteAccount);

        this.sideNavItems = [
            { show: true, href: '#user-basic-info', text: gettext('Profile') },
            { show: canUpdatePassword, href: '#update-user-passwd', text: gettext('Password') },
            { show: enableGetAuthToken, href: '#get-auth-token', text: gettext('Web API Auth Token') },
            { show: enableWebdavSecret, href: '#update-webdav-passwd', text: gettext('WebDav Password') },
            { show: enableAddressBook, href: '#list-in-address-book', text: gettext('Global Address Book') },
            { show: true, href: '#lang-setting', text: gettext('Language') },
            { show: isPro, href: '#email-notice', text: gettext('Email Notification') },
            { show: twoFactorAuthEnabled, href: '#two-factor-auth', text: gettext('Two-Factor Authentication') },
            { show: (enableWechatWork || enableDingtalk || enableADFS || (enableMultiADFS || isOrgContext)), href: '#social-auth', text: gettext('Social Login') },
            { show: enableDeleteAccount, href: '#del-account', text: gettext('Delete Account') },
        ];

        this.state = {
            curItemID: this.sideNavItems[0].href.substr(1),
            userInfo: buildUserInfoFromPageOptions(),
        };
    }

    componentDidMount() {
        seafileAPI.getUserInfo().then((res) => {
            this.setState({
                userInfo: res.data
            });
        }).catch((error) => {
            if (!this.state.userInfo) {
                toaster.danger(Utils.getErrorMsg(error));
            }
        });
    }

    updateUserInfo = (data) => {
        seafileAPI.updateUserInfo(data).then((res) => {
            this.setState({
                userInfo: res.data
            });
            toaster.success(gettext('Success'));
        }).catch((error) => {
            toaster.danger(Utils.getErrorMsg(error));
        });
    };

    handleContentScroll = (event) => {
        const scrollTop = event.target.scrollTop;
        const scrolled = this.sideNavItems.filter((item) => {
            const section = document.getElementById(item.href.substr(1));
            return item.show && section && section.offsetTop - 45 < scrollTop;
        });

        if (scrolled.length) {
            this.setState({
                curItemID: scrolled[scrolled.length - 1].href.substr(1)
            });
        }
    };

    render() {
        const pageOptions = getSettingsPageOptions();
        const canUpdatePassword = Boolean(pageOptions.canUpdatePassword);
        const passwordOperationText = pageOptions.passwordOperationText || gettext('Change');
        const enableGetAuthToken = Boolean(pageOptions.enableGetAuthToken);
        const enableWebdavSecret = Boolean(pageOptions.enableWebdavSecret);
        const enableAddressBook = Boolean(pageOptions.enableAddressBook);
        const twoFactorAuthEnabled = Boolean(pageOptions.twoFactorAuthEnabled);
        const enableWechatWork = Boolean(pageOptions.enableWechatWork);
        const enableDingtalk = Boolean(pageOptions.enableDingtalk);
        const isOrgContext = Boolean(pageOptions.isOrgContext);
        const enableADFS = Boolean(pageOptions.enableADFS);
        const enableMultiADFS = Boolean(pageOptions.enableMultiADFS);
        const enableDeleteAccount = Boolean(pageOptions.enableDeleteAccount);
        const passwordChangeUrl = getSettingsRoute('passwordChange', 'accounts/password/change/');

        return (
            <div className={`user-settings-layout ${this.props.className}`.trim()}>
                <div className="user-settings-layout__nav">
                    <SideNav data={this.sideNavItems} curItemID={this.state.curItemID} />
                </div>
                <div className="user-settings-layout__main">
                    {this.props.showHeading && <h2 className="user-settings-layout__heading">{gettext('Settings')}</h2>}
                    <div className="user-settings-layout__content position-relative" onScroll={this.handleContentScroll}>
                        <div id="user-basic-info" className="user-settings-layout__section">
                            <h3 className="user-settings-layout__section-heading">{gettext('Profile Setting')}</h3>
                            <UserAvatarForm />
                            {this.state.userInfo && <UserBasicInfoForm userInfo={this.state.userInfo} updateUserInfo={this.updateUserInfo} />}
                        </div>
                        {canUpdatePassword &&
                            <div id="update-user-passwd" className="user-settings-layout__section">
                                <h3 className="user-settings-layout__section-heading">{gettext('Password')}</h3>
                                <a href={passwordChangeUrl} className="btn btn-outline-primary">{passwordOperationText}</a>
                            </div>
                        }

                        {enableGetAuthToken && <WebAPIAuthToken />}
                        {enableWebdavSecret && <WebdavPassword />}
                        {enableAddressBook && this.state.userInfo &&
                            <ListInAddressBook userInfo={this.state.userInfo} updateUserInfo={this.updateUserInfo} />}
                        <LanguageSetting />
                        {(isPro || !isDBSqlite3) && <EmailNotice />}
                        {twoFactorAuthEnabled && <TwoFactorAuthentication />}
                        {enableWechatWork && <SocialLogin />}
                        {enableDingtalk && <SocialLoginDingtalk />}
                        {(enableADFS || (enableMultiADFS && isOrgContext)) && <SocialLoginSAML />}
                        {enableDeleteAccount && <DeleteAccount />}
                    </div>
                </div>
            </div>
        );
    }
}

SettingsContent.propTypes = {
    className: PropTypes.string,
    showHeading: PropTypes.bool,
};

SettingsContent.defaultProps = {
    className: '',
    showHeading: true,
};

export default SettingsContent;