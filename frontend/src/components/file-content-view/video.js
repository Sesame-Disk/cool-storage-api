import React from 'react';
import VideoPlayer from '../video-player';

import '../../css/video-file-view.css';

const {
  rawPath
} = window.app.pageOptions;

class FileContent extends React.Component {
  render() {
    const videoOptions = {
      autoplay: false,
      controls: true,
      preload: 'metadata',
      sources: [{
        src: rawPath
      }]
    };
    return (
      <div className="file-view-content flex-1 video-file-view">
        <VideoPlayer {...videoOptions} />
      </div>
    );
  }
}

export default FileContent;
