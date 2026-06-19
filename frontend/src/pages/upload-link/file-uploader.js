// This file is copied from frontend/src/components/file-uploader/file-uploader.js,
// and modified according to the requirements of this page.
import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import Resumablejs from '@seafile/resumablejs';
import MD5 from 'md5';
import { resumableUploadFileBlockSize, resumableSimultaneousUploads, maxUploadFileSize, maxNumberOfFilesForFileupload } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { clearFileUploadRuntimeState, consumeSuppressedUploadErrorToast, getBaselineSimultaneousUploads, getInitialSimultaneousUploads, initializeAdaptiveUploadConcurrency, markUploadConflictAutoRetry, maybeMarkFileFinalizing, maybeStartPendingUploadDuringFinalize, moveUploadToRetryState, noteAdaptiveUploadFailure, noteAdaptiveUploadRetry, resetAdaptiveUploadConcurrency, resetUploadConflictAutoRetry, resolveUploadSuccessResult, restoreUploadConcurrencyIfIdle, shouldAutoRetryUploadConflict, trackUploadResponseStatus, updateAdaptiveUploadConcurrency } from '../../utils/upload-finalization';
import { Utils } from '../../utils/utils';
import { gettext } from '../../utils/constants';
import UploadProgressDialog from './upload-progress-dialog';
import toaster from '../../components/toast';

import '../../css/file-uploader.css';

const propTypes = {
  dragAndDrop: PropTypes.bool.isRequired,
  token: PropTypes.string.isRequired,
  repoID: PropTypes.string.isRequired,
  path: PropTypes.string.isRequired,

  filetypes: PropTypes.array,
  chunkSize: PropTypes.number,
  withCredentials: PropTypes.bool,
  testMethod: PropTypes.string,
  testChunks: PropTypes.number,
  simultaneousUploads: PropTypes.number,
  fileParameterName: PropTypes.string,
  minFileSizeErrorCallback: PropTypes.func,
  fileTypeErrorCallback: PropTypes.func,
  onFileUploadSuccess: PropTypes.func.isRequired,
};

class FileUploader extends React.Component {

  constructor(props) {
    super(props);
    this.state = {
      retryFileList: [],
      uploadFileList: [],
      forbidUploadFileList: [],
      totalProgress: 0,
      isUploadProgressDialogShow: false,
      currentResumableFile: null,
      uploadBitrate: 0,
    };

    this.uploadInput = React.createRef();

    this.notifiedFolders = [];

    this.timestamp = null;
    this.loaded = 0;
    this.bitrateInterval = 500; // Interval in milliseconds to calculate the bitrate
    this.isUploadLinkLoaded = false;
    this.adaptiveUploadCleanup = null;
    this.allowNavigationWithoutPrompt = false;
    this.navigationPromptResetTimer = null;

    window.onbeforeunload = this.onbeforeunload;
  }

  componentDidMount() {
    document.addEventListener('click', this.onDocumentNavigationAttempt, true);
    const configuredSimultaneousUploads = getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads);
    const simultaneousUploads = getInitialSimultaneousUploads(configuredSimultaneousUploads);
    this.resumable = new Resumablejs({
      target: '',
      query: this.setQuery || {},
      fileType: this.props.filetypes,
      maxFiles: maxNumberOfFilesForFileupload || undefined,
      maxFileSize: maxUploadFileSize * 1000 * 1000 || undefined,
      testMethod: this.props.testMethod || 'post',
      testChunks: this.props.testChunks || false,
      headers: this.setHeaders || {},
      withCredentials: this.props.withCredentials || false,
      chunkSize: parseInt(resumableUploadFileBlockSize) * 1024 * 1024 || 1 * 1024 * 1024,
      simultaneousUploads,
      fileParameterName: this.props.fileParameterName,
      generateUniqueIdentifier: this.generateUniqueIdentifier,
      forceChunkSize: true,
      maxChunkRetries: 3,
      minFileSize: 0,
    });

    this.resumable.assignBrowse(this.uploadInput.current, true);
    if (this.props.dragAndDrop) {
      this.resumable.assignDrop(document.getElementById('upload-link-drop-zone'));
    }

