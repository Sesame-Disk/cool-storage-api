import React, { Component } from 'react';
import PropTypes from 'prop-types';
import { Button } from 'reactstrap';
import { gettext } from '../../utils/constants';

const propTypes = {
  conflictingItems: PropTypes.array.isRequired,
  operationType: PropTypes.string.isRequired, // 'copy' or 'move'
  onReplace: PropTypes.func.isRequired,
  onKeepBoth: PropTypes.func.isRequired,
  onCancel: PropTypes.func.isRequired,
};

class CopyMoveConflictDialog extends Component {

  render() {
    const { conflictingItems, operationType, onReplace, onKeepBoth, onCancel } = this.props;
    const opName = operationType === 'copy' ? gettext('copy') : gettext('move');
    const itemCount = conflictingItems.length;
    let title;
    if (itemCount === 1) {
      title = gettext('A file or folder with the same name already exists in the destination.');
    } else {
      title = gettext('{count} files or folders with the same name already exist in the destination.')
        .replace('{count}', itemCount);
    }

    return (
      <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
        <div className="modal-dialog modal-dialog-centered">
          <div className="modal-content">
            <div className="modal-header">
              <h5 className="modal-title">{gettext('Name Conflict')}</h5>
              <button type="button" className="close" onClick={onCancel} aria-label="Close"><span aria-hidden="true">&times;</span></button>
            </div>
            <div className="modal-body">
              <p>{title}</p>
              <ul className="list-unstyled mb-0" style={{ maxHeight: '200px', overflowY: 'auto' }}>
                {conflictingItems.map((name, i) => (
                  <li key={i} className="text-break mb-1">
                    <i className="sf2-icon-file mr-1"></i>
                    {name}
                  </li>
                ))}
              </ul>
            </div>
            <div className="modal-footer">
              <Button color="secondary" onClick={onCancel}>{gettext('Cancel')}</Button>
              <Button color="primary" className="mr-2" onClick={onKeepBoth}>{gettext('Keep Both')}</Button>
              <Button color="primary" onClick={onReplace}>{gettext('Replace')}</Button>
            </div>
          </div>
        </div>
      </div>
    );
  }
}

CopyMoveConflictDialog.propTypes = propTypes;

export default CopyMoveConflictDialog;
