import React from 'react';
import PropTypes from 'prop-types';
import { gettext } from '../../utils/constants';
import { Button } from 'reactstrap';

const propTypes = {
  currentNode: PropTypes.object.isRequired,
  toggleCancel: PropTypes.func.isRequired,
  handleSubmit: PropTypes.func.isRequired,
};

class Delete extends React.Component {

  toggle = () => {
    this.props.toggleCancel();
  };

  render() {
    let currentNode = this.props.currentNode;
    let name = currentNode.object.name;
    let title = gettext('Delete File');
    if (currentNode.object.isDir()) {
      title = gettext('Delete Folder');
    }
    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{title}</h5>
              <button type="button" className="close" onClick={this.toggle} aria-label="Close"><span aria-hidden="true">&times;</span></button>
            </div>
            <div className="modal-body">
              <p>{gettext('Are you sure to delete')}{' '}<b>{name}</b> ?</p>
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={this.toggle}>{gettext('Cancel')}</Button>
              <Button color="primary" onClick={this.props.handleSubmit}>{gettext('Delete')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

Delete.propTypes = propTypes;

export default Delete;
