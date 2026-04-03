import React, { Component } from 'react';
import ReactDom from 'react-dom';
import { Router, navigate } from '@gatsbyjs/reach-router';
import MediaQuery from 'react-responsive';
import { Modal } from 'reactstrap';
import { siteRoot } from './utils/constants';
import { Utils } from './utils/utils';
import { isAuthenticated, seafileAPI, getToken } from './utils/seafile-api';
import LoginPage from './pages/login';
import SSOPage from './pages/sso';
import SystemNotification from './components/system-notification';
import QuotaBanner from './components/quota-banner';
import SidePanel from './components/side-panel';
import MainPanel from './components/main-panel';
import FilesActivities from './pages/dashboard/files-activities';
import MyFileActivities from './pages/dashboard/my-file-activities';
import Starred from './pages/starred/starred';
import SettingsPage from './pages/settings';
import LinkedDevices from './pages/linked-devices/linked-devices';
import ShareAdminLibraries from './pages/share-admin/libraries';
import ShareAdminFolders from './pages/share-admin/folders';
import ShareAdminShareLinks from './pages/share-admin/share-links';
import ShareAdminUploadLinks from './pages/share-admin/upload-links';
import SharedLibraries from './pages/shared-libs/shared-libs';
import ShareWithOCM from './pages/share-with-ocm/shared-with-ocm';
import OCMViaWebdav from './pages/ocm-via-webdav/ocm-via-webdav';
import OCMRepoDir from './pages/share-with-ocm/remote-dir-view';
import MyLibraries from './pages/my-libs/my-libs';
import MyLibDeleted from './pages/my-libs/my-libs-deleted';
import PublicSharedView from './pages/shared-with-all/public-shared-view';
import LibContentView from './pages/lib-content-view/lib-content-view';
import FileHistory from './pages/file-history';
import RepoTrash from './pages/repo-trash';
import RepoHistoryView from './pages/repo-history-view';
import RepoSnapshot from './pages/repo-snapshot';
import Group from './pages/groups/group-view';
import Groups from './pages/groups/groups-view';
import InvitationsView from './pages/invitations/invitations-view';
import Wikis from './pages/wikis/wikis';
import MainContentWrapper from './components/main-content-wrapper';

// Bootstrap CSS (required for reactstrap components)
import 'bootstrap/dist/css/bootstrap.min.css';

import './css/layout.css';
import './css/toolbar.css';
import './css/search.css';

import './services/css.css';

const FilesActivitiesWrapper = MainContentWrapper(FilesActivities);
const MyFileActivitiesWrapper = MainContentWrapper(MyFileActivities);
const StarredWrapper = MainContentWrapper(Starred);
const LinkedDevicesWrapper = MainContentWrapper(LinkedDevices);
const SharedLibrariesWrapper = MainContentWrapper(SharedLibraries);
const SharedWithOCMWrapper = MainContentWrapper(ShareWithOCM);
const OCMViaWebdavWrapper = MainContentWrapper(OCMViaWebdav);
const ShareAdminLibrariesWrapper = MainContentWrapper(ShareAdminLibraries);
const ShareAdminFoldersWrapper = MainContentWrapper(ShareAdminFolders);

class App extends Component {

  constructor(props) {
    super(props);
    this.state = {
      isOpen: false,
      isSidePanelClosed: false,
      currentTab: '/',
      pathPrefix: [],
      isCheckingAuth: true,
      isLoggedIn: false,
      isSSOCallback: false,
    };
    this.dirViewPanels = ['my-libs', 'shared-libs', 'org']; // and group
    window.onpopstate = this.onpopstate;
  }

  onpopstate = (event) => {
    if (event.state && event.state.currentTab && event.state.pathPrefix) {
      let { currentTab, pathPrefix } = event.state;
      this.setState({ currentTab, pathPrefix });
    }
  };

  UNSAFE_componentWillMount() {
    if (!Utils.isDesktop()) {
      this.setState({
        isSidePanelClosed: true
      });
    }
  }

  navigateClientUrlToLib = () => {
    if (window.location.hash && window.location.hash.indexOf('common/lib') !== -1) {
      let splitUrlArray = window.location.hash.split('/');
      let repoID = splitUrlArray[splitUrlArray.length - 2];
      let url = siteRoot + 'library/' + repoID + '/';
      navigate(url, { repalce: true });
    }
  };

