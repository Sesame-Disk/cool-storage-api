import React, { Fragment } from 'react';
import PropTypes from 'prop-types';
import Resumablejs from '@seafile/resumablejs';
import MD5 from 'md5';
import { resumableUploadFileBlockSize, resumableSimultaneousUploads, maxUploadFileSize, maxNumberOfFilesForFileupload } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { aggregateBlockUploadBitrate, clearFileUploadRuntimeState, getBaselineSimultaneousUploads, getInitialSimultaneousUploads, initializeAdaptiveUploadConcurrency, markUploadConflictAutoRetry, maybeMarkFileFinalizing, maybeStartPendingUploadDuringFinalize, moveUploadToRetryState, noteAdaptiveUploadFailure, noteAdaptiveUploadRetry, resetAdaptiveUploadConcurrency, resetBlockUploadBitrate, resetUploadConflictAutoRetry, resolveUploadSuccessResult, restoreUploadConcurrencyIfIdle, sampleBlockUploadBitrate, shouldAutoRetryUploadConflict, trackUploadResponseStatus, updateAdaptiveUploadConcurrency } from '../../utils/upload-finalization';
import { Utils } from '../../utils/utils';
import { gettext } from '../../utils/constants';
import UploadNavigationGuard from '../../utils/upload-navigation-guard';
import { createBlockLimiter } from '../../utils/block-upload-limiter';
import UploadProgressDialog from './upload-progress-dialog';
import UploadRemindDialog from '../dialog/upload-remind-dialog';
import toaster from '../toast';
import { isAbortError, shouldUseBlockUpload, uploadFileViaBlocks } from './block-upload-orchestrator';
import '../../css/file-uploader.css';

const propTypes = {
  repoID: PropTypes.string.isRequired,
  repoEncrypted: PropTypes.bool,
  direntList: PropTypes.array.isRequired,
  filetypes: PropTypes.array,
  chunkSize: PropTypes.number,
  withCredentials: PropTypes.bool,
  testMethod: PropTypes.string,
  testChunks: PropTypes.number,
  simultaneousUploads: PropTypes.number,
  fileParameterName: PropTypes.string,
  minFileSizeErrorCallback: PropTypes.func,
  fileTypeErrorCallback: PropTypes.func,
  dragAndDrop: PropTypes.bool.isRequired,
  path: PropTypes.string.isRequired,
  onFileUploadSuccess: PropTypes.func.isRequired,
  isCustomPermission: PropTypes.bool,
};

class FileUploader extends React.Component {

  static defaultProps = {
    isCustomPermission: false
  };

  constructor(props) {
    super(props);
    this.state = {
      retryFileList: [],
      uploadFileList: [],
      forbidUploadFileList: [],
      totalProgress: 0,
      isUploadProgressDialogShow: false,
      isUploadRemindDialogShow: false,
      currentResumableFile: null,
      uploadBitrate: 0,
    };

    this.uploadInput = React.createRef();

    this.notifiedFolders = [];

    this.timestamp = null;
    this.loaded = 0;
    this.legacyUploadBitrate = 0;
    this.bitrateInterval = 500; // Interval in milliseconds to calculate the bitrate
    window.onbeforeunload = this.onbeforeunload;
    this.isUploadLinkLoaded = false;
    this.adaptiveUploadCleanup = null;
    this.navigationGuard = new UploadNavigationGuard(this.hasActiveUploadWork);
  }

  componentDidMount() {
    this.navigationGuard.attach();
    const configuredSimultaneousUploads = getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads);
    const simultaneousUploads = getInitialSimultaneousUploads(configuredSimultaneousUploads);
    // Single GLOBAL block-upload limiter shared by every block (CAS) upload, so the
    // total blocks on the wire across all files never exceed the configured ceiling
    // (`simultaneous_uploads`). Adaptive: it starts at 1 and ramps up to that ceiling
    // while the link stays healthy (fed by noteBitrate/noteFailure).
    this.blockLimiter = createBlockLimiter({ maxConcurrency: configuredSimultaneousUploads });
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

