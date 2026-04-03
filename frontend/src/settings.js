import React from 'react';
import ReactDom from 'react-dom';
import { navigate } from '@gatsbyjs/reach-router';
import { Utils } from './utils/utils';
import { siteRoot, mediaUrl, logoPath, logoHeight, siteTitle } from './utils/constants';
import { getToken } from './utils/seafile-api';
import CommonToolbar from './components/toolbar/common-toolbar';
import SettingsContent from './components/user-settings/settings-content';

import './css/toolbar.css';
import './css/search.css';

class Settings extends React.Component {

  onSearchedClick = (selectedItem) => {
    if (selectedItem.is_dir === true) {
      let url = siteRoot + 'library/' + selectedItem.repo_id + '/' + selectedItem.repo_name + selectedItem.path;
      navigate(url, { repalce: true });
    } else {
      const token = getToken();
      let url = siteRoot + 'lib/' + selectedItem.repo_id + '/file' + Utils.encodePath(selectedItem.path) + (token ? '?token=' + encodeURIComponent(token) : '');
      let newWindow = window.open('about:blank');
      newWindow.location.href = url;
    }
  };

  render() {
    return (
      <React.Fragment>
        <div className="h-100 d-flex flex-column">
          <div className="top-header d-flex justify-content-between">
            <a href={siteRoot}>
              <img src={mediaUrl + logoPath} height={logoHeight} style={{ width: 'auto' }} title={siteTitle} alt="logo" />
            </a>
            <CommonToolbar onSearchedClick={this.onSearchedClick} />
          </div>
          <SettingsContent className="flex-auto" />
        </div>
      </React.Fragment>
    );
  }
}

ReactDom.render(<Settings />, document.getElementById('wrapper'));
