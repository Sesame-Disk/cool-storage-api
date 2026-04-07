import React from 'react';
import ReactDom from 'react-dom';
import SharedFileView from './components/shared-file-view/shared-file-view';
import SharedFileViewTip from './components/shared-file-view/shared-file-view-tip';

import 'bootstrap/dist/css/bootstrap.min.css';
import './css/pdf-file-view.css';

const { rawPath, fileName, err } = window.shared.pageOptions;

class SharedFileViewPDF extends React.Component {
  render() {
    return <SharedFileView content={<FileContent />} fileType="pdf" />;
  }
}

class FileContent extends React.Component {
  render() {
    if (err) {
      return <SharedFileViewTip />;
    }

    return (
      <div className="shared-file-view-body pdf-file-view">
        <embed src={rawPath} type="application/pdf" title={fileName || 'PDF preview'} width="100%" height="100%" style={{ border: 'none' }} />
      </div>
    );
  }
}

ReactDom.render(<SharedFileViewPDF />, document.getElementById('wrapper'));
