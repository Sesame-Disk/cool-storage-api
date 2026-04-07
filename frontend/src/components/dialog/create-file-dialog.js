import React from 'react';
import PropTypes from 'prop-types';
import { Button, Input, Form, FormGroup, Label, Alert } from 'reactstrap';
import { gettext } from '../../utils/constants';
import { Utils, validateName } from '../../utils/utils';

const propTypes = {
  fileType: PropTypes.string,
  parentPath: PropTypes.string.isRequired,
  onAddFile: PropTypes.func.isRequired,
  checkDuplicatedName: PropTypes.func.isRequired,
  toggleDialog: PropTypes.func.isRequired,
};

class CreateFile extends React.Component {
  constructor(props) {
    super(props);
    const { fileType = '' } = props;
    this.state = {
      parentPath: '',
      childName: fileType,
      errMessage: '',
      isSubmitBtnActive: this.isSdocSuffix(fileType) ? true : false,
    };
    this.newInput = React.createRef();
  }

  componentDidMount() {
    let parentPath = this.props.parentPath;
    if (parentPath[parentPath.length - 1] === '/') {  // mainPanel
      this.setState({parentPath: parentPath});
    } else {
      this.setState({parentPath: parentPath + '/'}); // sidePanel
    }
    // Focus input after mount
    setTimeout(() => {
      if (this.newInput.current) {
        this.newInput.current.focus();
        this.newInput.current.setSelectionRange(0, 0);
      }
    }, 100);
  }

  isSdocSuffix = (name) => {
    return name.endsWith('.sdoc');
  };

  handleChange = (e) => {
    if (!e.target.value.trim()) {
      this.setState({isSubmitBtnActive: false});
    } else {
      this.setState({isSubmitBtnActive: true});
    }

    this.setState({
      childName: e.target.value,
    }) ;
  };

  handleSubmit = () => {
    if (!this.state.isSubmitBtnActive) {
      return;
    }
    let isDuplicated = this.checkDuplicatedName();
    let newName = this.state.childName.trim();
    let { isValid, errMessage } = validateName(newName);
    if (!isValid) {
      this.setState({ errMessage });
      return;
    }

    if (isDuplicated) {
      let errMessage = gettext('The name "{name}" is already taken. Please choose a different name.');
      errMessage = errMessage.replace('{name}', Utils.HTMLescape(newName));
      this.setState({errMessage: errMessage});
    } else {
      let path = this.state.parentPath + newName;
      this.props.onAddFile(path);
      this.props.toggleDialog();
    }
  };

  handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      this.handleSubmit();
      e.preventDefault();
    }
  };

  checkDuplicatedName = () => {
    let isDuplicated = this.props.checkDuplicatedName(this.state.childName);
    return isDuplicated;
  };

  render() {
    const { toggleDialog } = this.props;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('New File')}</h5>
              <button type="button" className="close" onClick={toggleDialog} aria-label="Close"><span aria-hidden="true">&times;</span></button>
            </div>
            <div className="modal-body">
              <Form>
                <FormGroup>
                  <Label for="fileName">{gettext('Name')}</Label>
                  <Input
                    id="fileName"
                    onKeyDown={this.handleKeyDown}
                    innerRef={this.newInput}
                    value={this.state.childName}
                    onChange={this.handleChange}
                  />
                </FormGroup>
              </Form>
              {this.state.errMessage && <Alert color="danger" className="mt-2">{this.state.errMessage}</Alert>}
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={toggleDialog}>{gettext('Cancel')}</Button>
              <Button color="primary" onClick={this.handleSubmit} disabled={!this.state.isSubmitBtnActive}>{gettext('Submit')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

CreateFile.propTypes = propTypes;

export default CreateFile;
