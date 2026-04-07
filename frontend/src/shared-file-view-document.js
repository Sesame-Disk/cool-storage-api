import React from 'react';
import ReactDom from 'react-dom';
import { seafileAPI } from './utils/seafile-api';
import { gettext, siteRoot } from './utils/constants';
import SharedFileView from './components/shared-file-view/shared-file-view';
import SharedFileViewTip from './components/shared-file-view/shared-file-view-tip';
import Loading from './components/loading';
import PDFViewer from './components/pdf-viewer';

import 'bootstrap/dist/css/bootstrap.min.css';
import './css/pdf-file-view.css';

const {
  repoID, filePath, fileName, err,
  commitID, fileType, sharedToken
} = window.shared.pageOptions;

function buildOfficeConvertedPDFURL() {
  return `${siteRoot}office-convert/static/${repoID}/${commitID}${encodeURIComponent(filePath)}/1.pdf?token=${sharedToken}`;
}

class SharedFileViewDocument extends React.Component {
  render() {
    return <SharedFileView content={<FileContent />} />;
  }
}

class FileContent extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      isLoading: !err,
      errorMsg: ''
    };
  }

  componentDidMount() {
    if (err) {
      return;
    }

    let queryStatus = () => {
      seafileAPI.queryOfficeFileConvertStatus(repoID, commitID, filePath, fileType.toLowerCase(), sharedToken).then((res) => {
        const convertStatus = res.data['status'];
        switch (convertStatus) {
          case 'PROCESSING':
            this.setState({
              isLoading: true
            });
            setTimeout(queryStatus, 2000);
            break;
          case 'ERROR':
            this.setState({
              isLoading: false,
              errorMsg: gettext('Document convertion failed.')
            });
            break;
          case 'DONE':
            this.setState({
              isLoading: false,
              errorMsg: ''
            });
        }
      }).catch((error) => {
        if (error.response) {
          this.setState({
            isLoading: false,
            errorMsg: gettext('Document convertion failed.')
          });
        } else {
          this.setState({
            isLoading: false,
            errorMsg: gettext('Please check the network.')
          });
        }
      });
    };

    queryStatus();
  }

  render() {
    const { isLoading, errorMsg } = this.state;

    if (err) {
      return <SharedFileViewTip />;
    }

    if (isLoading) {
      return <Loading />;
    }

    if (errorMsg) {
      return <SharedFileViewTip errorMsg={errorMsg} />;
    }

    return (
      <div className="shared-file-view-body pdf-file-view">
        <PDFViewer src={buildOfficeConvertedPDFURL()} title={fileName} />
      </div>
    );
  }
}

ReactDom.render(<SharedFileViewDocument />, document.getElementById('wrapper'));