  componentDidMount() {
    // Check authentication status
    const loggedIn = isAuthenticated();
    const pathname = window.location.pathname;
    const isLoginPage = pathname === '/login/' || pathname === '/login';
    const isSSOPage = pathname === '/sso/' || pathname === '/sso';

    // SSO page handles its own auth flow
    if (isSSOPage) {
      this.setState({
        isCheckingAuth: false,
        isLoggedIn: false,
        isSSOCallback: true,
      });
      return;
    }

    if (!loggedIn && !isLoginPage) {
      // Redirect to login if not authenticated, preserving the original URL
      const next = encodeURIComponent(window.location.pathname + window.location.search + window.location.hash);
      window.location.href = '/login/?next=' + next;
      return;
    }

    if (loggedIn && isLoginPage) {
      // Redirect to home if already authenticated and on login page
      window.location.href = '/';
      return;
    }

    this.setState({
      isCheckingAuth: false,
      isLoggedIn: loggedIn,
    });

    if (!loggedIn) {
      return; // Don't initialize app state for login page
    }

    // Bootstrap is the primary source of truth for page options.
    // Keep account/info only as a fallback if bootstrap did not hydrate the page.
    if (window.app?.pageOptions?.bootstrapReady !== true) {
      this.loadUserPermissions();
    }

    // url from client  e.g. http://127.0.0.1:8000/#common/lib/34e7fb92-e91d-499d-bcde-c30ea8af9828/
    // navigate to library page http://127.0.0.1:8000/library/34e7fb92-e91d-499d-bcde-c30ea8af9828/
    this.navigateClientUrlToLib();

    // Extract current tab from URL path segment
    let href = window.location.href.split('/');
    this.setState({ currentTab: href[href.length - 2] });
  }

  loadUserPermissions = () => {
    // Fetch account info and update global permissions
    seafileAPI.getAccountInfo().then(resp => {
      const data = resp.data;

      // Identity
      window.app.pageOptions.name = data.name || '';
      window.app.pageOptions.username = data.email || '';
      window.app.pageOptions.contactEmail = data.contact_email || data.email || '';
      window.app.pageOptions.userRole = data.role || 'user';

      // Capability flags — backend resolves role AND enforcement profile.
      // Fall back to role-derived defaults only for old backends without these fields.
      const role = data.role || 'user';
      const canWrite = role === 'admin' || role === 'user';

      window.app.pageOptions.canAddRepo = data.can_add_repo !== undefined ? data.can_add_repo : canWrite;
      window.app.pageOptions.canShareRepo = data.can_share_repo !== undefined ? data.can_share_repo : canWrite;
      window.app.pageOptions.canAddGroup = data.can_add_group !== undefined ? data.can_add_group : canWrite;
      window.app.pageOptions.canGenerateShareLink = data.can_generate_share_link !== undefined ? data.can_generate_share_link : canWrite;
      window.app.pageOptions.canGenerateUploadLink = data.can_generate_upload_link !== undefined ? data.can_generate_upload_link : canWrite;
      window.app.pageOptions.canSendShareLinkMail = data.can_send_share_link_mail !== undefined ? data.can_send_share_link_mail : canWrite;
      window.app.pageOptions.canInviteGuest = data.can_invite_guest !== undefined ? data.can_invite_guest : false;
      window.app.pageOptions.canPublishRepo = data.can_publish_repo !== undefined ? data.can_publish_repo : false;

      // Plan & upgrade CTA
      window.app.pageOptions.plan = data.plan || '';
      window.app.pageOptions.isOrgOwner = data.is_org_owner === true;
      window.app.pageOptions.canUpgrade = data.can_upgrade === true;
      window.app.pageOptions.billingCycle = data.billing_cycle || '';
      window.app.pageOptions.maxUsers = Number(data.max_users) || 0;
      window.app.pageOptions.currentUsers = Number(data.current_users) || 0;
      window.app.pageOptions.currentPeriodStartedAt = data.current_period_started_at || null;
      window.app.pageOptions.currentPeriodEndsAt = data.current_period_ends_at || null;
      // upgradeFeatures: list of short feature names the user can unlock by upgrading
      // (e.g. ["add_group", "invite_guest"]) — empty for paid orgs.
      window.app.pageOptions.upgradeFeatures = Array.isArray(data.upgrade_features) ? data.upgrade_features : [];

      // Enforcement limits for share/upload link creation UI
      window.app.pageOptions.shareLinkExpireDaysMax = data.share_link_expire_days_max || 0;
      window.app.pageOptions.uploadLinkExpireDaysMax = data.upload_link_expire_days_max || 0;

      // Org-level storage quota object  { used, quota, percent, over_quota }
      window.app.pageOptions.storageInfo = data.storage || null;

      // Org-level traffic quota object  { used, quota, percent, over_quota, upload_used,
      //   upload_quota, upload_over_quota, download_used, download_quota,
      //   download_over_quota, reset_date }
      window.app.pageOptions.trafficInfo = data.traffic || null;

      // Force a re-render to pick up new permissions
      this.forceUpdate();
    }).catch(error => {
      console.error('Failed to load user permissions:', error);
      // On error, default to restrictive permissions
      window.app.pageOptions.canAddRepo = false;
    });
  };

