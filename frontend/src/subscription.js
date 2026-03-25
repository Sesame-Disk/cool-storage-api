import React from 'react';
import ReactDOM from 'react-dom';
import { siteRoot, mediaUrl, logoPath, logoHeight, siteTitle, gettext } from './utils/constants';
import SideNav from './components/user-settings/side-nav';
import Account from './components/common/account';
import Notification from './components/common/notification';
import Subscription from './components/subscription';

import './css/toolbar.css';
import './css/search.css';
import './css/user-settings.css';


class UserSubscription extends React.Component {

  constructor(props) {
    super(props);
    this.sideNavItems = [
      { show: true, href: '#current-plan', text: gettext('Current Plan') },
      { show: true, href: '#asset-quota', text: gettext('Storage') },
      { show: true, href: '#traffic-quota', text: gettext('Traffic') },
      { show: true, href: '#current-subscription-period', text: gettext('Billing Cycle') },
      { show: true, href: '#product-price', text: gettext('Billing Details') },
    ];
    this.state = {
      curItemID: this.sideNavItems[0].href.slice(1),
    };
  }

  handleContentScroll = (e) => {
    // Mobile does not display the sideNav, so when scrolling don't update curItemID
    const scrollTop = e.target.scrollTop;
    const scrolled = this.sideNavItems.filter((item, index) => {
      const section = document.getElementById(item.href.slice(1));
      return item.show && section && section.offsetTop - 45 < scrollTop;
    });
    if (scrolled.length) {
      this.setState({
        curItemID: scrolled[scrolled.length - 1].href.slice(1)
      });
    }
  };

  render() {
    let logoUrl = logoPath.startsWith('http') ? logoPath : mediaUrl + logoPath;
    return (
      <div className="subscription-page h-100 d-flex flex-column">
        <div className="top-header d-flex justify-content-between">
          <a href={siteRoot}>
            <img src={logoUrl} height={logoHeight} style={{ width: 'auto' }} title={siteTitle} alt="logo" />
          </a>
          <div className="common-toolbar">
            <Notification />
            <Account />
          </div>
        </div>
        <div className="subscription-shell flex-auto d-flex o-hidden">
          <div className="side-panel o-auto">
            <SideNav data={this.sideNavItems} curItemID={this.state.curItemID} />
          </div>
          <div className="main-panel subscription-main-panel d-flex flex-column">
            <h2 className="heading">{gettext('Subscription')}</h2>
            <Subscription isOrgContext={false} handleContentScroll={this.handleContentScroll} />
          </div>
        </div>
      </div>
    );
  }
}

ReactDOM.render(
  <UserSubscription />,
  document.getElementById('wrapper')
);
