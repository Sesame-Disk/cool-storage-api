import React from 'react';

function getGettext() {
  return typeof window.gettext === 'function' ? window.gettext : (message) => message;
}

function getSiteRoot() {
  const siteRoot = window.app?.config?.siteRoot || '/';
  return siteRoot.endsWith('/') ? siteRoot : `${siteRoot}/`;
}

function buildPublicLinkPasswordCheckUrl(token) {
  return `${getSiteRoot()}api/v2.1/public-links/${encodeURIComponent(token)}/check-password`;
}

class SharedLinkPasswordDialog extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      password: '',
      error: '',
      isSubmitting: false,
    };
  }

  handleInputChange = (e) => {
    this.setState({ password: e.target.value, error: '' });
  };

  handleSubmit = (e) => {
    e.preventDefault();
    const gettext = getGettext();
    const { password } = this.state;
    const { token } = this.props;

    if (!password) {
      this.setState({ error: gettext('Please enter the password.') });
      return;
    }

    this.setState({ isSubmitting: true, error: '' });

    fetch(buildPublicLinkPasswordCheckUrl(token), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password }),
    })
      .then((res) => {
        if (res.ok) {
          window.location.reload();
        } else {
          this.setState({
            isSubmitting: false,
            error: gettext('Incorrect password'),
          });
        }
      })
      .catch(() => {
        this.setState({
          isSubmitting: false,
          error: gettext('Network error. Please try again.'),
        });
      });
  };

  render() {
    const gettext = getGettext();
    const { password, error, isSubmitting } = this.state;

    return (
      <div style={{
        position: 'fixed',
        top: 0, left: 0, right: 0, bottom: 0,
        backgroundColor: 'rgba(0, 0, 0, 0.5)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 9999,
      }}>
        <div style={{
          background: '#fff',
          borderRadius: '8px',
          padding: '40px',
          width: '400px',
          maxWidth: '90%',
          boxShadow: '0 4px 20px rgba(0, 0, 0, 0.15)',
          textAlign: 'center',
        }}>
          <h4 style={{ marginBottom: '8px' }}>{gettext('Password Protected')}</h4>
          <p style={{ color: '#666', marginBottom: '24px' }}>
            {gettext('This link is protected. Please enter the password to continue.')}
          </p>
          <form onSubmit={this.handleSubmit}>
            <input
              type="password"
              className="form-control"
              value={password}
              onChange={this.handleInputChange}
              placeholder={gettext('Password')}
              autoFocus
              style={{ marginBottom: '12px' }}
            />
            {error && <p style={{ color: '#dc3545', fontSize: '14px', marginBottom: '12px' }}>{error}</p>}
            <button
              type="submit"
              className="btn btn-primary btn-block"
              disabled={isSubmitting}
              style={{ width: '100%' }}
            >
              {isSubmitting ? gettext('Verifying...') : gettext('Submit')}
            </button>
          </form>
        </div>
      </div>
    );
  }

}

export default SharedLinkPasswordDialog;