    this.bindCallbackHandler();
    this.bindEventHandler();
    this.adaptiveUploadCleanup = initializeAdaptiveUploadConcurrency(this.resumable, configuredSimultaneousUploads);
  }

  componentWillUnmount = () => {
    window.onbeforeunload = null;
    document.removeEventListener('click', this.onDocumentNavigationAttempt, true);
    window.clearTimeout(this.navigationPromptResetTimer);
    this.allowNavigationWithoutPrompt = false;
    if (this.props.dragAndDrop === true) {
      this.resumable.disableDropOnDocument();
    }
    if (typeof this.adaptiveUploadCleanup === 'function') {
      this.adaptiveUploadCleanup();
    }
  };

  hasActiveUploadWork = () => {
    return this.state.isUploadProgressDialogShow
      && this.state.uploadFileList.some(file => file && !file.isSaved && !file.error);
  };

  shouldPromptForNavigation = () => {
    return this.hasActiveUploadWork() && !this.allowNavigationWithoutPrompt;
  };

  confirmNavigationIfUploading = () => {
    if (!this.shouldPromptForNavigation()) {
      return true;
    }

    const confirmed = window.confirm(gettext('A file is being uploaded. Are you sure you want to leave this page?'));
    if (!confirmed) {
      return false;
    }

    this.allowNavigationWithoutPrompt = true;
    window.clearTimeout(this.navigationPromptResetTimer);
    this.navigationPromptResetTimer = window.setTimeout(() => {
      this.allowNavigationWithoutPrompt = false;
      this.navigationPromptResetTimer = null;
    }, 1000);
    return true;
  };

  onDocumentNavigationAttempt = (event) => {
    if (!this.shouldPromptForNavigation() || event.defaultPrevented) {
      return;
    }

    if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
      return;
    }

    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }

    const anchor = target.closest('a[href]');
    if (!anchor) {
      return;
    }

    const href = anchor.getAttribute('href');
    if (!href || href === '#' || /^\s*javascript:/i.test(href)) {
      return;
    }

    if (anchor.hasAttribute('download') || anchor.target === '_blank') {
      return;
    }

    const destination = new URL(anchor.href, window.location.href);
    const current = new URL(window.location.href);
    if (destination.origin === current.origin
      && destination.pathname === current.pathname
      && destination.search === current.search) {
      return;
    }

    if (!this.confirmNavigationIfUploading()) {
      event.preventDefault();
      event.stopPropagation();
    }
  };

  onbeforeunload = () => {
    if (this.shouldPromptForNavigation()) {
      return '';
    }
  };

  bindCallbackHandler = () => {
    let { minFileSizeErrorCallback, fileTypeErrorCallback } = this.props;

    if (this.maxFilesErrorCallback) {
      this.resumable.opts.maxFilesErrorCallback = this.maxFilesErrorCallback;
    }

    if (minFileSizeErrorCallback) {
      this.resumable.opts.minFileSizeErrorCallback = this.props.minFileSizeErrorCallback;
    }

    if (this.maxFileSizeErrorCallback) {
      this.resumable.opts.maxFileSizeErrorCallback = this.maxFileSizeErrorCallback;
    }

    if (fileTypeErrorCallback) {
      this.resumable.opts.fileTypeErrorCallback = this.props.fileTypeErrorCallback;
    }

  };

  bindEventHandler = () => {
    this.resumable.on('chunkingComplete', this.onChunkingComplete.bind(this));
    this.resumable.on('fileAdded', this.onFileAdded.bind(this));
    this.resumable.on('filesAddedComplete', this.filesAddedComplete.bind(this));
    this.resumable.on('fileProgress', this.onFileProgress.bind(this));
    this.resumable.on('fileSuccess', this.onFileUploadSuccess.bind(this));
    this.resumable.on('fileError', this.onFileError.bind(this));
    this.resumable.on('fileRetry', this.onFileRetry.bind(this));
    this.resumable.on('uploadStart', this.onUploadStart.bind(this));
    this.resumable.on('progress', this.onProgress.bind(this));
    this.resumable.on('complete', this.onComplete.bind(this));
    this.resumable.on('error', this.onError.bind(this));
    this.resumable.on('dragstart', this.onDragStart.bind(this));
  };

  maxFilesErrorCallback = (files, errorCount) => {
    let maxFiles = maxNumberOfFilesForFileupload;
    let message = gettext('Please upload no more than {maxFiles} files at a time.');
    message = message.replace('{maxFiles}', maxFiles);
    toaster.danger(message);
  };

  maxFileSizeErrorCallback = (file) => {
    let { forbidUploadFileList } = this.state;
    forbidUploadFileList.push(file);
    this.setState({ forbidUploadFileList: forbidUploadFileList });
  };

  onChunkingComplete = (resumableFile) => {
    let path = this.props.path;
    let fileName = resumableFile.fileName;
    let relativePath = resumableFile.relativePath;
    let isFile = fileName === relativePath;

    resumableFile.formData = {};
    if (isFile) { // upload file
      resumableFile.formData = {
        parent_dir: path,
      };
    } else { // upload folder
      let relative_path = relativePath.slice(0, relativePath.lastIndexOf('/') + 1);
      resumableFile.formData = {
        parent_dir: path,
        relative_path: relative_path
      };
    }

    maybeStartPendingUploadDuringFinalize(this.resumable);
  };

  onFileAdded = (resumableFile, files) => {
    let isFile = resumableFile.fileName === resumableFile.relativePath;
    this.setUploadFileList(this.resumable.files);

    // Fetch the session upload link EXACTLY ONCE and reuse it for every file,
    // including files added after the upload has already started. Re-fetching
    // mints a brand-new upload token and overwrites the shared
    // this.resumable.opts.target; any file still in flight then has its remaining
    // chunks rerouted to a different server-side tracker (the tracker is keyed by
    // token), splitting the upload across two trackers so neither ever reaches
    // contiguity and the file never finalizes. This is the "add the big file
    // first, then add more files" failure: the previously-unguarded single-file
    // branch re-minted a token under the in-flight big file. Files added later
    // are picked up automatically by the already-running upload queue.
    if (this.isUploadLinkLoaded) {
      return;
    }
    this.isUploadLinkLoaded = true;
    seafileAPI.sharedUploadLinkGetFileUploadUrl(this.props.token).then(res => {
      this.resumable.opts.target = res.data.upload_link + '?ret-json=1';
      if (isFile && files.length === 1) {
        this.resumableUpload(resumableFile);
      } else {
        this.resumable.upload();
      }
    }).catch(error => {
      this.isUploadLinkLoaded = false;
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  resumableUpload = (resumableFile) => {
    // Upload links have no auth token, so we can't call getFileUploadedBytes.
    // Resume from offset 0 (start fresh) - same result since the endpoint returns 0 anyway.
    resumableFile.markChunksCompleted(0);
    this.resumable.upload();
  };

  filesAddedComplete = (resumable, files) => {
    let { forbidUploadFileList } = this.state;
    if (forbidUploadFileList.length > 0 && files.length === 0) {
      this.setState({
        isUploadProgressDialogShow: true,
        totalProgress: 100
      });
    }
  };

  setUploadFileList = () => {
    let uploadFileList = this.resumable.files;
    this.setState({
      uploadFileList: uploadFileList,
      isUploadProgressDialogShow: true,
    });
  };

  onFileProgress = (resumableFile) => {
    const simultaneousUploads = getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads);
    let uploadBitrate = this.getBitrate();
    updateAdaptiveUploadConcurrency(this.resumable, resumableFile, uploadBitrate);
    let uploadFileList = this.state.uploadFileList.map(item => {
      if (item.uniqueIdentifier === resumableFile.uniqueIdentifier) {
        if (uploadBitrate) {
          let lastSize = (item.size - (item.size * item.progress())) * 8;
          let time = Math.floor(lastSize / uploadBitrate);
          item.remainingTime = time;
        }
        // All chunk bytes have been transferred but the server hasn't acked
        // yet (it's hashing, storing blocks to S3, and committing metadata).
        // Mark the file as finalizing so the UI shows "Saving..." instead of
        // a stale "Remaining" countdown, and let the next queued file start.
        maybeMarkFileFinalizing(item, this.resumable, simultaneousUploads);
      }
      return item;
    });

    this.setState({
      uploadBitrate: uploadBitrate,
      uploadFileList: uploadFileList
    });
    this.scheduleFinalizeStateRefresh(resumableFile);
  };

  scheduleFinalizeStateRefresh = (resumableFile) => {
    window.setTimeout(() => {
      const simultaneousUploads = getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads);
      let didMarkFinalizing = false;
      let uploadFileList = this.state.uploadFileList.map(item => {
        if (item.uniqueIdentifier === resumableFile.uniqueIdentifier) {
          didMarkFinalizing = maybeMarkFileFinalizing(item, this.resumable, simultaneousUploads) || didMarkFinalizing;
        }
        return item;
      });
      if (didMarkFinalizing) {
        this.setState({ uploadFileList });
      }
    }, 0);
  };

  restoreConcurrencyIfIdle = () => {
    restoreUploadConcurrencyIfIdle(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
  };

  getBitrate = () => {
    let loaded = 0;
    let uploadBitrate = 0;
    let now = new Date().getTime();

    this.resumable.files.forEach(file => {
      loaded += file.progress() * file.size;
    });

    if (this.timestamp) {
      let timeDiff = (now - this.timestamp);
      if (timeDiff < this.bitrateInterval) {
        return this.state.uploadBitrate;
      }

      // 1. Cancel will produce loaded greater than this.loaded
      // 2. reset can make this.loaded to be 0
      if (loaded < this.loaded || this.loaded === 0) {
        this.loaded = loaded; //
      }

      uploadBitrate = (loaded - this.loaded) * (1000 / timeDiff) * 8;
    }

    this.timestamp = now;
    this.loaded = loaded;

    return uploadBitrate;
  };

  // start uploading
  onUploadStart = () => {
    /*
    const message = gettext('File upload started');
    toaster.notify(message);
    */
  };

  onProgress = () => {
    let progress = Math.round(this.resumable.progress() * 100);
    this.setState({ totalProgress: progress });
  };

  markUploadSaved = (resumableFile, newFileName) => {
    let uploadFileList = this.state.uploadFileList.map(item => {
      if (item.uniqueIdentifier === resumableFile.uniqueIdentifier) {
        clearFileUploadRuntimeState(item);
        item.newFileName = newFileName;
        item.isSaved = true;
      }
      return item;
    });
    this.setState({ uploadFileList: uploadFileList });
    this.restoreConcurrencyIfIdle();
  };

  markUploadUnconfirmed = (resumableFile, message) => {
    // The finalize response never reached us (e.g. it was lost on a retried
    // request and the server returned a bare ack). We cannot confirm the file
    // landed, so surface it as retryable instead of silently reporting success
    // — a false "Uploaded" leaves big files missing from the listing.
    // eslint-disable-next-line no-console
    console.error('Upload finalize metadata missing for', resumableFile.fileName, 'message:', message);
    const error = gettext('Upload could not be confirmed. Please retry.');
    const { retryFileList, uploadFileList } = moveUploadToRetryState(this.state.uploadFileList, this.state.retryFileList, resumableFile, error);
    this.setState({ retryFileList: retryFileList, uploadFileList: uploadFileList });
    this.restoreConcurrencyIfIdle();
  };

  onFileUploadSuccess = (resumableFile, message) => {
    let formData = resumableFile.formData;
    let currentTime = new Date().getTime() / 1000;

    // resumable.js hands fileSuccess the body of whichever chunk's XHR finished
    // last. With more than one chunk of this file in flight (simultaneous
    // uploads, or the temporary finalize slot) that body can be an intermediate
    // ack ({"success":true}) instead of the finalize response. Resolve against
    // the metadata captured from whichever chunk actually carried it so we never
    // dereference an undefined entry — which used to throw inside fileSuccess,
    // stall the whole upload queue, and freeze files on "Saving..." forever.
    let resolved = resolveUploadSuccessResult(resumableFile, message, formData.replace);
    if (!resolved) {
      this.markUploadUnconfirmed(resumableFile, message);
      return;
    }

    if (formData.relative_path) { // upload folder
      let entry = resolved.entry;
      let relative_path = formData.relative_path;
      let dir_name = relative_path.slice(0, relative_path.indexOf('/'));
      let dirent = {
        id: entry.id,
        name: dir_name,
        type: 'dir',
        mtime: currentTime,
      };

      // update folders cache
      let isExist = this.notifiedFolders.some(item => { return item.name === dirent.name; });
      if (!isExist) {
        this.notifiedFolders.push(dirent);
        this.props.onFileUploadSuccess(dirent);
      }

      this.markUploadSaved(resumableFile, relative_path + entry.name);
      return;
    }

    if (formData.replace) { // upload file -- replace exist file
      let fileName = resumableFile.fileName;
      let dirent = {
        id: resolved.id,
        name: fileName,
        type: 'file',
        mtime: currentTime
      };
      this.props.onFileUploadSuccess(dirent); // this contance: just one file

      this.markUploadSaved(resumableFile, fileName);
      return;
    }

    // upload file -- add files
    let entry = resolved.entry;
    let dirent = {
      id: entry.id,
      type: 'file',
      name: entry.name,
      size: entry.size,
      mtime: currentTime,
    };
    this.props.onFileUploadSuccess(dirent);

    this.markUploadSaved(resumableFile, entry.name);
  };

  onFileError = (resumableFile, message) => {
    if (shouldAutoRetryUploadConflict(resumableFile, message)) {
      markUploadConflictAutoRetry(resumableFile);
      this.retryUploadWithFreshLink(resumableFile, { resetAutoRetry: false });
      return;
    }
    noteAdaptiveUploadFailure(this.resumable, resumableFile, message);

    let error = '';
    if (!message) {
      error = gettext('Network error');
    } else {
      // eg: '{"error": "Internal error" \n }'
      let errorMessage = message.replace(/\n/g, '');
      errorMessage = JSON.parse(errorMessage);
      error = errorMessage.error;
      if (error === 'File locked by others.') {
        error = gettext('File is locked by others.');
      }
      if (error === 'Internal error.') {
        error = gettext('Internal Server Error');
      }
    }

    const { retryFileList, uploadFileList } = moveUploadToRetryState(this.state.uploadFileList, this.state.retryFileList, resumableFile, error);

    this.loaded = 0;  // reset loaded data;
    this.setState({
      retryFileList: retryFileList,
      uploadFileList: uploadFileList
    });
    this.restoreConcurrencyIfIdle();
  };

  retryUploadWithFreshLink = (resumableFile, options = {}) => {
    const { resetAutoRetry = true } = options;

    // Reuse the session's existing upload link — do NOT mint a fresh one.
    // sharedUploadLinkGetFileUploadUrl returns a brand-new upload TOKEN on each
    // call, and assigning it to the shared this.resumable.opts.target reroutes
    // EVERY other in-flight file onto a different server-side tracker mid-upload.
    // The server keys its chunk tracker by token, so a large concurrent upload
    // gets split across two trackers (token A then token B): neither ever reaches
    // contiguity, neither finalizes, and the file silently never lands — which
    // surfaces on the client as "Upload could not be confirmed". This happens in
    // practice when one file hits a 409 ("library modified concurrently") and its
    // auto-retry swaps the token out from under a big file still uploading.
    // The session token is multi-use and valid for the whole session (it already
    // serves every chunk of every file), so retrying on it is correct and has no
    // cross-file side effects.
    let retryFileList = this.state.retryFileList.filter(item => {
      return item.uniqueIdentifier !== resumableFile.uniqueIdentifier;
    });
    let uploadFileList = this.state.uploadFileList.map(item => {
      if (item.uniqueIdentifier === resumableFile.uniqueIdentifier) {
        clearFileUploadRuntimeState(item, { resetRemainingTime: true });
        item.error = null;
        if (resetAutoRetry) {
          resetUploadConflictAutoRetry(item);
        }
        this.retryUploadFile(item);
      }
      return item;
    });

    this.setState({
      retryFileList: retryFileList,
      uploadFileList: uploadFileList
    });
    this.restoreConcurrencyIfIdle();
  };

  onComplete = () => {
    /*
    if (!this.error) {
      const message = gettext('All files uploaded');
      toaster.success(message);
    }
    */
    this.error = false; // reset it

    this.notifiedFolders = [];
    // reset upload link loaded
    this.isUploadLinkLoaded = false;
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
  };

  onError = (message, file) => {
    if (consumeSuppressedUploadErrorToast(file)) {
      return;
    }

    let msg = gettext('Error');
    if (file && file.fileName) {
      msg = gettext('Failed to upload {file_name}.')
        .replace('{file_name}', file.fileName);
    }
    toaster.danger(msg, { 'id': 'file-error-msg' });
    this.error = true;

    // A file-level error can fan out to the global error event before every
    // other chunk/file in the queue has stopped. Keep reusing the session link
    // until the queue is actually idle so a later file add cannot mint a new
    // token and split an in-flight upload across trackers.
    if (!this.resumable || !this.resumable.isUploading()) {
      this.isUploadLinkLoaded = false;
    }
    // After the error, the user can switch windows
  };

  setHeaders = (resumableFile, resumable) => {
    trackUploadResponseStatus(resumableFile, resumable);

    let offset = resumable.offset;
    let chunkSize = resumable.getOpt('chunkSize');
    let fileSize = resumableFile.size === 0 ? 1 : resumableFile.size;
    let startByte = offset !== 0 ? offset * chunkSize : 0;
    let endByte = Math.min(fileSize, (offset + 1) * chunkSize) - 1;

    if (fileSize - resumable.endByte < chunkSize && !resumable.getOpt('forceChunkSize')) {
      endByte = fileSize;
    }

    let headers = {
      'Accept': 'application/json; text/javascript, */*; q=0.01',
      'Content-Disposition': 'attachment; filename="' + encodeURI(resumableFile.fileName) + '"',
      'Content-Range': 'bytes ' + startByte + '-' + endByte + '/' + fileSize,
    };

    return headers;
  };

  setQuery = (resumableFile) => {
    let formData = resumableFile.formData;
    return formData;
  };

  generateUniqueIdentifier = (file) => {
    let relativePath = file.webkitRelativePath || file.relativePath || file.fileName || file.name;
    return MD5(relativePath + new Date()) + relativePath;
  };

  onClick = (e) => {
    e.nativeEvent.stopImmediatePropagation();
    e.stopPropagation();
  };

  onFileUpload = () => {
    this.uploadInput.current.removeAttribute('webkitdirectory');
    this.uploadInput.current.click();
  };

  onFolderUpload = () => {
    this.uploadInput.current.setAttribute('webkitdirectory', 'webkitdirectory');
    this.uploadInput.current.click();
  };

  onDragStart = () => {
    this.uploadInput.current.setAttribute('webkitdirectory', 'webkitdirectory');
  };

  onCloseUploadDialog = () => {
    this.loaded = 0;
    this.resumable.files = [];
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
    this.restoreConcurrencyIfIdle();
    // reset upload link loaded
    this.isUploadLinkLoaded = false;
    this.setState({ isUploadProgressDialogShow: false, uploadFileList: [], forbidUploadFileList: [] });
  };

  onUploadCancel = (uploadingItem) => {

    let uploadFileList = this.state.uploadFileList.filter(item => {
      if (item.uniqueIdentifier === uploadingItem.uniqueIdentifier) {
        clearFileUploadRuntimeState(item, { resetRemainingTime: true });
        item.cancel(); // execute cancel function will delete the file at the same time
        return false;
      }
      return true;
    });

    if (!this.resumable.isUploading()) {
      this.setState({
        totalProgress: 100
      });
      this.loaded = 0;
    }

    this.setState({ uploadFileList: uploadFileList });
    this.restoreConcurrencyIfIdle();
  };

  onCancelAllUploading = () => {
    let uploadFileList = this.state.uploadFileList.filter(item => {
      if (Math.round(item.progress() !== 1)) {
        clearFileUploadRuntimeState(item, { resetRemainingTime: true });
        item.cancel();
        return false;
      }
      return true;
    });

    this.loaded = 0;

    this.setState({
      totalProgress: 100,
      uploadFileList: uploadFileList
    });
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
    this.restoreConcurrencyIfIdle();
    // reset upload link loaded
    this.isUploadLinkLoaded = false;
  };

  onUploadRetry = (resumableFile) => {
    this.retryUploadWithFreshLink(resumableFile);
  };

  onUploadRetryAll = () => {
    this.state.retryFileList.forEach(item => {
      clearFileUploadRuntimeState(item, { resetRemainingTime: true });
      resetUploadConflictAutoRetry(item);
      item.error = false;
      this.retryUploadFile(item);
    });

    let uploadFileList = this.state.uploadFileList.slice(0);
    this.setState({
      retryFileList: [],
      uploadFileList: uploadFileList
    });
    this.restoreConcurrencyIfIdle();
  };

  onFileRetry = () => {
    noteAdaptiveUploadRetry(this.resumable);
  };

  retryUploadFile = (resumableFile) => {
    resumableFile.bootstrap();
    var firedRetry = false;
    resumableFile.resumableObj.on('chunkingComplete', () => {
      if (!firedRetry) {
        // Upload links have no auth token - start fresh from offset 0.
        resumableFile.markChunksCompleted(0);
        resumableFile.resumableObj.upload();
      }
      firedRetry = true;
    });

  };

  replaceRepetitionFile = () => {
    let { repoID, path } = this.props;
    seafileAPI.getUpdateLink(repoID, path).then(res => {
      let resumableFile = this.resumable.files[this.resumable.files.length - 1];
      // Scope the update (replace) endpoint to THIS file only via per-file opts.
      // resumable.getOpt resolves file.opts before the shared resumable.opts, so
      // setting it here avoids overwriting the global target and rerouting any
      // OTHER in-flight upload onto the update endpoint mid-flight (which would
      // split it across two server-side trackers; see onFileAdded).
      resumableFile.opts = resumableFile.opts || {};
      resumableFile.opts.target = res.data;
      resumableFile.formData['replace'] = 1;
      resumableFile.formData['target_file'] = resumableFile.formData.parent_dir + resumableFile.fileName;
      this.setUploadFileList(this.resumable.files);
      this.resumable.upload();
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  cancelFileUpload = () => {
    this.resumable.files.pop(); //delete latest file；
  };

  render() {
    return (
      <Fragment>
        <div className="file-uploader-container">
          <div className="file-uploader">
            <input className="upload-input" type="file" ref={this.uploadInput} onClick={this.onClick} aria-label={gettext('Upload')} />
          </div>
        </div>
        <div className="upload-link-actions text-center">
          <button type="button" className="btn btn-primary" onClick={this.onFileUpload}>{gettext('Upload Files')}</button>
          <button type="button" className="btn btn-primary" onClick={this.onFolderUpload}>{gettext('Upload Folder')}</button>
          <button
            type="button"
            className="btn btn-outline-primary"
            onClick={this.onCancelAllUploading}
            disabled={!this.resumable || !this.resumable.isUploading()}
          >
            {gettext('Cancel All')}
          </button>
        </div>
        {this.state.isUploadProgressDialogShow &&
          <UploadProgressDialog
            retryFileList={this.state.retryFileList}
            uploadFileList={this.state.uploadFileList}
            forbidUploadFileList={this.state.forbidUploadFileList}
            totalProgress={this.state.totalProgress}
            uploadBitrate={this.state.uploadBitrate}
            onCloseUploadDialog={this.onCloseUploadDialog}
            onCancelAllUploading={this.onCancelAllUploading}
            onUploadCancel={this.onUploadCancel}
            onUploadRetry={this.onUploadRetry}
            onUploadRetryAll={this.onUploadRetryAll}
            isUploading={this.resumable ? this.resumable.isUploading() : false}
          />
        }
      </Fragment>
    );
  }
}

FileUploader.propTypes = propTypes;

export default FileUploader;
