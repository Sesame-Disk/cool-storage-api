import React from 'react';
import { Button } from 'reactstrap';
import { gettext } from '../../utils/constants';
import ModalPortal from '../modal-portal';
import { getSettingsPageOptions, getSettingsRoute } from './page-options';

class SocialLoginSAML extends React.Component {

  constructor(props) {
    super(props);
    this.form = React.createRef();
    this.state = {
      isConfirmDialogOpen: false
    };
  }

  confirmDisconnect = () => {
    this.setState({
      isConfirmDialogOpen: true
    });
  };

  disconnect = () => {
    this.form.current.submit();
  };

  toggleDialog = () => {
    this.setState({
      isConfirmDialogOpen: !this.state.isConfirmDialogOpen
    });
  };

  render() {
    const pageOptions = getSettingsPageOptions();
    const csrfToken = pageOptions.csrfToken || '';
    const isOrgContext = Boolean(pageOptions.isOrgContext);
    const orgID = pageOptions.orgID || '';
    const samlConnected = Boolean(pageOptions.samlConnected);
    const enableMultiADFS = Boolean(pageOptions.enableMultiADFS);
    const orgSamlConnected = Boolean(pageOptions.orgSamlConnected);
    const socialNextPage = pageOptions.socialNextPage || '/';
    let connectUrl = (enableMultiADFS && isOrgContext)
      ? getSettingsRoute('orgSamlConnect', 'org/custom/{orgID}/saml2/connect/?next={next}', { orgID, next: socialNextPage })
      : getSettingsRoute('samlConnect', 'saml2/connect/?next={next}', { next: socialNextPage });
    let disconnectUrl = (orgSamlConnected && isOrgContext)
      ? getSettingsRoute('orgSamlDisconnect', 'org/custom/{orgID}/saml2/disconnect/?next={next}', { orgID, next: socialNextPage })
      : getSettingsRoute('samlDisconnect', 'saml2/disconnect/?next={next}', { next: socialNextPage });

    return (
      <React.Fragment>
        <div className="setting-item" id="social-auth">
          <h3 className="setting-item-heading">{gettext('Social Login')}</h3>
          <p className="mb-2">{'SAML'}</p>
          {(samlConnected || (orgSamlConnected && isOrgContext)) ?
            <button className="btn btn-outline-primary" onClick={this.confirmDisconnect}>{gettext('Disconnect')}</button> :
            <a href={connectUrl} className="btn btn-outline-primary">{gettext('Connect')}</a>
          }
        </div>
        {this.state.isConfirmDialogOpen && (
          <ModalPortal>
            <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
              <div className="modal-dialog modal-dialog-centered">
                <div className="modal-content">
                  <div className="modal-header">
                    <h5 className="modal-title">{gettext('Disconnect')}</h5>
                    <button type="button" className="close" onClick={this.toggleDialog} aria-label="Close">
                      <span aria-hidden="true">&times;</span>
                    </button>
                  </div>
                  <div className="modal-body">
                    <p>{gettext('Are you sure you want to disconnect?')}</p>
                    <form ref={this.form} className="d-none" method="post" action={disconnectUrl}>
                      <input type="hidden" name="csrfmiddlewaretoken" value={csrfToken} />
                    </form>
                  </div>
                  <div className="modal-footer">
                    <Button color="secondary" onClick={this.toggleDialog}>{gettext('Cancel')}</Button>
                    <Button color="primary" onClick={this.disconnect}>{gettext('Disconnect')}</Button>
                  </div>
                </div>
              </div>
            </div>
          </ModalPortal>
        )}
      </React.Fragment>
    );
  }
}

export default SocialLoginSAML;
