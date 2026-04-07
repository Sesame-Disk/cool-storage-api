import React from 'react';
import moment from 'moment';
import PropTypes from 'prop-types';
import { Utils } from '../../utils/utils';
import { siteRoot, gettext } from '../../utils/constants';
import { buildFileViewURL } from '../../utils/file-view-url';

class ResultsItem extends React.Component {

  handlerFileURL = (item) => {
    return item.is_dir ? siteRoot + 'library/' + item.repo_id + '/' + item.repo_name + item.fullpath :
      buildFileViewURL({ repoID: item.repo_id, filePath: item.fullpath });
  };

  handlerParentDirPath = (item) => {
    let index = item.is_dir ? item.fullpath.length - item.name.length - 1 : item.fullpath.length - item.name.length;
    return item.fullpath.substring(0, index);
  };

  handlerParentDirURL = (item) => {
    return siteRoot + 'library/' + item.repo_id + '/' + item.repo_name + this.handlerParentDirPath(item);
  };

  render() {
    let item = this.props.item;
    let linkContent = decodeURI(item.fullpath).substring(1);
    let folderIconUrl = linkContent ? Utils.getFolderIconUrl(false, 192) : Utils.getDefaultLibIconUrl(true);
    let fileIconUrl = item.is_dir ? folderIconUrl : Utils.getFileIconUrl(item.name, 192);

    if (item.thumbnail_url !== '') {
      fileIconUrl = item.thumbnail_url;
    }

    return (
      <li className="search-result-item">
        <img className={linkContent ? 'item-img' : 'lib-item-img'} src={fileIconUrl} alt="" />
        <div className="item-content">
          <div className="item-name ellipsis">
            <a href={this.handlerFileURL(item)} target="_blank" title={item.name} rel="noreferrer">{item.name}</a>
          </div>
          <div className="item-link ellipsis">
            <a href={this.handlerParentDirURL(item)} target="_blank" rel="noreferrer" >{item.repo_name}{this.handlerParentDirPath(item)}</a>
          </div>
          <div className="item-link ellipsis">
            {Utils.bytesToSize(item.size) + ' ' + moment(item.last_modified * 1000).format('YYYY-MM-DD')}
          </div>
          <div className="item-text ellipsis" dangerouslySetInnerHTML={{ __html: item.content_highlight }}></div>
        </div>
      </li>
    );
  }
}

const resultsItemPropTypes = {
  item: PropTypes.object.isRequired,
};

ResultsItem.propTypes = resultsItemPropTypes;

class SearchResults extends React.Component {

  render() {
    const { resultItems, total } = this.props;
    return (
      <div className="search-result-container position-static">
        <p className="tip">{total > 0 ? (total + ' ' + (total === 1 ? gettext('result') : gettext('results'))) : gettext('No result')}</p>
        <ul className="search-result-list">
          {resultItems.map((item, index) => {
            return <ResultsItem key={index} item={item} />;
          })}
        </ul>
      </div>
    );
  }
}

const searchResultsPropTypes = {
  resultItems: PropTypes.array.isRequired,
  total: PropTypes.number.isRequired
};

SearchResults.propTypes = searchResultsPropTypes;

export default SearchResults;
