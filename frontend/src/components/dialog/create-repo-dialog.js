import React from 'react';
import PropTypes from 'prop-types';
import { Button, Input, Form, FormGroup, Label, Alert } from 'reactstrap';
import { gettext, enableEncryptedLibrary, repoPasswordMinLength, storages, libraryTemplates } from '../../utils/constants';
import { SeahubSelect } from '../common/select';

const normalizeStorages = (storageList) => (Array.isArray(storageList)
  ? storageList.map((item) => {
    if (typeof item === 'string') {
      return { id: item, name: item, is_default: false, region: '' };
    }
    return {
      id: item?.id || '',
      name: item?.name || item?.id || '',
      is_default: item?.is_default === true,
      region: typeof item?.region === 'string' ? item.region.trim().toLowerCase() : '',
    };
  }).filter((item) => item.id)
  : []);

const readCreateRepoContext = () => {
  const pageOptions = window.app?.pageOptions || {};
  const normalizedStorages = normalizeStorages(pageOptions.storages || storages);
  const rawPolicy = pageOptions.orgStoragePolicy || {};
  const dataResidency = rawPolicy.data_residency === 'strict' ? 'strict' : 'flexible';
  const defaultRegion = typeof rawPolicy.default_region === 'string' ? rawPolicy.default_region.trim().toLowerCase() : '';
  const pinnedStorage = defaultRegion ? normalizedStorages.find((item) => item.region === defaultRegion) || null : null;

  return {
    normalizedStorages,
    storagePolicy: { dataResidency, defaultRegion },
    pinnedStorage,
  };
};

const propTypes = {
  libraryType: PropTypes.string.isRequired,
  onCreateRepo: PropTypes.func.isRequired,
  onCreateToggle: PropTypes.func.isRequired,
};

class CreateRepoDialog extends React.Component {
  constructor(props) {
    super(props);
    const createRepoContext = readCreateRepoContext();
    this.normalizedStorages = createRepoContext.normalizedStorages;
    this.storagePolicy = createRepoContext.storagePolicy;
    this.pinnedStorage = createRepoContext.pinnedStorage;
    this.state = {
      repoName: '',
      disabled: true,
      encrypt: false,
      password1: '',
      password2: '',
      errMessage: '',
      permission: 'rw',
      storage_id: '',
      library_template: libraryTemplates.length ? libraryTemplates[0] : '',
      isSubmitBtnActive: false,
    };
    this.templateOptions = [];
    this.storageOptions = [];
    this.automaticStorageOption = { value: '', label: gettext('Automatic') };
    if (Array.isArray(libraryTemplates) && libraryTemplates.length) {
      this.templateOptions = libraryTemplates.map((item) => { return {value: item, label: item}; });
    }
    if (this.normalizedStorages.length) {
      this.storageOptions = this.normalizedStorages.map((item) => { return {value: item.id, label: item.name}; });
    }
  }

  handleRepoNameChange = (e) => {
    if (!e.target.value.trim()) {
      this.setState({isSubmitBtnActive: false});
    } else {
      this.setState({isSubmitBtnActive: true});
    }

    this.setState({repoName: e.target.value});
  };

  handlePassword1Change = (e) => {
    this.setState({password1: e.target.value});
  };

  handlePassword2Change = (e) => {
    this.setState({password2: e.target.value});
  };

  handleSubmit = () => {
    let isValid = this.validateInputParams();
    if (isValid) {
      let repoData = this.prepareRepoData();
      if (this.props.libraryType === 'department') {
        this.props.onCreateRepo(repoData, 'department');
        return;
      }
      this.props.onCreateRepo(repoData);
    }
  };

  handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      this.handleSubmit();
      e.preventDefault();
    }
  };

  toggle = () => {
    this.props.onCreateToggle();
  };

  validateInputParams() {
    let errMessage = '';
    let repoName = this.state.repoName.trim();
    if (!repoName.length) {
      errMessage = gettext('Name is required');
      this.setState({errMessage: errMessage});
      return false;
    }
    if (repoName.indexOf('/') > -1) {
      errMessage = gettext('Name should not include \'/\'.');
      this.setState({errMessage: errMessage});
      return false;
    }
    if (this.storagePolicy.dataResidency === 'strict' && !this.pinnedStorage) {
      errMessage = gettext('This organization storage policy is misconfigured. Please contact an administrator or support.');
      this.setState({errMessage: errMessage});
      return false;
    }
    if (this.state.encrypt) {
      let password1 = this.state.password1.trim();
      let password2 = this.state.password2.trim();
      if (!password1.length) {
        errMessage = gettext('Please enter password');
        this.setState({errMessage: errMessage});
        return false;
      }
      if (!password2.length) {
        errMessage = gettext('Please enter the password again');
        this.setState({errMessage: errMessage});
        return false;
      }
      if (password1.length < repoPasswordMinLength) {
        errMessage = gettext('Password is too short');
        this.setState({errMessage: errMessage});
        return false;
      }
      if (password1 !== password2) {
        errMessage = gettext('Passwords don\'t match');
        this.setState({errMessage: errMessage});
        return false;
      }
    }
    return true;
  }

  onPermissionChange = (e) => {
    let permission = e.target.value;
    this.setState({permission: permission});
  };

  handleStorageInputChange = (selectedItem) => {
    this.setState({storage_id: selectedItem ? selectedItem.value : ''});
  };

  handlelibraryTemplatesInputChange = (selectedItem) => {
    this.setState({library_template: selectedItem.value});
  };

  onEncrypted = (e) => {
    let isChecked = e.target.checked;
    this.setState({
      encrypt: isChecked,
      disabled: !isChecked
    });
  };

  prepareRepoData = () => {
    let libraryType = this.props.libraryType;

    let repoName = this.state.repoName.trim();
    let password = this.state.encrypt ? this.state.password1 : '';
    let permission = this.state.permission;
    let encrypted = this.state.encrypt;

    let repo = null;
    if (libraryType === 'mine' || libraryType === 'public') {
      repo = {
        name: repoName,
        passwd: password,
        encrypted: encrypted
      };
    }
    if (libraryType === 'group') {
      repo = {
        repo_name: repoName,
        password: password,
        permission: permission,
        encrypted: encrypted
      };
    }
    if (libraryType === 'department') {
      repo = {
        repo_name: repoName,
        passwd: password,
        encrypted: encrypted
      };
    }

    const storage_id = this.state.storage_id;
    if (this.storagePolicy.dataResidency !== 'strict' && storage_id) {
      repo.storage_id = storage_id;
    }

    const library_template = this.state.library_template;
    if (library_template) {
      repo.library_template = library_template;
    }

    return repo;
  };

  render() {
    const isStrictPolicy = this.storagePolicy.dataResidency === 'strict';
    const selectedStorageOption = this.storageOptions.find((opt) => opt.value === this.state.storage_id) || this.automaticStorageOption;

    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('New Library')}</h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close"><span aria-hidden="true">&times;</span></button>
            </div>
            <div className="modal-body">
              <Form>
                <FormGroup>
                  <Label for="repoName">{gettext('Name')}</Label>
                  <Input
                    id="repoName"
                    onKeyDown={this.handleKeyDown}
                    value={this.state.repoName}
                    onChange={this.handleRepoNameChange}
                    autoFocus={true}
                  />
                </FormGroup>

                {libraryTemplates.length > 0 && (
                  <FormGroup>
                    <Label>{gettext('Template')}</Label>
                    <SeahubSelect
                      defaultValue={this.templateOptions[0]}
                      options={this.templateOptions}
                      onChange={this.handlelibraryTemplatesInputChange}
                      value={this.templateOptions.find(opt => opt.value === this.state.library_template) || null}
                    />
                  </FormGroup>
                )}

                {isStrictPolicy && (
                  <FormGroup>
                    <Label>{gettext('Pinned region')}</Label>
                    <Input value={this.pinnedStorage ? this.pinnedStorage.name : gettext('Unavailable')} disabled={true} />
                    <p className="text-muted mb-0 mt-2">
                      {this.pinnedStorage
                        ? gettext('This organization uses strict data residency. New libraries are pinned to this region.')
                        : gettext('This organization storage policy is misconfigured. Please contact an administrator or support.')}
                    </p>
                  </FormGroup>
                )}

                {!isStrictPolicy && this.normalizedStorages.length > 0 && (
                  <FormGroup>
                    <Label>{gettext('Region')}</Label>
                    <SeahubSelect
                      defaultValue={this.automaticStorageOption}
                      options={[this.automaticStorageOption].concat(this.storageOptions)}
                      onChange={this.handleStorageInputChange}
                      value={selectedStorageOption}
                    />
                    <p className="text-muted mb-0 mt-2">
                      {gettext('Automatic uses the request region first, then the organization fallback region, then the global storage default. Choosing a region here overrides that automatic resolution.')}
                    </p>
                  </FormGroup>
                )}

                {this.props.libraryType === 'group' && (
                  <FormGroup>
                    <Label for="exampleSelect">{gettext('Permission')}</Label>
                    <Input type="select" name="select" id="exampleSelect" onChange={this.onPermissionChange} value={this.state.permission}>
                      <option value='rw'>{gettext('Read-Write')}</option>
                      <option value='r'>{gettext('Read-Only')}</option>
                    </Input>
                  </FormGroup>
                )}
                {enableEncryptedLibrary &&
                  <div>
                    <FormGroup check>
                      <Input type="checkbox" id="encrypt" onChange={this.onEncrypted} />
                      <Label for="encrypt">{gettext('Encrypt')}</Label>
                    </FormGroup>
                    {!this.state.disabled &&
                      <FormGroup>
                        {/* todo translate */}
                        <Label for="passwd1">{gettext('Password')}</Label><span className="tip">{' '}{gettext('(at least {placeholder} characters)').replace('{placeholder}', repoPasswordMinLength)}</span>
                        <Input
                          id="passwd1"
                          type="password"
                          disabled={this.state.disabled}
                          value={this.state.password1}
                          onChange={this.handlePassword1Change}
                          autoComplete="new-password"
                        />
                      </FormGroup>
                    }
                    {!this.state.disabled &&
                      <FormGroup>
                        <Label for="passwd2">{gettext('Password again')}</Label>
                        <Input
                          id="passwd2"
                          type="password"
                          disabled={this.state.disabled}
                          value={this.state.password2}
                          onChange={this.handlePassword2Change}
                          autoComplete="new-password"
                        />
                      </FormGroup>
                    }
                  </div>
                }
              </Form>
              {this.state.errMessage && <Alert color="danger">{this.state.errMessage}</Alert>}
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

CreateRepoDialog.propTypes = propTypes;

export default CreateRepoDialog;
