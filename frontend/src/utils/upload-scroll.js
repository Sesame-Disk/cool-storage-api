// Scroll `row` into view inside its scroll `container`, nudging only when the
// row is actually clipped. Shared by every UploadProgressDialog variant.
//
// Measure with getBoundingClientRect instead of offsetTop: the active row is a
// <tr> whose offsetParent is the <table>, not the scroll container, so
// row.offsetTop - container.offsetTop mixes coordinate frames and collapses to a
// negative value (forcing scrollTop to 0 and pinning the list at the top). Rects
// are viewport-relative, so the difference plus the current scrollTop gives the
// row's true position within the scrolled content.
export const scrollRowIntoView = (container, row) => {
  if (!container || !row) {
    return;
  }

  const containerRect = container.getBoundingClientRect();
  const rowRect = row.getBoundingClientRect();
  const rowTop = (rowRect.top - containerRect.top) + container.scrollTop;
  const rowBottom = rowTop + rowRect.height;
  const visibleTop = container.scrollTop;
  const visibleBottom = visibleTop + container.clientHeight;

  if (rowTop < visibleTop) {
    container.scrollTop = Math.max(0, rowTop - 8);
  } else if (rowBottom > visibleBottom) {
    container.scrollTop = rowBottom - container.clientHeight + 8;
  }
};
