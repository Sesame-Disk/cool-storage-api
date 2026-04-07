import React from 'react';
import PropTypes from 'prop-types';
import { gettext } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import { Button, Input, Alert } from 'reactstrap';

const propTypes = {
  onRename: PropTypes.func.isRequired,
  toggleCancel: PropTypes.func.isRequired,
  checkDuplicatedName: PropTypes.func.isRequired,
  dirent: PropTypes.object,
};

class Rename extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      newName: '',
      errMessage: '',
      isSubmitBtnActive: false,
    };
    this.newInput = React.createRef();
  }

  UNSAFE_componentWillMount() {
    this.setState({newName: this.props.dirent.name});
  }

  UNSAFE_componentWillReceiveProps(nextProps) {
    this.changeState(nextProps.dirent);
  }

  handleChange = (e) => {
    if (!e.target.value.trim()) {
      this.setState({isSubmitBtnActive: false});
    } else {
      this.setState({isSubmitBtnActive: true});
    }

    this.setState({newName: e.target.value});
  };

  handleSubmit = () => {
    let { isValid, errMessage } = this.validateInput();
    if (!isValid) {
      this.setState({errMessage : errMessage});
    } else {
      let isDuplicated = this.checkDuplicatedName();
      if (isDuplicated) {
        let errMessage = gettext('The name "{name}" is already taken. Please choose a different name.');
        errMessage = errMessage.replace('{name}', Utils.HTMLescape(this.state.newName));
        this.setState({errMessage: errMessage});
      } else {
        this.props.onRename(this.state.newName);
        this.props.toggleCancel();
      }
    }
  };

  handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      this.handleSubmit();
    }
  };

  toggle = () => {
    this.props.toggleCancel();
  };

  changeState = (dirent) => {
    let name = dirent.name;
    this.setState({newName: name});
  };

  validateInput = () => {
    let newName = this.state.newName.trim();
    let isValid = true;
    let errMessage = '';
    if (!newName) {
      isValid = false;
      errMessage = gettext('Name is required.');
      return { isValid, errMessage };
    }

    if (newName.indexOf('/') > -1) {
      isValid = false;
      errMessage = gettext('Name should not include ' + '\'/\'' + '.');
      return { isValid, errMessage };
    }

    return { isValid, errMessage };
  };

  checkDuplicatedName = () => {
    let isDuplicated = this.props.checkDuplicatedName(this.state.newName);
    return isDuplicated;
  };

  onAfterModelOpened = () => {
    if (!this.newInput.current) return;
    this.newInput.current.focus();

    let { dirent } = this.props;
    let type = dirent.type;
    if (type === 'file') {
      var endIndex = dirent.name.lastIndexOf('.md');
      this.newInput.current.setSelectionRange(0, endIndex, 'forward');
    } else {
      this.newInput.current.setSelectionRange(0, -1);
    }
  };

  componentDidMount() {
    let { dirent } = this.props;
    this.changeState(dirent);
    // Focus and select on mount
    setTimeout(() => this.onAfterModelOpened(), 100);
  }

  render() {
    let type = this.props.dirent.type;
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{type === 'file' ? gettext('Rename File') : gettext('Rename Folder')}</h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close"><span aria-hidden="true">&times;</span></button>
            </div>
            <div className="modal-body">
              <p>{type === 'file' ? gettext('New file name') : gettext('New folder name')}</p>
              <Input onKeyDown={this.handleKeyDown} innerRef={this.newInput} value={this.state.newName} onChange={this.handleChange} autoFocus />
              {this.state.errMessage && <Alert color="danger" className="mt-2">{this.state.errMessage}</Alert>}
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={this.toggle}>{gettext('Cancel')}</Button>
              <Button color="primary" onClick={this.handleSubmit} disabled={!this.state.isSubmitBtnActive}>{gettext('Submit')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

Rename.propTypes = propTypes;

export default Rename;
