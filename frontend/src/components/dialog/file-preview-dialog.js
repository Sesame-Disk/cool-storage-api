import React from 'react';
import PropTypes from 'prop-types';
import { gettext, siteRoot } from '../../utils/constants';
import { Utils } from '../../utils/utils';

import '../../css/file-preview-dialog.css';

const propTypes = {
  repoID: PropTypes.string.isRequired,
  filePath: PropTypes.string.isRequired,
  fileName: PropTypes.string.isRequired,
  closePreview: PropTypes.func.isRequired,
};

class FilePreviewDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      textContent: null,
      textLoading: false,
      textError: null,
      iworkPreviewType: 'image',
    };
  }

  componentDidMount() {
    document.addEventListener('keydown', this.onKeyDown);
    if (this.isTextFile()) {
      this.loadTextContent();
    }
  }

  componentWillUnmount() {
    document.removeEventListener('keydown', this.onKeyDown);
  }

  onKeyDown = (e) => {
    if (e.key === 'Escape') {
      this.props.closePreview();
    }
  };

  onOverlayClick = (e) => {
    if (e.target === e.currentTarget) {
      this.props.closePreview();
    }
  };

  getRawURL = () => {
    const { repoID, filePath } = this.props;
    const path = Utils.encodePath(filePath);
    return `${siteRoot}repo/${repoID}/raw${path}`;
  };

  getDownloadURL = () => {
    const { repoID, filePath } = this.props;
    const path = Utils.encodePath(filePath);
    return `${siteRoot}lib/${repoID}/file${path}?dl=1`;
  };

  getFileExt = () => {
    const { fileName } = this.props;
    const idx = fileName.lastIndexOf('.');
    if (idx === -1) return '';
    return fileName.substring(idx + 1).toLowerCase();
  };

  isPDF = () => this.getFileExt() === 'pdf';

  isAppleIWork = () => {
    return ['pages', 'numbers', 'key'].includes(this.getFileExt());
  };

  getPreviewURL = () => {
    let url = this.getRawURL();
    url += (url.includes('?') ? '&' : '?') + 'preview=1';
    return url;
  };

  isVideo = () => {
    return ['mp4', 'webm', 'ogg', 'mov'].includes(this.getFileExt());
  };

  isAudio = () => {
    return ['mp3', 'wav', 'flac', 'aac'].includes(this.getFileExt());
  };

  isTextFile = () => {
    const textExts = [
      'txt', 'md', 'markdown', 'json', 'yaml', 'yml', 'xml', 'csv',
      'html', 'htm', 'css', 'js', 'ts', 'jsx', 'tsx',
      'py', 'go', 'rs', 'java', 'c', 'cpp', 'h', 'hpp',
      'sh', 'bash', 'zsh', 'fish',
      'toml', 'ini', 'cfg', 'conf', 'env',
      'sql', 'graphql', 'proto',
      'dockerfile', 'makefile',
      'rb', 'php', 'swift', 'kt', 'scala', 'r', 'lua', 'pl',
      'log', 'diff', 'patch',
    ];
    return textExts.includes(this.getFileExt());
  };

  loadTextContent = () => {
    this.setState({ textLoading: true });
    const rawURL = this.getRawURL();
    fetch(rawURL, { cache: 'no-cache' })
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        return res.text();
      })
      .then((text) => {
        this.setState({ textContent: text, textLoading: false });
      })
      .catch((err) => {
        this.setState({ textError: err.message, textLoading: false });
      });
  };

  renderPreviewContent = () => {
    const rawURL = this.getRawURL();
    const { fileName } = this.props;

    if (this.isPDF()) {
      return (
        <div className="file-preview-content file-preview-pdf">
          <iframe src={rawURL} title={fileName} />
        </div>
      );
    }

    if (this.isAppleIWork()) {
      const previewURL = this.getPreviewURL();
      if (this.state.iworkPreviewType === 'pdf') {
        return (
          <div className="file-preview-content file-preview-pdf">
            <iframe src={previewURL} title={fileName} />
          </div>
        );
      }
      // Default: show as image (most modern iWork files use JPEG previews)
      return (
        <div className="file-preview-content file-preview-media">
          <img src={previewURL} alt={fileName}
            style={{ maxWidth: '90vw', maxHeight: '80vh', objectFit: 'contain', borderRadius: '4px' }}
            onError={() => {
              // If image fails, try as PDF iframe
              this.setState({ iworkPreviewType: 'pdf' });
            }}
          />
        </div>
      );
    }

    if (this.isVideo()) {
      return (
        <div className="file-preview-content file-preview-media">
          <video controls autoPlay src={rawURL}>
            {gettext('Your browser does not support video playback.')}
          </video>
        </div>
      );
    }

    if (this.isAudio()) {
      return (
        <div className="file-preview-content file-preview-media">
          <audio controls autoPlay src={rawURL}>
            {gettext('Your browser does not support audio playback.')}
          </audio>
        </div>
      );
    }

    if (this.isTextFile()) {
      const { textContent, textLoading, textError } = this.state;
      return (
        <div className="file-preview-content file-preview-text">
          {textLoading && <div className="file-preview-loading">{gettext('Loading...')}</div>}
          {textError && <div className="file-preview-error">{gettext('Failed to load file')}: {textError}</div>}
          {textContent !== null && (
            <pre><code>{textContent}</code></pre>
          )}
        </div>
      );
    }

    return (
      <div className="file-preview-content file-preview-unsupported">
        <p>{gettext('Preview not available for this file type.')}</p>
      </div>
    );
  };

  render() {
    const { fileName } = this.props;
    const downloadURL = this.getDownloadURL();

    return (
      <div className="file-preview-overlay" onClick={this.onOverlayClick}>
        <div className="file-preview-header">
          <div className="file-preview-title" title={fileName}>{fileName}</div>
          <div className="file-preview-actions">
            <a href={downloadURL} className="file-preview-btn file-preview-download" title={gettext('Download')}>
              <i className="fas fa-download"></i>
            </a>
            <button className="file-preview-btn file-preview-close" onClick={this.props.closePreview} title={gettext('Close (Esc)')}>
              <i className="fas fa-times"></i>
            </button>
          </div>
        </div>
        <div className="file-preview-body" onClick={this.onOverlayClick}>
          {this.renderPreviewContent()}
        </div>
      </div>
    );
  }
}

FilePreviewDialog.propTypes = propTypes;

export default FilePreviewDialog;
