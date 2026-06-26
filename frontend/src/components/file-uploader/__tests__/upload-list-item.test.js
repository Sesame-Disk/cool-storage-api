import UploadListItem from '../upload-list-item';

// Collect all rendered text (strings/numbers) from a React element tree so a test
// can assert on the label a row shows without a full DOM render.
const collectText = (node) => {
  if (node === null || node === undefined || node === false) {
    return '';
  }
  if (typeof node === 'string' || typeof node === 'number') {
    return String(node);
  }
  if (Array.isArray(node)) {
    return node.map(collectText).join('');
  }
  if (typeof node === 'object' && node.props) {
    return collectText(node.props.children);
  }
  return '';
};

const UPLOAD_UPLOADING = 'uploading';
const UPLOAD_ISSAVING = 'isSaving';
const UPLOAD_UPLOADED = 'uploaded';

const blockFile = (overrides = {}) => ({
  uniqueIdentifier: 'b1',
  fileName: 'big.bin',
  newFileName: null,
  size: 10 * 1000, // small-file branch (< 100MB)
  isBlockUpload: true,
  isSaved: false,
  error: null,
  _phase: 'hashing',
  _progress: 0.3,
  _dedupedBytes: 0,
  progress() { return this._progress; },
  isUploading() { return true; },
  ...overrides,
});

const renderRow = (resumableFile, uploadState) => {
  const item = new UploadListItem({
    resumableFile,
    onUploadCancel: () => {},
    onUploadRetry: () => {},
  });
  item.state = { uploadState };
  return collectText(item.render());
};

describe('UploadListItem block-upload phase labels', () => {
  test('shows "Hashing..." while the block entry is hashing', () => {
    const text = renderRow(blockFile({ _phase: 'hashing' }), UPLOAD_UPLOADING);
    expect(text).toContain('Hashing...');
  });

  test('shows "Checking..." while the block entry is checking the server', () => {
    const text = renderRow(blockFile({ _phase: 'checking', _progress: 0.5 }), UPLOAD_UPLOADING);
    expect(text).toContain('Checking...');
  });

  test('shows "Uploading... X%" while the block entry uploads', () => {
    const text = renderRow(blockFile({ _phase: 'uploading', _progress: 0.73 }), UPLOAD_UPLOADING);
    expect(text).toContain('Uploading...');
    expect(text).toContain('73%');
  });

  test('shows "Saving..." while the block entry commits', () => {
    const text = renderRow(blockFile({ _phase: 'saving', _progress: 1 }), UPLOAD_ISSAVING);
    expect(text).toContain('Saving...');
  });

  test('shows the dedup note when bytes were already on the server', () => {
    const text = renderRow(blockFile({ _phase: 'uploading', _progress: 0.8, _dedupedBytes: 40 * 1000 * 1000 }), UPLOAD_UPLOADING);
    expect(text).toContain('already on server');
    expect(text).toContain('40.0 M');
  });

  test('keeps the dedup note on the completed row', () => {
    const text = renderRow(blockFile({ isSaved: true, _phase: 'done', _progress: 1, _dedupedBytes: 40 * 1000 * 1000 }), UPLOAD_UPLOADED);
    expect(text).toContain('Uploaded');
    expect(text).toContain('already on server');
  });

  test('no dedup note when nothing was deduplicated', () => {
    const text = renderRow(blockFile({ _phase: 'uploading', _progress: 0.8, _dedupedBytes: 0 }), UPLOAD_UPLOADING);
    expect(text).not.toContain('already on server');
  });
});
