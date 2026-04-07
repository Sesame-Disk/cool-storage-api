import React from 'react';
import ReactDom from 'react-dom';
import SharedFileView from './components/shared-file-view/shared-file-view';
import SharedFileViewTip from './components/shared-file-view/shared-file-view-tip';
import VideoPlayer from './components/video-player';

import 'bootstrap/dist/css/bootstrap.min.css';
import './css/video-file-view.css';

const { rawPath, rawContentType, err } = window.shared.pageOptions;

class SharedFileViewImage extends React.Component {
  render() {
    return <SharedFileView content={<FileContent />} />;
  }
}

class FileContent extends React.Component {
  render() {
    if (err) {
      return <SharedFileViewTip />;
    }

    const videoOptions = {
      autoplay: false,
      controls: true,
      preload: 'metadata',
      sources: rawContentType ? [{ src: rawPath, type: rawContentType }, { src: rawPath }] : [{ src: rawPath }]
    };

    return (
      <div className="shared-file-view-body d-flex">
        <div className="flex-1">
          <VideoPlayer {...videoOptions} />
        </div>
      </div>
    );
  }
}

ReactDom.render(<SharedFileViewImage />, document.getElementById('wrapper'));
