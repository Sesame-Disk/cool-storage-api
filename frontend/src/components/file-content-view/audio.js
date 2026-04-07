import React from 'react';
import AudioPlayer from '../audio-player';

import '../../css/audio-file-view.css';

const { rawPath } = window.app.pageOptions;

class FileContent extends React.Component {
  render() {
    const audioOptions = {
      autoplay: false,
      controls: true,
      preload: 'metadata',
      sources: [{
        src: rawPath
      }]
    };
    return (
      <div className="file-view-content flex-1 audio-file-view">
        <AudioPlayer {...audioOptions} />
      </div>
    );
  }
}

export default FileContent;
