import React from 'react';
import PropTypes from 'prop-types';
import './toast.css';

class Alert extends React.PureComponent {
  getContainerStyle(intent) {
    switch (intent) {
      case 'success':
        return { borderClass: 'toast-alert-success', iconClass: 'fa fa-check-circle toast-alert-icon-success' };
      case 'warning':
        return { borderClass: 'toast-alert-warning', iconClass: 'fa fa-exclamation-triangle toast-alert-icon-warning' };
      case 'none':
        return { borderClass: 'toast-alert-notify', iconClass: 'fa fa-exclamation-circle toast-alert-icon-notify' };
      case 'notify-in-progress':
        return { borderClass: 'toast-alert-notify', iconClass: 'loading-icon toast-alert-icon-loading' };
      case  'danger':
        return { borderClass: 'toast-alert-danger', iconClass: 'fa fa-exclamation-circle toast-alert-icon-danger' };
      default:
        return { borderClass: 'toast-alert-notify', iconClass: 'fa fa-exclamation-circle toast-alert-icon-notify' };
    }
  }


  render() {
    const toastStyle = this.getContainerStyle(this.props.intent);
    return (
      <div className={`toast-alert ${toastStyle.borderClass}`}>
        <div className="toast-alert-icon">
          <i className={toastStyle.iconClass}/>
        </div>
        <div>
          <p className="toast-alert-title">{this.props.title}</p>
          {this.props.children ? <p className="toast-alert-child">{this.props.children}</p> : null}
        </div>
        <div onClick={this.props.onRemove} className="toast-alert-close">
          <span>&times;</span>
        </div>
      </div>
    );
  }
}

Alert.propTypes = {
  onRemove: PropTypes.func.isRequired,
  children: PropTypes.any,
  title: PropTypes.string.isRequired,
  intent: PropTypes.string.isRequired,
};

export default Alert;
