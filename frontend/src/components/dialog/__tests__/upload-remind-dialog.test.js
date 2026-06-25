import UploadRemindDialog from '../upload-remind-dialog';

// Recursively search a rendered element tree for a node with the given id.
const findById = (node, id) => {
  if (!node || typeof node !== 'object') {
    return null;
  }
  if (node.props && node.props.id === id) {
    return node;
  }
  const children = node.props && node.props.children;
  const arr = Array.isArray(children) ? children : [children];
  for (let i = 0; i < arr.length; i += 1) {
    const found = findById(arr[i], id);
    if (found) {
      return found;
    }
  }
  return null;
};

const baseProps = {
  currentResumableFile: { fileName: 'dup.txt' },
  replaceRepetitionFile: jest.fn(),
  uploadFile: jest.fn(),
  cancelFileUpload: jest.fn(),
};

describe('UploadRemindDialog apply-to-all', () => {
  test('renders the apply-to-all checkbox only when showApplyToAll is set', () => {
    const withFlag = new UploadRemindDialog({ ...baseProps, showApplyToAll: true });
    withFlag.state = { applyToAll: false };
    expect(findById(withFlag.render(), 'upload-remind-apply-to-all')).not.toBeNull();

    const withoutFlag = new UploadRemindDialog({ ...baseProps, showApplyToAll: false });
    withoutFlag.state = { applyToAll: false };
    expect(findById(withoutFlag.render(), 'upload-remind-apply-to-all')).toBeNull();
  });

  test('getApplyToAll only counts when the box is both offered AND checked', () => {
    const offered = new UploadRemindDialog({ ...baseProps, showApplyToAll: true });
    offered.state = { applyToAll: true };
    expect(offered.getApplyToAll()).toBe(true);
    offered.state = { applyToAll: false };
    expect(offered.getApplyToAll()).toBe(false);

    // Checked state can linger but must NOT count when the box was never offered.
    const notOffered = new UploadRemindDialog({ ...baseProps, showApplyToAll: false });
    notOffered.state = { applyToAll: true };
    expect(notOffered.getApplyToAll()).toBe(false);
  });

  test('each action forwards the resolved apply-to-all flag to the parent', () => {
    const dialog = new UploadRemindDialog({ ...baseProps, showApplyToAll: true });
    dialog.state = { applyToAll: true };
    const stop = { nativeEvent: { stopImmediatePropagation: jest.fn() } };

    dialog.replaceRepetitionFile(stop);
    dialog.uploadFile(stop);
    dialog.toggle(stop);

    expect(baseProps.replaceRepetitionFile).toHaveBeenCalledWith(true);
    expect(baseProps.uploadFile).toHaveBeenCalledWith(true);
    expect(baseProps.cancelFileUpload).toHaveBeenCalledWith(true);
  });
});
