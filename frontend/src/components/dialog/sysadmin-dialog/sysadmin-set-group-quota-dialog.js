import React from 'react';
import PropTypes from 'prop-types';
import { Button, Input, InputGroupAddon, InputGroup } from 'reactstrap';
import { gettext } from '../../../utils/constants';
import { BYTES_IN_GB } from '../../../utils/quota-units';
import { seafileAPI } from '../../../utils/seafile-api';
import { Utils } from '../../../utils/utils';
import toaster from '../../toast';

const propTypes = {
  toggle: PropTypes.func.isRequired,
  groupID: PropTypes.number.isRequired,
  onSetQuota: PropTypes.func.isRequired,
};

class SetGroupQuotaDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      quota: '',
      errMessage: '',
    };
  }

  setGroupQuota = () => {
    const numberReg = /^[1-9]\d*$/im;
    let quota = this.state.quota;
    if ((quota.length && numberReg.test(quota)) || quota === -2) {
      this.setState({ errMessage: '' });
      let newQuota = this.state.quota === -2 ? this.state.quota : this.state.quota * BYTES_IN_GB;
      seafileAPI.sysAdminUpdateDepartmentQuota(this.props.groupID, newQuota).then((res) => {
        this.props.toggle();
        this.props.onSetQuota(res.data);
      }).catch(error => {
        let errMessage = Utils.getErrorMsg(error);
        toaster.danger(errMessage);
      });
    } else {
      const err = gettext('Quota is invalid.');
      this.setState({ errMessage: err });
    }
  };

  handleChange = (e) => {
    const quota = e.target.value.trim();
    this.setState({ quota: quota });
  };

  handleKeyDown = (e) => {
    if (e.key === 'Enter') {
      this.setGroupQuota();
      e.preventDefault();
    }
  };

  render() {
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('Set Quota')}</h5>
              <button type="button" className="close" onClick={this.props.toggle} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>
            <div className="modal-body">
              <InputGroup>
                <Input
                  onKeyDown={this.handleKeyDown}
                  value={this.state.quota}
                  onChange={this.handleChange}
                  autoFocus={true}
                />
                <InputGroupAddon addonType="append">{'GB'}</InputGroupAddon>
              </InputGroup>
              <p className="tip">
                <br /><span>{gettext('An integer that is greater than 0 or equal to -2.')}</span><br />
                <span>{gettext('Tip: -2 means no limit.')}</span>
              </p>
              {this.state.errMessage && <p className="error">{this.state.errMessage}</p>}
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={this.props.toggle}>{gettext('Cancel')}</Button>
              <Button color="primary" onClick={this.setGroupQuota}>{gettext('Submit')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

SetGroupQuotaDialog.propTypes = propTypes;

export default SetGroupQuotaDialog;
