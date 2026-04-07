import React from 'react';
import ReactDom from 'react-dom';
import { MarkdownViewer } from '@seafile/seafile-editor';
import { seafileAPI } from './utils/seafile-api';
import { Utils } from './utils/utils';
import { mediaUrl } from './utils/constants';
import { rewriteSharedMarkdownNode } from './utils/share-link-markdown-url';
import SharedFileView from './components/shared-file-view/shared-file-view';
import SharedFileViewTip from './components/shared-file-view/shared-file-view-tip';
import Loading from './components/loading';
import toaster from './components/toast';

import 'bootstrap/dist/css/bootstrap.min.css';

const { repoID, sharedToken, rawPath, filePath, smartLinkMap, err } = window.shared.pageOptions;

class SharedFileViewMarkdown extends React.Component {
  render() {
    return <SharedFileView content={<FileContent />} fileType="md" />;
  }
}

class FileContent extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      markdownContent: '',
      loading: !err
    };
  }

  componentDidMount() {
    seafileAPI.getFileContent(rawPath).then((res) => {
      this.setState({
        markdownContent: res.data,
        loading: false
      });
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      this.setState({ loading: false });
      toaster.danger(errMessage);
    });
  }

  rewriteMarkdownNode = (innerNode) => {
    return rewriteSharedMarkdownNode(innerNode, {
      repoID,
      sharedToken,
      currentFilePath: filePath,
      smartLinkMap,
    });
  };

  modifyValueBeforeRender = (value) => {
    return Utils.changeMarkdownNodes(value, this.rewriteMarkdownNode);
  };

  render() {
    if (err) {
      return <SharedFileViewTip />;
    }

    if (this.state.loading) {
      return <Loading />;
    }

    return (
      <div className="shared-file-view-body md-view">
        <MarkdownViewer
          value={this.state.markdownContent}
          isShowOutline={true}
          mathJaxSource={mediaUrl + 'js/mathjax/tex-svg.js'}
          beforeRenderCallback={this.modifyValueBeforeRender}
        />
      </div>
    );
  }
}

ReactDom.render(<SharedFileViewMarkdown />, document.getElementById('wrapper'));