    //Enable or Disable DragAnd Drop
    if (this.props.dragAndDrop === true) {
      this.resumable.enableDropOnDocument();
    }

    this.bindCallbackHandler();
    this.bindEventHandler();
    this.adaptiveUploadCleanup = initializeAdaptiveUploadConcurrency(this.resumable, configuredSimultaneousUploads);
  }

  componentWillUnmount = () => {
    window.onbeforeunload = null;
    this.navigationGuard.detach();
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

  hasUploadingEntries = (uploadFileList = this.state.uploadFileList) => {
    return uploadFileList.some(file => (
      file
      && !file.isSaved
      && !file.error
      && typeof file.isUploading === 'function'
      && file.isUploading()
    ));
  };

  hasActiveLegacyUploads = (uploadFileList = this.state.uploadFileList) => {
    return uploadFileList.some(file => (
      file
      && !file.isBlockUpload
      && !file.isSaved
      && !file.error
      && typeof file.isUploading === 'function'
      && file.isUploading()
    ));
  };

  // calculateUploadBitrate combines the isolated legacy resumable bitrate with the
  // summed block-upload bitrate. The legacy figure is held in this.legacyUploadBitrate
  // (computed in getBitrate); it is zeroed when no legacy upload is active so a stale
  // reading does not linger after the resumable queue drains while block uploads run.
  calculateUploadBitrate = (uploadFileList = this.state.uploadFileList) => {
    const hasActiveLegacyUploads = this.hasActiveLegacyUploads(uploadFileList);
    if (!hasActiveLegacyUploads) {
      this.legacyUploadBitrate = 0;
    }
    const legacyUploadBitrate = hasActiveLegacyUploads ? this.legacyUploadBitrate : 0;
    return legacyUploadBitrate + aggregateBlockUploadBitrate(uploadFileList);
  };

  calculateTotalProgress = (uploadFileList = this.state.uploadFileList) => {
    // Byte-weighted so a 10 GB file dominates the bar over a 1 KB file (an equal
    // per-file average badly misrepresents overall progress when sizes differ).
    // Falls back to an equal average only when no file sizes are known.
    let weightedDone = 0;
    let totalBytes = 0;
    let fractionSum = 0;
    let count = 0;
    uploadFileList.forEach(item => {
      if (item && typeof item.progress === 'function') {
        const fraction = item.progress();
        const size = typeof item.size === 'number' && item.size > 0 ? item.size : 0;
        weightedDone += fraction * size;
        totalBytes += size;
        fractionSum += fraction;
        count += 1;
      }
    });
    if (totalBytes > 0) {
      return Math.round((weightedDone / totalBytes) * 100);
    }
    return count ? Math.round((fractionSum / count) * 100) : 0;
  };

  isUploading = () => {
    return Boolean(this.resumable && this.resumable.isUploading && this.resumable.isUploading())
      || this.hasUploadingEntries();
  };

  cancelActiveUploads = (uploadFileList = this.state.uploadFileList) => {
    uploadFileList.forEach(item => {
      if (item && !item.isSaved && !item.error && typeof item.cancel === 'function') {
        item.cancel();
      }
    });
  };

  prepareBlockUploadRetry = (entry) => {
    clearFileUploadRuntimeState(entry, { resetRemainingTime: true });
    resetUploadConflictAutoRetry(entry);
    entry.error = null;
    entry.isSaved = false;
    entry._progress = 0;
    entry._uploading = false;
    entry._cancelled = false;
    entry._abortController = null;
    entry._phase = 'hashing';
  };

  // Thin delegators onto the shared guard; kept as instance methods so callers
  // (and tests) keep a stable component API.
  confirmNavigationIfUploading = () => this.navigationGuard.confirmIfUploading();

  onDocumentNavigationAttempt = (event) => this.navigationGuard.onDocumentClick(event);

  onbeforeunload = () => this.navigationGuard.onbeforeunload();

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
    this.resumable.on('progress', this.onProgress.bind(this));
    this.resumable.on('complete', this.onComplete.bind(this));
    this.resumable.on('pause', this.onPause.bind(this));
    this.resumable.on('fileRetry', this.onFileRetry.bind(this));
    this.resumable.on('fileError', this.onFileError.bind(this));
    this.resumable.on('error', this.onError.bind(this));
    this.resumable.on('beforeCancel', this.onBeforeCancel.bind(this));
    this.resumable.on('cancel', this.onCancel.bind(this));
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

    //get parent_dir relative_path
    let path = this.props.path === '/' ? '/' : this.props.path + '/';
    let fileName = resumableFile.fileName;
    let relativePath = resumableFile.relativePath;
    let isFile = fileName === relativePath;

    //update formdata
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

  // maybeBlockUpload diverts an eligible file to the content-addressed flow.
  // Returns true if it took ownership of the file (caller must stop).
  maybeBlockUpload = (resumableFile) => {
    const file = resumableFile.file;
    // Phase 1: single files only. Folder uploads carry a relativePath and need
    // directory creation/relative_path plumbing the block flow does not yet do;
    // routing them here would flatten the folder structure. Let them fall through
    // to the resumable.js path.
    if (resumableFile.fileName !== resumableFile.relativePath) {
      return false;
    }
    if (!file || !shouldUseBlockUpload(file, { encrypted: this.props.repoEncrypted })) {
      return false;
    }
    // Take the file out of resumable.js — we drive it ourselves.
    this.resumable.removeFile(resumableFile);

    const entry = this.createBlockUploadEntry(resumableFile);
    this.setState(prev => ({
      uploadFileList: [...prev.uploadFileList, entry],
      isUploadProgressDialogShow: true,
    }));
    this.runBlockUpload(entry, file);
    return true;
  };

  // createBlockUploadEntry builds a resumable-file-shaped adapter so the existing
  // upload progress dialog renders a block-flow upload like any other.
  createBlockUploadEntry = (resumableFile) => {
    const file = resumableFile.file;
    const entry = {
      uniqueIdentifier: resumableFile.uniqueIdentifier,
      fileName: resumableFile.fileName,
      relativePath: resumableFile.relativePath,
      size: file.size,
      newFileName: null,
      isSaved: false,
      error: null,
      remainingTime: -1,
      formData: {},
      file,
      isBlockUpload: true,
      // Explicit flow phase, the single source of truth for how this entry
      // renders: 'hashing' | 'uploading' | 'saving' | 'done' | 'error'. It is
      // driven by the orchestrator's onPhase callback, NOT inferred from
      // resumable.js chunk/remainingTime state (which a block entry does not have).
      _phase: 'hashing',
      _abortController: null,
      _cancelled: false,
      _progress: 0,
      _uploading: true,
    };
    entry.progress = () => entry._progress;
    entry.isUploading = () => entry._uploading;
    entry.cancel = () => {
      entry._cancelled = true;
      entry._uploading = false;
      if (entry._abortController) {
        entry._abortController.abort();
      }
    };
    return entry;
  };

  updateBlockUploadProgress = (entry, fraction) => {
    entry._progress = Math.max(0, Math.min(1, fraction));
    // Deliberately do NOT touch remainingTime here. Parking it at 0 used to make
    // isFileSaving() true for the whole upload, rendering the row as "Saving..."
    // at 3% and hiding the per-row Cancel button. The block flow has no ETA, so
    // the entry stays at the "Preparing"-style sentinel until phase 'saving'.
    this.setState(prev => {
      const uploadFileList = prev.uploadFileList.map(item => (
        item.uniqueIdentifier === entry.uniqueIdentifier ? entry : item
      ));
      return {
        uploadFileList,
        totalProgress: this.calculateTotalProgress(uploadFileList),
        uploadBitrate: this.calculateUploadBitrate(uploadFileList),
      };
    });
  };

  // updateBlockUploadTransferredBytes accumulates real bytes moved over the wire
  // (from the orchestrator's onTransferProgress) and samples a bits/s figure on the
  // entry, so the dialog header shows an actual block-upload speed instead of the
  // legacy 0.00 B/s (block entries are not in this.resumable.files).
  updateBlockUploadTransferredBytes = (entry, deltaBytes) => {
    if (!deltaBytes || deltaBytes <= 0) {
      return;
    }
    entry._uploadedNetworkBytes = (Number(entry._uploadedNetworkBytes) || 0) + deltaBytes;
    // sampleBlockUploadBitrate is throttled to one fresh reading per
    // BLOCK_BITRATE_SAMPLE_MS and only advances entry._bitrateTs when it actually
    // produced a new sample. Feed the adaptive limiter ONLY on a fresh sample — not on
    // every progress tick — otherwise a burst of rapid progress events would count as
    // many "healthy samples" and ramp the shared ceiling up almost instantly, defeating
    // the gradual 1→max climb. The first (seed) call has no prior timestamp, so it is
    // not a real sample either. Block-only signal (not the combined legacy+block figure).
    const bitrateTsBefore = entry._bitrateTs;
    sampleBlockUploadBitrate(entry, entry._uploadedNetworkBytes);
    const freshBitrateSample = typeof bitrateTsBefore === 'number' && entry._bitrateTs !== bitrateTsBefore;
    if (freshBitrateSample && this.blockLimiter) {
      // Entries are mutated in place, so the current list already reflects the new bitrate.
      this.blockLimiter.noteBitrate(aggregateBlockUploadBitrate(this.state.uploadFileList));
    }
    this.setState(prev => {
      const uploadFileList = prev.uploadFileList.map(item => (
        item.uniqueIdentifier === entry.uniqueIdentifier ? entry : item
      ));
      return {
        uploadFileList,
        uploadBitrate: this.calculateUploadBitrate(uploadFileList),
      };
    });
  };

  setBlockUploadPhase = (entry, phase) => {
    if (entry._phase === phase) {
      return;
    }
    if (phase === 'uploading') {
      resetBlockUploadBitrate(entry);
    }
    entry._phase = phase;
    this.setState(prev => {
      const uploadFileList = prev.uploadFileList.map(item => (
        item.uniqueIdentifier === entry.uniqueIdentifier ? entry : item
      ));
      return {
        uploadFileList,
        uploadBitrate: this.calculateUploadBitrate(uploadFileList),
      };
    });
  };

  runBlockUpload = (entry, file) => {
    const { repoID, path } = this.props;
    const abortController = typeof AbortController === 'function' ? new AbortController() : null;
    entry._abortController = abortController;
    entry._cancelled = false;
    entry._uploading = true;
    entry.error = null;
    entry.isSaved = false;
    entry.remainingTime = -1;
    entry._phase = 'hashing';
    resetBlockUploadBitrate(entry);

    // Hashing is the first half of the bar, uploading the second half. onPhase
    // drives the explicit entry._phase so the row renders Uploading…/Saving…
    // correctly; onTransferProgress feeds real wire bytes for the speed readout.
    uploadFileViaBlocks(file, {
      repoID,
      parentDir: path,
      filename: file.name,
      replace: false,
      // Shared global ceiling: every block upload competes for the same slots.
      limiter: this.blockLimiter,
      signal: abortController ? abortController.signal : undefined,
      onPhase: (phase) => this.setBlockUploadPhase(entry, phase),
      onHashProgress: (hashed, total) => this.updateBlockUploadProgress(entry, (total ? hashed / total : 0) * 0.5),
      onUploadProgress: (done, total) => this.updateBlockUploadProgress(entry, 0.5 + (total ? done / total : 1) * 0.5),
      onTransferProgress: (deltaBytes) => this.updateBlockUploadTransferredBytes(entry, deltaBytes),
    }).then(result => {
      entry._abortController = null;
      entry._progress = 1;
      entry._uploading = false;
      entry._phase = 'done';
      const r = Array.isArray(result) ? result[0] : result;
      const name = (r && r.name) || file.name;
      const dirent = { id: (r && r.id) || '', type: 'file', name, size: file.size, mtime: new Date().getTime() / 1000 };
      this.props.onFileUploadSuccess(dirent);
      this.markUploadSaved(entry, name);
    }).catch(error => {
      entry._abortController = null;
      entry._uploading = false;
      if (entry._cancelled || isAbortError(error)) {
        entry._phase = 'hashing';
        this.setState({
          totalProgress: this.calculateTotalProgress(),
          uploadBitrate: this.calculateUploadBitrate(),
        });
        this.restoreConcurrencyIfIdle();
        return;
      }
      entry._phase = 'error';
      const message = this.getAxiosErrorMessage(error) || gettext('Network error');
      const { retryFileList, uploadFileList } = moveUploadToRetryState(this.state.uploadFileList, this.state.retryFileList, entry, message);
      this.setState({
        retryFileList,
        uploadFileList,
        totalProgress: this.calculateTotalProgress(uploadFileList),
        uploadBitrate: this.calculateUploadBitrate(uploadFileList),
      });
      this.restoreConcurrencyIfIdle();
    });
  };

  onFileAdded = (resumableFile, files) => {
    // Web content-addressed (block) upload flow, behind enableBlockUpload. Large
    // non-encrypted files are diverted out of resumable.js and uploaded via
    // chunk+hash+check+commit (resume/dedup). Anything ineligible (flag off,
    // small, encrypted, unsupported browser) falls through to resumable.js.
    if (this.maybeBlockUpload(resumableFile)) {
      return;
    }

    const { isCustomPermission } = this.props;
    let isFile = resumableFile.fileName === resumableFile.relativePath;
    // uploading is file and only upload one file
    // A lone file whose name already exists offers the replace dialog; the
    // replace flow manages its own upload target, so bail out here.
    if (isFile && files.length === 1 && !isCustomPermission) {
      let direntList = this.props.direntList;
      for (let i = 0; i < direntList.length; i++) {
        if (direntList[i].type === 'file' && direntList[i].name === resumableFile.fileName) {
          this.setState({
            isUploadRemindDialogShow: true,
            currentResumableFile: resumableFile,
          });
          return;
        }
      }
    }

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
    let { repoID, path } = this.props;
    seafileAPI.getFileServerUploadLink(repoID, path).then(res => {
      this.resumable.opts.target = res.data + '?ret-json=1';
      if (isFile && files.length === 1) {
        this.resumableUpload(resumableFile);
      } else {
        this.resumable.upload();
      }
    }).catch(error => {
      this.isUploadLinkLoaded = false;
      let errMessage = this.getAxiosErrorMessage(error);
      toaster.danger(errMessage);
    });
  };

  resumableUpload = (resumableFile) => {
    let { repoID, path } = this.props;
    seafileAPI.getFileUploadedBytes(repoID, path, resumableFile.fileName).then(res => {
      let uploadedBytes = res.data.uploadedBytes;
      let blockSize = parseInt(resumableUploadFileBlockSize) * 1024 * 1024 || 1024 * 1024;
      let offset = Math.floor(uploadedBytes / blockSize);
      resumableFile.markChunksCompleted(offset);
      this.resumable.upload();
    }).catch(error => {
      let errMessage = this.getAxiosErrorMessage(error);
      toaster.danger(errMessage);
    });
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

  // mergeUploadFileList unions the resumable.js files with the in-flight block
  // entries (which live outside this.resumable.files), deduped by uniqueIdentifier.
  // Rebuilding the list from this.resumable.files alone erased block rows from the
  // dialog the moment a legacy file was added after a block upload had started.
  mergeUploadFileList = (resumableFiles = this.resumable ? this.resumable.files : [], uploadFileList = this.state.uploadFileList) => {
    const merged = uploadFileList.filter(item => item && item.isBlockUpload);
    const seen = new Set(merged.map(item => item.uniqueIdentifier));
    (resumableFiles || []).forEach(file => {
      if (!seen.has(file.uniqueIdentifier)) {
        merged.push(file);
        seen.add(file.uniqueIdentifier);
      }
    });
    return merged;
  };

  setUploadFileList = (resumableFiles = this.resumable.files) => {
    const uploadFileList = this.mergeUploadFileList(resumableFiles);
    this.setState({
      uploadFileList,
      isUploadProgressDialogShow: true,
      totalProgress: this.calculateTotalProgress(uploadFileList),
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    });
  };

  onFileProgress = (resumableFile) => {
    const simultaneousUploads = getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads);
    let legacyUploadBitrate = this.getBitrate();
    updateAdaptiveUploadConcurrency(this.resumable, resumableFile, legacyUploadBitrate);
    let uploadFileList = this.state.uploadFileList.map(item => {
      if (item.uniqueIdentifier === resumableFile.uniqueIdentifier) {
        if (legacyUploadBitrate) {
          let lastSize = (item.size - (item.size * item.progress())) * 8;
          let time = Math.floor(lastSize / legacyUploadBitrate);
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
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
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
        this.setState({ uploadFileList, uploadBitrate: this.calculateUploadBitrate(uploadFileList) });
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
        return this.legacyUploadBitrate;
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
    this.legacyUploadBitrate = uploadBitrate;

    return uploadBitrate;
  };

  onProgress = () => {
    this.setState({ totalProgress: this.calculateTotalProgress(this.state.uploadFileList) });
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
    this.setState({
      uploadFileList: uploadFileList,
      totalProgress: this.calculateTotalProgress(uploadFileList),
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    });
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
    this.setState({
      retryFileList: retryFileList,
      uploadFileList: uploadFileList,
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    });
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
    this.props.onFileUploadSuccess(dirent); // this contance:  no repetition file

    this.markUploadSaved(resumableFile, entry.name);
  };

  getFileServerErrorMessage = (key) => {
    const errorMessage = {
      'File locked by others.': gettext('File is locked by others.'),       // 403
      'Invalid filename.': gettext('Invalid filename.'),                    // 440
      'File already exists.': gettext('File already exists.'),              // 441
      'File size is too large.': gettext('File size is too large.'),        // 442
      'Out of quota.': gettext('Out of quota.'),                            // 443
      'Internal error.': gettext('Internal Server Error'),                  // 500
    };
    return errorMessage[key] || key;
  };

  getQuotaErrorMessage = (payload) => {
    if (!payload || !payload.error) {
      return '';
    }
    if (payload.error === 'storage quota exceeded') {
      return gettext('Storage quota exceeded.');
    }
    if (payload.error === 'traffic quota exceeded') {
      switch (payload.reason) {
        case 'traffic-combined':
          return gettext('Combined traffic quota exceeded.');
        case 'traffic-upload':
          return gettext('Upload traffic quota exceeded.');
        case 'traffic-download':
          return gettext('Download traffic quota exceeded.');
        default:
          return gettext('Traffic quota exceeded.');
      }
    }
    return '';
  };

  getAxiosErrorMessage = (error) => {
    const quotaMessage = this.getQuotaErrorMessage(error && error.response && error.response.data);
    if (quotaMessage) {
      return quotaMessage;
    }
    return Utils.getErrorMsg(error);
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
      try {
        let errorMessage = message.replace(/\n/g, '');
        errorMessage = JSON.parse(errorMessage);
        error = this.getQuotaErrorMessage(errorMessage) || this.getFileServerErrorMessage(errorMessage.error);
      } catch (e) {
        error = gettext('Network error');
      }
    }

    const { retryFileList, uploadFileList } = moveUploadToRetryState(this.state.uploadFileList, this.state.retryFileList, resumableFile, error);

    this.loaded = 0;  // reset loaded data;
    this.setState({
      retryFileList: retryFileList,
      uploadFileList: uploadFileList,
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    });
    this.restoreConcurrencyIfIdle();
  };

  retryUploadWithFreshLink = (resumableFile, options = {}) => {
    const { resetAutoRetry = true } = options;

    // Reuse the session's existing upload link — do NOT mint a fresh one.
    // getFileServerUploadLink returns a brand-new upload TOKEN on each call, and
    // assigning it to the shared this.resumable.opts.target reroutes EVERY other
    // in-flight file onto a different server-side tracker mid-upload. The server
    // keys its chunk tracker by token, so a large concurrent upload gets split
    // across two trackers (token A then token B): neither ever reaches
    // contiguity, neither finalizes, and the file silently never lands — which
    // surfaces on the client as "Upload could not be confirmed". This happens
    // when one file hits a 409 ("library modified concurrently") and its
    // auto-retry swaps the token out from under a big file still uploading.
    // The session token is multi-use and valid for the whole session (it already
    // serves every chunk of every file), so retrying on it is correct.
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
      uploadFileList: uploadFileList,
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    });
    this.restoreConcurrencyIfIdle();
  };

  onComplete = () => {
    this.notifiedFolders = [];
    // reset upload link loaded
    this.isUploadLinkLoaded = false;
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
  };

  onPause = () => {
  };

  onError = (message) => {
    // A file-level error can fan out to the global error event before every
    // other chunk/file in the queue has stopped. Keep reusing the session link
    // until the queue is actually idle so a later file add cannot mint a new
    // token and split an in-flight upload across trackers.
    if (!this.resumable || !this.resumable.isUploading()) {
      this.isUploadLinkLoaded = false;
    }
  };

  onFileRetry = () => {
    noteAdaptiveUploadRetry(this.resumable);
  };

  onBeforeCancel = () => {
    // todo, giving a pop message ?
  };

  onCancel = () => {

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
    this.cancelActiveUploads();
    if (this.blockLimiter) {
      this.blockLimiter.reset();
    }
    this.loaded = 0;
    this.legacyUploadBitrate = 0;
    this.resumable.files = [];
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
    this.restoreConcurrencyIfIdle();
    // reset upload link loaded
    this.isUploadLinkLoaded = false;
    this.setState({ isUploadProgressDialogShow: false, uploadFileList: [], forbidUploadFileList: [], retryFileList: [], totalProgress: 0, uploadBitrate: 0 });
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

    const hasActiveUploads = Boolean(this.resumable && this.resumable.isUploading && this.resumable.isUploading())
      || this.hasUploadingEntries(uploadFileList);

    if (!hasActiveUploads) {
      this.loaded = 0;
    }

    this.setState({
      uploadFileList: uploadFileList,
      totalProgress: hasActiveUploads ? this.calculateTotalProgress(uploadFileList) : 100,
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    });
    this.restoreConcurrencyIfIdle();
  };

  onCancelAllUploading = () => {
    let uploadFileList = this.state.uploadFileList.filter(item => {
      // Cancel every unsaved entry, including block uploads already at 100% that
      // are still inside the final commit ('saving').
      // (The old `Math.round(item.progress() !== 1)` rounded a boolean — it worked
      // only by accident; compare against isSaved directly.)
      if (!item.isSaved) {
        clearFileUploadRuntimeState(item, { resetRemainingTime: true });
        item.cancel();
        return false;
      }
      return true;
    });

    this.loaded = 0;
    this.legacyUploadBitrate = 0;
    if (this.blockLimiter) {
      this.blockLimiter.reset();
    }

    this.setState({
      totalProgress: 100,
      uploadFileList: uploadFileList,
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    });
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
    this.restoreConcurrencyIfIdle();
    // reset upload link loaded
    this.isUploadLinkLoaded = false;
  };

  onUploadRetry = (resumableFile) => {
    if (resumableFile.isBlockUpload) {
      this.retryBlockUpload(resumableFile);
      return;
    }
    this.retryUploadWithFreshLink(resumableFile);
  };

  onUploadRetryAll = () => {
    // Reuse the session's existing upload link instead of fetching a fresh one.
    // Re-fetching mints a new upload token and overwrites the shared
    // this.resumable.opts.target, which would reroute any still-in-flight file
    // onto a different server-side tracker mid-upload and split it across two
    // trackers (see retryUploadWithFreshLink). The session token stays valid.
    const blockRetryList = [];
    this.state.retryFileList.forEach(item => {
      if (item.isBlockUpload) {
        this.prepareBlockUploadRetry(item);
        blockRetryList.push(item);
        return;
      }
      clearFileUploadRuntimeState(item, { resetRemainingTime: true });
      resetUploadConflictAutoRetry(item);
      item.error = null;
      this.retryUploadFile(item);
    });

    let uploadFileList = this.state.uploadFileList.slice(0);
    this.setState({
      retryFileList: [],
      uploadFileList: uploadFileList,
      totalProgress: this.calculateTotalProgress(uploadFileList),
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    }, () => {
      blockRetryList.forEach(item => {
        this.runBlockUpload(item, item.file);
      });
    });
    this.restoreConcurrencyIfIdle();
  };

  retryBlockUpload = (entry) => {
    this.prepareBlockUploadRetry(entry);
    let retryFileList = this.state.retryFileList.filter(item => {
      return item.uniqueIdentifier !== entry.uniqueIdentifier;
    });
    let uploadFileList = this.state.uploadFileList.slice(0);
    this.setState({
      retryFileList: retryFileList,
      uploadFileList: uploadFileList,
      totalProgress: this.calculateTotalProgress(uploadFileList),
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    }, () => {
      this.runBlockUpload(entry, entry.file);
    });
    this.restoreConcurrencyIfIdle();
  };

  retryUploadFile = (resumableFile) => {
    let { repoID, path } = this.props;
    let fileName = resumableFile.fileName;
    let isFile = resumableFile.fileName === resumableFile.relativePath;
    if (!isFile) {
      let relative_path = resumableFile.formData.relative_path;
      let prefix = path === '/' ? (path + relative_path) : (path + '/' + relative_path);
      fileName = prefix + fileName;
    }

    resumableFile.bootstrap();
    var firedRetry = false;
    resumableFile.resumableObj.on('chunkingComplete', () => {
      if (!firedRetry) {
        seafileAPI.getFileUploadedBytes(repoID, path, fileName).then(res => {
          let uploadedBytes = res.data.uploadedBytes;
          let blockSize = parseInt(resumableUploadFileBlockSize) * 1024 * 1024 || 1024 * 1024;
          let offset = Math.floor(uploadedBytes / blockSize);
          resumableFile.markChunksCompleted(offset);

          resumableFile.resumableObj.upload();

        }).catch(error => {
          let errMessage = Utils.getErrorMsg(error);
          toaster.danger(errMessage);
        });
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
      this.setState({ isUploadRemindDialogShow: false });
      this.setUploadFileList(this.resumable.files);
      this.resumable.upload();
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  uploadFile = () => {
    let resumableFile = this.resumable.files[this.resumable.files.length - 1];
    resumableFile.formData['replace'] = 0;
    let { repoID, path } = this.props;
    seafileAPI.getFileServerUploadLink(repoID, path).then((res) => {  // get upload link
      // Per-file target (file.opts wins over the shared resumable.opts in
      // getOpt) so accepting the "upload anyway" dialog for one file never
      // overwrites the global target and reroutes other in-flight uploads.
      resumableFile.opts = resumableFile.opts || {};
      resumableFile.opts.target = res.data + '?ret-json=1';
      this.setState({
        isUploadRemindDialogShow: false,
        isUploadProgressDialogShow: true,
        uploadFileList: [...this.state.uploadFileList, resumableFile]
      }, () => {
        this.resumable.upload();
      });
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  };

  cancelFileUpload = () => {
    this.resumable.files.pop(); //delete latest file；
    this.setState({ isUploadRemindDialogShow: false });
  };

  render() {
    return (
      <Fragment>
        <div className="file-uploader-container">
          <div className="file-uploader">
            <input className="upload-input" type="file" ref={this.uploadInput} onClick={this.onClick} aria-label={gettext('Upload')} />
          </div>
        </div>
        {this.state.isUploadRemindDialogShow &&
          <UploadRemindDialog
            currentResumableFile={this.state.currentResumableFile}
            replaceRepetitionFile={this.replaceRepetitionFile}
            uploadFile={this.uploadFile}
            cancelFileUpload={this.cancelFileUpload}
          />
        }
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
            isUploading={this.isUploading()}
          />
        }
      </Fragment>
    );
  }
}

FileUploader.propTypes = propTypes;

export default FileUploader;