  onCloseSidePanel = () => {
    this.setState({
      isSidePanelClosed: !this.state.isSidePanelClosed
    });
  };

  onShowSidePanel = () => {
    this.setState({
      isSidePanelClosed: !this.state.isSidePanelClosed
    });
  };

  onSearchedClick = (selectedItem) => {
    if (selectedItem.is_dir === true) {
      this.setState({ currentTab: '', pathPrefix: [] });
      let url = siteRoot + 'library/' + selectedItem.repo_id + '/' + selectedItem.repo_name + selectedItem.path;
      navigate(url, { repalce: true });
    } else {
      const token = getToken();
      let url = siteRoot + 'lib/' + selectedItem.repo_id + '/file' + Utils.encodePath(selectedItem.path) + (token ? '?token=' + encodeURIComponent(token) : '');
      let isWeChat = Utils.isWeChat();
      if (!isWeChat) {
        let newWindow = window.open('about:blank');
        newWindow.location.href = url;
      } else {
        location.href = url;
      }
    }
  };

  onGroupChanged = (groupID) => {
    setTimeout(function () {
      let url;
      if (groupID) {
        url = siteRoot + 'group/' + groupID + '/';
      }
      else {
        url = siteRoot + 'groups/';
      }
      window.location = url.toString();
    }, 1);
  };

  tabItemClick = (tabName, groupID) => {
    let pathPrefix = [];
    if (groupID || this.dirViewPanels.indexOf(tabName) > -1) {
      pathPrefix = this.generatorPrefix(tabName, groupID);
    }
    this.setState({
      currentTab: tabName,
      pathPrefix: pathPrefix
    }, () => {
      let { currentTab, pathPrefix } = this.state;
      window.history.replaceState({ currentTab: currentTab, pathPrefix: pathPrefix }, null);
    });
    if (!Utils.isDesktop() && !this.state.isSidePanelClosed) {
      this.setState({ isSidePanelClosed: true });
    }
  };

  generatorPrefix = (tabName, groupID) => {
    let pathPrefix = [];
    if (groupID) {
      let navTab1 = {
        url: siteRoot + 'groups/',
        showName: 'Groups',
        name: 'groups',
        id: null,
      };
      let navTab2 = {
        url: siteRoot + 'group/' + groupID + '/',
        showName: tabName,
        name: tabName,
        id: groupID,
      };
      pathPrefix.push(navTab1);
      pathPrefix.push(navTab2);
    } else {
      let navTab = {
        url: siteRoot + tabName + '/',
        showName: this.getTabShowName(tabName),
        name: tabName,
        id: null,
      };
      pathPrefix.push(navTab);
    }
    return pathPrefix;
  };

  getTabShowName = (tabName) => {
    if (tabName === 'my-libs') {
      return 'Libraries';
    }
    if (tabName === 'shared-libs') {
      return 'Shared with me';
    }
    if (tabName === 'org') {
      return 'Shared with all';
    }
  };

  toggleSidePanel = () => {
    this.setState({
      isSidePanelClosed: !this.state.isSidePanelClosed
    });
  };

