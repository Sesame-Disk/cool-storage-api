import { isFileSaving } from './upload-finalization';

// The "active upload" that the dialog highlight and auto-scroll follow must be
// the file actually transferring bytes, not merely the first not-yet-saved file.
// A file in "Saving..." (server-side finalize) keeps isUploading() true while its
// last chunk awaits the server, so it has to be excluded or the scroll would
// stay pinned to it while a later file uploads visibly.
const isTransferringBytes = (file) => {
  return Boolean(file)
    && !file.isSaved
    && !file.error
    && typeof file.isUploading === 'function'
    && file.isUploading()
    && !isFileSaving(file);
};

// Shared by every FileUploader/UploadProgressDialog variant so this selection
// lives in exactly one place (it drifted and re-grew bugs while copy-pasted).
export const findActiveUploadFile = (uploadFileList) => {
  const list = uploadFileList || [];
  return list.find(isTransferringBytes)
    || list.find(file => file && !file.isSaved && !file.error)
    || null;
};

export const getActiveUploadId = (uploadFileList) => {
  const activeFile = findActiveUploadFile(uploadFileList);
  return activeFile ? activeFile.uniqueIdentifier : null;
};
