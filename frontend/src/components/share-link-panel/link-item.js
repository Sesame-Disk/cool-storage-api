import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import moment from 'moment';
import copy from 'copy-to-clipboard';
import toaster from '../toast';
import { isPro, gettext } from '../../utils/constants';
import { Utils } from '../../utils/utils';
import CommonOperationConfirmationDialog from '../../components/dialog/common-operation-confirmation-dialog';

const propTypes = {
  item: PropTypes.object.isRequired,
  showLinkDetails: PropTypes.func.isRequired,
  toggleSelectLink: PropTypes.func.isRequired,
  deleteLink: PropTypes.func.isRequired
};

class LinkItem extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      isItemOpVisible: false,
      isDeleteShareLinkDialogOpen: false
    };
  }

  onMouseOver = () => {
    this.setState({
      isItemOpVisible: true
    });
  };

  onMouseOut = () => {
    this.setState({
      isItemOpVisible: false
    });
  };

  cutLink = (link) => {
    let length = link.length;
    return link.slice(0, 9) + '...' + link.slice(length - 5);
  };

  onDeleteIconClicked = (e) => {
    e.preventDefault();
    e.stopPropagation();
    this.toggleDeleteShareLinkDialog();
  };

  toggleDeleteShareLinkDialog = () => {
    this.setState({ isDeleteShareLinkDialogOpen: !this.state.isDeleteShareLinkDialogOpen });
  };

  onCopyIconClicked = (e) => {
    e.preventDefault();
    e.stopPropagation();
    const { item } = this.props;
    copy(item.link);
    toaster.success(gettext('Share link is copied to the clipboard.'));
  };

  clickItem = (e) => {
    this.props.showLinkDetails(this.props.item);
  };

  onCheckboxClicked = (e) => {
    e.stopPropagation();
  };

  toggleSelectLink = (e) => {
    const { item } = this.props;
    this.props.toggleSelectLink(item, e.target.checked);
  };

  deleteLink = () => {
    const { item } = this.props;
    this.props.deleteLink(item.token);
  };

  render() {
    const { isItemOpVisible } = this.state;
    const { item } = this.props;
    const { isSelected = false, permissions, link, expire_date, has_password, is_expired } = item;
    const currentPermission = Utils.getShareLinkPermissionStr(permissions);
    return (
      <Fragment>
        <tr
          onClick={this.clickItem}
          onMouseOver={this.onMouseOver}
          onMouseOut={this.onMouseOut}
          className={`cursor-pointer ${isSelected ? 'tr-highlight' : ''} ${is_expired ? 'share-link-expired' : ''}`}
        >
          <td className="text-center">
            <input
              type="checkbox"
              checked={isSelected}
              className="vam"
              onClick={this.onCheckboxClicked}
              onChange={this.toggleSelectLink}
            />
          </td>
          <td>
            {has_password && (
              <i
                className="fas fa-lock mr-1"
                title={gettext('Password protected')}
                style={{ fontSize: '11px', color: '#f0a040' }}
              ></i>
            )}
            {this.cutLink(link)}
          </td>
          <td>
            {(isPro && permissions) && Utils.getShareLinkPermissionObject(currentPermission).text}
          </td>
          <td>
            {expire_date ? (
              <span style={is_expired ? { color: '#dc3545', fontWeight: 500 } : {}}>
                {is_expired && <i className="fas fa-exclamation-triangle mr-1" title={gettext('Expired')} style={{ fontSize: '11px' }}></i>}
                {moment(expire_date).format('YYYY-MM-DD HH:mm')}
              </span>
            ) : '--'}
          </td>
          <td>
            <button type="button" onClick={this.onCopyIconClicked} className={`sf2-icon-copy action-icon ${isItemOpVisible ? '' : 'invisible'} border-0 bg-transparent p-0`} title={gettext('Copy')} aria-label={gettext('Copy')}>
              <span className="sr-only">{gettext('Copy')}</span>
            </button>
            <button type="button" onClick={this.onDeleteIconClicked} className={`sf2-icon-delete action-icon ${isItemOpVisible ? '' : 'invisible'} border-0 bg-transparent p-0`} title={gettext('Delete')} aria-label={gettext('Delete')}>
              <span className="sr-only">{gettext('Delete')}</span>
            </button>
          </td>
        </tr>
        {this.state.isDeleteShareLinkDialogOpen && (
          <CommonOperationConfirmationDialog
            title={gettext('Delete share link')}
            message={gettext('Are you sure you want to delete the share link?')}
            executeOperation={this.deleteLink}
            confirmBtnText={gettext('Delete')}
            toggleDialog={this.toggleDeleteShareLinkDialog}
          />
        )}
      </Fragment>
    );
  }
}

LinkItem.propTypes = propTypes;

export default LinkItem;
