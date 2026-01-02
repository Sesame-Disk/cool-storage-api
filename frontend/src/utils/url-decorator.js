import { siteRoot, historyRepoID } from './constants';
import { Utils } from './utils';
import { getToken } from './seafile-api';

class URLDecorator {

  static getUrl(options) {
    let url = '';
    let params = '';
    switch (options.type) {
      case 'download_historic_file':
        params = 'p=' + Utils.encodePath(options.filePath);
        url = siteRoot + 'repo/' + historyRepoID + '/' + options.objID + '/download?' + params;
        break;
      case 'download_file_url':
        // Include auth token in URL for downloads (opens in new tab without headers)
        const token = getToken();
        url = siteRoot + 'lib/' + options.repoID + '/file' + Utils.encodePath(options.filePath) + '?dl=1' + (token ? '&token=' + token : '');
        break;
      case 'file_revisions':
        params = 'p=' + Utils.encodePath(options.filePath);
        url = siteRoot + 'repo/file_revisions/' + options.repoID + '/?' + params;
        break;
      case 'open_via_client':
        url = 'seafile://openfile?repo_id=' + options.repoID + '&path=' + Utils.encodePath(options.filePath);
        break;
      default:
        url = '';
        break;
    }
    return url;
  }

}

export default URLDecorator;
