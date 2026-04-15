import React from 'react';
import ReactDom from 'react-dom';
import CommonToolbar from '../../components/toolbar/common-toolbar';
import Logo from '../../components/logo';
import SearchViewPanel from './main-panel';
import { siteRoot } from '../../utils/constants';
import { buildFileViewURL } from '../../utils/file-view-url';

import '../../css/layout.css';
import '../../css/toolbar.css';
import '../../css/search.css';

class SearchView extends React.Component {

  onSearchedClick = (selectedItem) => {
    let url = selectedItem.is_dir ?
      siteRoot + 'library/' + selectedItem.repo_id + '/' + selectedItem.repo_name + selectedItem.path :
      buildFileViewURL({ repoID: selectedItem.repo_id, filePath: selectedItem.path });
    let newWindow = window.open('about:blank');
    newWindow.location.href = url;
  };

  render() {
    return (
      <div className="w-100 h-100">
        <div className="main-panel-north border-left-show">
          <Logo />
          <CommonToolbar onSearchedClick={this.onSearchedClick} />
        </div>
        <div className="main-panel-south">
          <SearchViewPanel />
        </div>
      </div>
    );
  }
}

ReactDom.render(<SearchView />, document.getElementById('wrapper'));