  render() {
    let { currentTab, isSidePanelClosed, isCheckingAuth, isLoggedIn, isSSOCallback } = this.state;
    const normalizedPath = window.location.pathname.replace(/\/+$/, '') || '/';
    const normalizedSiteRoot = siteRoot === '/' ? '' : siteRoot.replace(/\/+$/, '');
    const settingsRoutePath = `${normalizedSiteRoot}/profile`;
    const isStandaloneSettingsRoute = normalizedPath === settingsRoutePath;

    // Show loading while checking auth
    if (isCheckingAuth) {
      return (
        <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100vh' }}>
          <div>Loading...</div>
        </div>
      );
    }

    // Show SSO callback page
    if (isSSOCallback) {
      return <SSOPage />;
    }

    // Show login page if not authenticated
    if (!isLoggedIn) {
      return <LoginPage />;
    }

    // Show "My Libraries" for users who can create, "Shared Libraries" for readonly/guest
    const userCanAddRepo = window.app.pageOptions.canAddRepo;
    const home = userCanAddRepo ?
      <MyLibraries path={siteRoot} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} /> :
      <SharedLibrariesWrapper path={siteRoot} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />;

    return (
      <React.Fragment>
        <SystemNotification />
        <QuotaBanner />
        <div id="main">
          {!isStandaloneSettingsRoute && (
            <SidePanel isSidePanelClosed={this.state.isSidePanelClosed} onCloseSidePanel={this.onCloseSidePanel} currentTab={currentTab} tabItemClick={this.tabItemClick} />
          )}
          <MainPanel>
            <Router className="reach-router">
              {home}
              <FilesActivitiesWrapper path={siteRoot + 'dashboard'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <MyFileActivitiesWrapper path={siteRoot + 'my-activities'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <StarredWrapper path={siteRoot + 'starred'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <SettingsPage path={siteRoot + 'profile'} onSearchedClick={this.onSearchedClick} />
              <SettingsPage path={siteRoot + 'profile/'} onSearchedClick={this.onSearchedClick} />
              <LinkedDevicesWrapper path={siteRoot + 'linked-devices'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <ShareAdminLibrariesWrapper path={siteRoot + 'share-admin-libs'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <ShareAdminFoldersWrapper path={siteRoot + 'share-admin-folders'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <ShareAdminShareLinks path={siteRoot + 'share-admin-share-links'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <ShareAdminUploadLinks path={siteRoot + 'share-admin-upload-links'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <SharedLibrariesWrapper path={siteRoot + 'shared-libs'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <SharedWithOCMWrapper path={siteRoot + 'shared-with-ocm'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <OCMViaWebdavWrapper path={siteRoot + 'ocm-via-webdav'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <MyLibraries path={siteRoot + 'my-libs'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <MyLibDeleted path={siteRoot + 'my-libs/deleted/'} onSearchedClick={this.onSearchedClick} />
              <LibContentView path={siteRoot + 'library/:repoID/*'} pathPrefix={this.state.pathPrefix} onMenuClick={this.onShowSidePanel} onTabNavClick={this.tabItemClick} />
              <FileHistory path={siteRoot + 'repo/file_revisions/:repoID/'} />
              <RepoTrash path={siteRoot + 'repo/:repoID/trash/'} />
              <RepoHistoryView path={siteRoot + 'repo/history/:repoID/'} />
              <RepoSnapshot path={siteRoot + 'repo/:repoID/snapshot/'} />
              <OCMRepoDir path={siteRoot + 'remote-library/:providerID/:repoID/*'} pathPrefix={this.state.pathPrefix} onMenuClick={this.onShowSidePanel} onTabNavClick={this.tabItemClick} />
              <Groups path={siteRoot + 'groups'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <Group
                path={siteRoot + 'group/:groupID'}
                onShowSidePanel={this.onShowSidePanel}
                onSearchedClick={this.onSearchedClick}
                onTabNavClick={this.tabItemClick}
                onGroupChanged={this.onGroupChanged}
              />
              <Wikis path={siteRoot + 'published'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
              <PublicSharedView path={siteRoot + 'org/'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} onTabNavClick={this.tabItemClick} />
              <InvitationsView path={siteRoot + 'invitations/'} onShowSidePanel={this.onShowSidePanel} onSearchedClick={this.onSearchedClick} />
            </Router>
          </MainPanel>
          {!isStandaloneSettingsRoute && (
            <MediaQuery query="(max-width: 767.8px)">
              <Modal zIndex="1030" isOpen={!isSidePanelClosed} toggle={this.toggleSidePanel} contentClassName="d-none"></Modal>
            </MediaQuery>
          )}
        </div>
      </React.Fragment>
    );
  }
}

ReactDom.render(<App />, document.getElementById('wrapper'));
