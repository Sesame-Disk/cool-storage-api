import React from 'react';
import PropTypes from 'prop-types';

// Lightweight PDF viewer that delegates rendering to the browser's native
// PDF plugin via <embed>. Reusable across normal file views, share links and
// office-converted document previews.
function PDFViewer({ src, title, className, style }) {
  if (!src) {
    return null;
  }

  const mergedStyle = { width: '100%', height: '100%', border: 'none', ...style };

  return (
    <embed
      src={src}
      type="application/pdf"
      title={title || 'PDF preview'}
      className={className}
      style={mergedStyle}
    />
  );
}

PDFViewer.propTypes = {
  src: PropTypes.string.isRequired,
  title: PropTypes.string,
  className: PropTypes.string,
  style: PropTypes.object,
};

export default PDFViewer;
