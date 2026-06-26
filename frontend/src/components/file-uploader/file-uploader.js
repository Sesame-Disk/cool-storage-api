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
      // True while the current duplicate prompt is part of a multi-file batch, so
      // the dialog offers an "apply to all" choice.
      duplicateBatchActive: false,
    };

    this.uploadInput = React.createRef();

    // Synchronous duplicate guard. fileNameExistsInDir reads this.state.uploadFileList
    // (async setState) and only matches a legacy file once it is uploading/saved, so a
    // rapid SECOND fileAdded for the same destination can slip past it before the first
    // is visible — producing two rows. This Set of destination keys (repoID:path:
    // relativePath) is updated SYNCHRONOUSLY the moment a file is committed (queued or
    // held for a prompt), so the second drop is caught immediately. Released when the
    // file reaches a terminal state (saved / cancelled / dialog closed).
    this.activeUploadNameKeys = new Set();

    this.notifiedFolders = [];

    this.timestamp = null;
    this.loaded = 0;
    this.legacyUploadBitrate = 0;
    this.bitrateInterval = 500; // Interval in milliseconds to calculate the bitrate
    window.onbeforeunload = this.onbeforeunload;
    this.isUploadLinkLoaded = false;
    // Cached promise of the shared resumable upload target for the current batch.
    // Both the normal add flow and the duplicate-resolution flow await it before
    // calling resumable.upload(), so a file is never POSTed to the empty default
    // target (→ 405 on the page URL). Reset (to null) — but the target itself is
    // NEVER cleared — wherever isUploadLinkLoaded is reset; a legacy retry reuses
    // the last token, so clearing opts.target would re-create a retry-405.
    this._uploadTargetPromise = null;
    // Files whose name already exists in the folder, held OUT of the resumable
    // queue (via removeFile) and OUT of the rendered list until the user decides
    // replace / keep / cancel. Prompted one at a time; `duplicateBulkAction`
    // short-circuits the prompt once the user picks "apply to all".
    this.pendingDuplicates = [];
    this.duplicateBulkAction = null;
    // Identity of the current add batch (resumable passes the same `files` array to
    // every fileAdded within one drag/selection). Scopes "apply to all" to its batch
    // instead of every later upload in the session.
    this._currentBatchRef = null;
    // Target-mode scheduler for LEGACY resumable uploads. resumablejs@1.1.16's
    // $h.getTarget reads ONLY the instance-level resumable.opts.target (it ignores
    // per-file opts.target), so every chunk of every queued file POSTs to that single
    // target. Files needing different endpoints — 'upload' (new/keep → upload-link) vs
    // 'update' (replace → update-link) — therefore CANNOT run concurrently. The
    // scheduler serializes them: while one mode is in flight, files of the other mode
    // are held out of resumable.files (rendered "Waiting…") and started after the queue
    // goes idle and the instance target is switched. No-conflict batches (all-upload or
    // all-update) never hit the hold path.
    this.legacyHold = []; // [{ resumableFile, mode }] held due to a target-mode conflict
    this.activeLegacyMode = null; // 'upload' | 'update' | null (queue idle)
    // True while cancel-all / close-dialog is tearing the session down, so a resumable
    // 'cancel' event cannot start a held mode group mid-reset.
    this._resettingUploads = false;
    // Cached update-link promise for the current replace group: multiple replace files
    // share the single instance target, so they reuse one update-link token. Reset when
    // the session ends (close / cancel-all).
    this._replaceUpdateLinkPromise = null;
    this.adaptiveUploadCleanup = null;
    // Block (CAS) uploads run ONE FILE AT A TIME: a file-level FIFO on top of the
    // block-level limiter. So a single large file gets the full block-concurrency
    // ceiling while the others wait ("Waiting..."), instead of several large files
    // crawling in parallel. Legacy resumable uploads are unaffected.
    this.blockUploadQueue = [];
    this.activeBlockUpload = null;
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
    // Re-queued for retry: it waits in the FIFO until it actually starts, so render
    // it as "Waiting…" ('queued'), not "Hashing…". runBlockUpload sets 'hashing'.
    entry._phase = 'queued';
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
    // The entry renders immediately (progress 0 → "Waiting...") and is started by the
    // file-level queue when no other block upload is active.
    this.enqueueBlockUpload(entry, file);
    return true;
  };

  // enqueueBlockUpload / drainBlockUploadQueue serialize block (CAS) uploads to ONE
  // active file at a time. The block-level limiter still bounds concurrency WITHIN the
  // active file; this only governs how many block FILES run at once (exactly one).
  // Idempotent per entry (by object identity): a double Retry / Retry-All, or events
  // firing before React removes the item from retryFileList, must not enqueue the same
  // entry twice and open two CAS sessions for one file.
  enqueueBlockUpload = (entry, file) => {
    if (!entry) {
      return false;
    }
    if (this.activeBlockUpload && this.activeBlockUpload.entry === entry) {
      return false; // already running
    }
    if (this.blockUploadQueue.some(job => job.entry === entry)) {
      return false; // already waiting
    }
    this.blockUploadQueue.push({ entry, file });
    this.drainBlockUploadQueue();
    return true;
  };

  drainBlockUploadQueue = () => {
    if (this.activeBlockUpload) {
      return; // a block file is already running; the rest stay "Waiting..."
    }
    const job = this.blockUploadQueue.shift();
    if (!job) {
      return;
    }
    this.activeBlockUpload = job;
    // runBlockUpload resolves on success, handled error, or cancel (it never rejects),
    // so the queue always advances. Guard a synchronous throw before the promise is
    // returned so a future bug cannot strand activeBlockUpload and wedge the queue.
    let promise;
    try {
      promise = this.runBlockUpload(job.entry, job.file);
    } catch (error) {
      promise = Promise.reject(error);
    }
    Promise.resolve(promise).finally(() => {
      if (this.activeBlockUpload === job) {
        this.activeBlockUpload = null;
      }
      this.drainBlockUploadQueue();
    });
  };

  // releaseActiveBlockSlotForCommit hands the file-level slot back the moment the
  // active block file enters its commit ('saving') phase, so the next queued file
  // can start uploading while this one's commit is in flight. Idempotent per job
  // (the needs_upload path can re-enter 'saving'): once released, the job no longer
  // owns the slot, so the guard and the job's own finally (which only nulls when it
  // still owns the slot) cannot double-release or strand the queue.
  releaseActiveBlockSlotForCommit = (entry) => {
    const job = this.activeBlockUpload;
    if (!job || job.entry !== entry || job._committing) {
      return;
    }
    job._committing = true;
    this.activeBlockUpload = null;
    this.drainBlockUploadQueue();
  };

  // removeQueuedBlockUpload drops a not-yet-started block file from the queue (it never
  // ran, so there is nothing to abort). Matched by object identity so re-uploading the
  // same filename in a later batch (a different entry) is not removed by accident. The
  // active file is cancelled via entry.cancel().
  removeQueuedBlockUpload = (item) => {
    this.blockUploadQueue = this.blockUploadQueue.filter(job => job.entry !== item);
  };

  // clearBlockUploadQueue drops every PENDING block file (dialog close / cancel-all).
  // It deliberately does NOT clear activeBlockUpload: the active file is being aborted
  // via entry.cancel() but its promise may not have reached the finally yet, so nulling
  // the marker here would let a freshly-added file start while the old one is still
  // tearing down — breaking the one-active-at-a-time guarantee. The marker is released
  // only by the active job's own finally.
  clearBlockUploadQueue = () => {
    this.blockUploadQueue = [];
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
      // renders: 'queued' | 'hashing' | 'checking' | 'uploading' | 'saving' |
      // 'done' | 'error'. Starts 'queued' so a file WAITING in the file-level FIFO
      // renders "Waiting…", not "Hashing…" (only the active file is hashing).
      // runBlockUpload flips it to 'hashing' when the file actually starts; the rest
      // is driven by the orchestrator's onPhase callback, NOT inferred from
      // resumable.js chunk/remainingTime state (a block entry has none).
      _phase: 'queued',
      _abortController: null,
      _cancelled: false,
      _progress: 0,
      _uploading: true,
      // Dedup plan from /blocks/check (set via onPlan): bytes already on the server
      // (shared/repeated blocks) vs bytes actually uploaded. 0 until the check runs.
      _dedupedBytes: 0,
      _uploadBytes: 0,
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
    if (phase === 'saving') {
      // The bytes are all on the server; only the commit is left. Release the
      // file-level slot NOW so the next queued block file starts uploading while
      // this one finishes its commit (the block limiter still caps total blocks
      // on the wire). Mirrors the legacy finalize-slot handoff.
      this.releaseActiveBlockSlotForCommit(entry);
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

  // setBlockUploadPlan records the dedup plan from /blocks/check so the row can show
  // how much of the file was already on the server (skipped) vs actually uploaded.
  setBlockUploadPlan = (entry, plan) => {
    if (!plan) {
      return;
    }
    entry._dedupedBytes = Math.max(0, Number(plan.dedupedBytes) || 0);
    entry._uploadBytes = Math.max(0, Number(plan.uploadBytes) || 0);
    this.setState(prev => {
      const uploadFileList = prev.uploadFileList.map(item => (
        item.uniqueIdentifier === entry.uniqueIdentifier ? entry : item
      ));
      return { uploadFileList };
    });
  };

  runBlockUpload = (entry, file, { replace = entry._replace || false } = {}) => {
    const { repoID, path } = this.props;
    const abortController = typeof AbortController === 'function' ? new AbortController() : null;
    entry._abortController = abortController;
    entry._cancelled = false;
    entry._uploading = true;
    entry.error = null;
    entry.isSaved = false;
    entry.remainingTime = -1;
    entry._phase = 'hashing';
    entry._dedupedBytes = 0;
    entry._uploadBytes = 0;
    // Persist the replace decision so a Retry / Retry-All re-runs with the same
    // semantics the user chose in the duplicate dialog.
    entry._replace = replace;
    resetBlockUploadBitrate(entry);

    // Hashing is the first half of the bar, uploading the second half. onPhase
    // drives the explicit entry._phase so the row renders Uploading…/Saving…
    // correctly; onTransferProgress feeds real wire bytes for the speed readout.
    // Returns the settled promise so the file-level queue can start the next file.
    return uploadFileViaBlocks(file, {
      repoID,
      parentDir: path,
      filename: file.name,
      replace,
      // Shared global ceiling: every block upload competes for the same slots.
      limiter: this.blockLimiter,
      signal: abortController ? abortController.signal : undefined,
      onPhase: (phase) => this.setBlockUploadPhase(entry, phase),
      onPlan: (plan) => this.setBlockUploadPlan(entry, plan),
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

  // fileNameExistsInDir reports whether a file with this exact name already lives in
  // the current folder OR was already uploaded / is uploading in THIS session (so the
  // upload would replace or auto-rename it). The session check matters because the
  // server-provided direntList prop may not have refreshed after a just-finished
  // upload — without it, re-dropping the same file silently produced a SECOND row
  // ("Waiting..." next to "Uploaded") instead of offering the Replace? prompt.
  fileNameExistsInDir = (fileName) => {
    const direntList = this.props.direntList || [];
    if (direntList.some(d => d && d.type === 'file' && d.name === fileName)) {
      return true;
    }
    return (this.state.uploadFileList || []).some(item => (
      item
      && !item.error
      && item.fileName === fileName
      && (item.isSaved || (typeof item.isUploading === 'function' && item.isUploading()))
    ));
  };

  // handleDuplicateFile pulls the file OUT of the resumable queue (so the running
  // uploader cannot start it with the wrong target before the user decides) AND
  // keeps it out of the rendered list, then either applies an already-chosen bulk
  // action or queues it for a prompt.
  handleDuplicateFile = (resumableFile, files) => {
    this.resumable.removeFile(resumableFile);
    if (this.duplicateBulkAction) {
      this.applyDuplicateDecision(resumableFile, this.duplicateBulkAction);
      return;
    }
    this.pendingDuplicates.push(resumableFile);
    const inBatch = (Array.isArray(files) && files.length > 1) || this.pendingDuplicates.length > 1;
    if (!this.state.isUploadRemindDialogShow) {
      this.showNextDuplicatePrompt(inBatch);
    } else if (inBatch && !this.state.duplicateBatchActive) {
      this.setState({ duplicateBatchActive: true });
    }
  };

  // showNextDuplicatePrompt advances the queue: it surfaces the next held duplicate
  // in the replace dialog, or closes the dialog when the queue is empty.
  showNextDuplicatePrompt = (inBatch = false) => {
    const next = this.pendingDuplicates.shift();
    if (!next) {
      // No more decisions pending. If the only thing that opened the panel was a
      // duplicate prompt the user then cancelled (nothing actually uploading or
      // uploaded), don't leave an empty progress dialog behind.
      const hasVisibleUploads = (this.state.uploadFileList || []).length > 0;
      this.setState({
        isUploadRemindDialogShow: false,
        currentResumableFile: null,
        duplicateBatchActive: false,
        isUploadProgressDialogShow: hasVisibleUploads,
      });
      return;
    }
    this.setState({
      isUploadProgressDialogShow: true,
      isUploadRemindDialogShow: true,
      currentResumableFile: next,
      // Offer "apply to all" while there is more than one duplicate in play.
      duplicateBatchActive: inBatch || this.pendingDuplicates.length > 0,
    });
  };

  // applyDuplicateDecision routes one resolved file to its real upload flow with the
  // chosen replace semantics: block flow for large eligible files, legacy resumable
  // otherwise. 'cancel' simply drops the held file (already removed from the queue).
  applyDuplicateDecision = (resumableFile, action) => {
    if (!resumableFile || action === 'cancel') {
      // Cancel drops the held duplicate: release its destination key so a later drop of
      // the same name can be offered again (the held file owns the key — a rapid second
      // drop was caught and never registered one).
      if (resumableFile && action === 'cancel') {
        this.activeUploadNameKeys.delete(this.getUploadDestinationKey(resumableFile));
      }
      return;
    }
    const replace = action === 'replace';
    const file = resumableFile.file;
    if (file && shouldUseBlockUpload(file, { encrypted: this.props.repoEncrypted })) {
      const entry = this.createBlockUploadEntry(resumableFile);
      entry._replace = replace; // persisted; the file-level queue runs it later
      this.setState(prev => ({
        uploadFileList: [...prev.uploadFileList, entry],
        isUploadProgressDialogShow: true,
      }));
      this.enqueueBlockUpload(entry, file);
      return;
    }
    this.startLegacyDuplicateUpload(resumableFile, replace);
  };

  // getCurrentParentDir returns the folder path WITH a trailing slash, matching the
  // shape onChunkingComplete writes into formData.parent_dir, so target_file is always
  // "/folder/name", never the malformed "/foldername".
  getCurrentParentDir = () => (this.props.path === '/' ? '/' : `${this.props.path}/`);

  // ensureReplaceUpdateLink fetches (once per replace group) the update-link used as
  // the instance target for replace ('update' mode) uploads. Cached so multiple
  // replace files in one group share a single token; reset at session end.
  ensureReplaceUpdateLink = () => {
    if (this._replaceUpdateLinkPromise) {
      return this._replaceUpdateLinkPromise;
    }
    const { repoID, path } = this.props;
    this._replaceUpdateLinkPromise = seafileAPI.getUpdateLink(repoID, path)
      .then(res => res.data)
      .catch(error => {
        this._replaceUpdateLinkPromise = null; // allow a retry on next attempt
        throw error;
      });
    return this._replaceUpdateLinkPromise;
  };

  // renderLegacyList rebuilds the rendered upload list from resumable.files + the
  // held ("Waiting…") files + block entries, and refreshes progress/bitrate. It uses a
  // FUNCTIONAL setState (reads prev.uploadFileList), so a block entry just added by an
  // earlier-but-not-yet-committed setState is preserved. With a plain setState the value
  // is computed against stale this.state, and under React batching this render would
  // OVERWRITE the list and silently drop a large block-flow file that was added moments
  // earlier (it never even appeared in the list).
  renderLegacyList = () => {
    this.setState(prev => {
      const uploadFileList = this.mergeUploadFileList(this.resumable.files, prev.uploadFileList);
      return {
        isUploadProgressDialogShow: true,
        uploadFileList,
        totalProgress: this.calculateTotalProgress(uploadFileList),
        uploadBitrate: this.calculateUploadBitrate(uploadFileList),
      };
    });
  };

  // enqueueLegacyUpload starts (or holds) an already-prepared legacy resumable file
  // under the target-mode scheduler. 'upload' = new/keep via the upload-link; 'update'
  // = replace via the update-link. Because resumablejs routes every chunk to the single
  // instance-level opts.target, only ONE mode can be in flight at a time: a file whose
  // mode conflicts with the active one is held (rendered "Waiting…") until the queue
  // goes idle and the instance target is switched. A no-conflict file starts at once.
  enqueueLegacyUpload = (resumableFile, mode, { resume = false } = {}) => {
    resumableFile._uploadMode = mode;
    resumableFile._resumeOnStart = resume;
    // Self-heal a stale active mode if an idle event was missed (queue truly idle and
    // nothing held), so a new mode is not blocked forever.
    if (!this.resumable.isUploading() && this.legacyHold.length === 0) {
      this.activeLegacyMode = null;
    }
    if (this.activeLegacyMode !== null && this.activeLegacyMode !== mode) {
      this.holdLegacyFile(resumableFile, mode);
      return;
    }
    this.activeLegacyMode = mode;
    this.startLegacyFiles([resumableFile]);
  };

  // startLegacyFiles ensures the instance target for the (single, shared) mode of the
  // given queued files, then uploads. All files passed share one mode.
  startLegacyFiles = (resumableFiles) => {
    if (resumableFiles.length === 0) {
      return;
    }
    const mode = resumableFiles[0]._uploadMode;
    resumableFiles.forEach(f => {
      if (this.resumable.files.indexOf(f) === -1) {
        this.resumable.files.push(f);
      }
    });
    this.renderLegacyList();

    const targetPromise = mode === 'update' ? this.ensureReplaceUpdateLink() : this.ensureSharedUploadTarget();
    targetPromise.then((target) => {
      // resumablejs ignores per-file opts.target — route the whole queue via the
      // instance target. Safe because the scheduler guarantees only same-mode files
      // are in resumable.files right now.
      this.resumable.opts.target = target;
      // A lone fresh normal file resumes from its uploaded-bytes offset; otherwise just
      // start the queue.
      if (mode === 'upload' && resumableFiles.length === 1 && resumableFiles[0]._resumeOnStart) {
        this.resumableUpload(resumableFiles[0]);
      } else {
        this.resumable.upload();
      }
    }).catch((error) => {
      toaster.danger(this.getAxiosErrorMessage(error));
      // The group's target could not be fetched and it never started. Take its files OUT
      // of resumable.files (so a later DIFFERENT-mode group can't run with them mixed in
      // against the wrong instance target) and FREE the active mode, otherwise the queue
      // would wedge — no real complete/error event fires to drain held work.
      resumableFiles.forEach(f => {
        this.resumable.removeFile(f);
        this.legacyHold = this.legacyHold.filter(h => h.resumableFile !== f);
        if (f._fromDuplicatePrompt) {
          this.pendingDuplicates.unshift(f); // re-offer the user's pending decision
        } else {
          // Abandon a plain normal file (release its dedup key so it can be re-added).
          this.activeUploadNameKeys.delete(this.getUploadDestinationKey(f));
        }
      });
      if (this.activeLegacyMode === mode) {
        this.activeLegacyMode = null;
      }
      this.renderLegacyList();
      if (!this.state.isUploadRemindDialogShow && this.pendingDuplicates.length > 0) {
        this.showNextDuplicatePrompt();
      }
      // Continue with any other held mode group now that this one is cleared.
      this.onLegacyQueueIdle();
    });
  };

  // holdLegacyFile pulls a file out of resumable.files (so the running other-mode queue
  // cannot start it against the wrong instance target) and records it as held; it
  // renders as a "Waiting…" row and is started by onLegacyQueueIdle when its turn comes.
  holdLegacyFile = (resumableFile, mode) => {
    this.resumable.removeFile(resumableFile);
    resumableFile.remainingTime = -1; // show "Preparing…" rather than a stuck 0% bar
    this.legacyHold.push({ resumableFile, mode });
    this.renderLegacyList();
  };

  // onLegacyQueueIdle runs when the resumable queue goes idle (complete / all errored /
  // cancelled). It clears the active mode and, if files of another mode are held, starts
  // the next same-mode group (switching the instance target for it). It is a no-op while
  // the queue is still uploading, or while a cancel-all/close reset is in progress (so a
  // resumable 'cancel' event cannot start held work as the session is being torn down).
  onLegacyQueueIdle = () => {
    if (this._resettingUploads || (this.resumable && this.resumable.isUploading())) {
      return;
    }
    this.activeLegacyMode = null;
    if (this.legacyHold.length === 0) {
      return;
    }
    const mode = this.legacyHold[0].mode;
    const group = this.legacyHold.filter(h => h.mode === mode).map(h => h.resumableFile);
    this.legacyHold = this.legacyHold.filter(h => h.mode !== mode);
    this.activeLegacyMode = mode;
    this.startLegacyFiles(group);
  };

  // reofferLegacyFile pulls a (held duplicate) file back out and re-prompts, leaving no
  // stale "Waiting…" row. Used when its target fetch fails.
  reofferLegacyFile = (resumableFile) => {
    this.resumable.removeFile(resumableFile);
    this.legacyHold = this.legacyHold.filter(h => h.resumableFile !== resumableFile);
    this.renderLegacyList();
    this.pendingDuplicates.unshift(resumableFile);
    if (!this.state.isUploadRemindDialogShow) {
      this.showNextDuplicatePrompt();
    }
  };

  // startLegacyDuplicateUpload prepares a held legacy duplicate and hands it to the
  // target-mode scheduler:
  //   - "keep"    → 'upload' mode (shared upload-link; backend auto-renames to "name (1).ext"),
  //   - "replace" → 'update' mode (update-link as the instance target + replace flag).
  startLegacyDuplicateUpload = (resumableFile, replace) => {
    resumableFile.formData = resumableFile.formData || {};
    resumableFile.opts = resumableFile.opts || {};
    resumableFile._fromDuplicatePrompt = true;
    // Clear routing left by a PREVIOUS decision on this SAME held object (e.g. a Replace
    // attempt that was re-offered): a later "Don't replace" must not inherit the stale
    // update target_file and overwrite the file against the user's choice.
    delete resumableFile.opts.target;
    delete resumableFile.formData.target_file;

    const parentDir = resumableFile.formData.parent_dir || this.getCurrentParentDir();
    resumableFile.formData['parent_dir'] = parentDir;

    if (replace) {
      resumableFile.formData['replace'] = 1;
      resumableFile.formData['target_file'] = `${parentDir}${resumableFile.fileName}`;
      this.enqueueLegacyUpload(resumableFile, 'update');
    } else {
      resumableFile.formData['replace'] = 0;
      this.enqueueLegacyUpload(resumableFile, 'upload');
    }
  };

  // resolveDuplicate is invoked by the replace dialog. It applies the action to the
  // current file and, when "apply to all" is checked, drains every remaining held
  // duplicate with the same action; otherwise it advances to the next prompt.
  resolveDuplicate = (action, applyToAll) => {
    const file = this.state.currentResumableFile;
    this.setState({ isUploadRemindDialogShow: false, currentResumableFile: null });
    if (applyToAll) {
      this.duplicateBulkAction = action;
    }
    this.applyDuplicateDecision(file, action);
    if (applyToAll) {
      const rest = this.pendingDuplicates.splice(0);
      rest.forEach(f => this.applyDuplicateDecision(f, action));
      // A bulk "Cancel" uploads nothing; if there is nothing else visible, don't leave
      // an empty progress panel behind. Replace/keep always produce uploads (some via an
      // async link fetch), so keep the panel open for those.
      const hasVisibleUploads = (this.state.uploadFileList || []).length > 0;
      this.setState({
        duplicateBatchActive: false,
        isUploadProgressDialogShow: action === 'cancel' ? hasVisibleUploads : true,
      });
    } else {
      this.showNextDuplicatePrompt();
    }
  };

  // getUploadDestinationKey returns the synchronous duplicate-guard key for a file.
  // relativePath (not just fileName) distinguishes folder-upload siblings in different
  // subdirs; for a standalone file relativePath === fileName.
  getUploadDestinationKey = (resumableFile) => {
    const relative = resumableFile.relativePath || resumableFile.fileName;
    return `${this.props.repoID}:${this.props.path}:${relative}`;
  };

  onFileAdded = (resumableFile, files) => {
    const { isCustomPermission } = this.props;
    const isFile = resumableFile.fileName === resumableFile.relativePath;

    // A fresh drag/selection is a new batch (resumable hands the same `files` array
    // to every fileAdded within one batch). An "apply to all" choice is scoped to its
    // batch and must NOT silently auto-resolve a duplicate the user adds later.
    if (files !== this._currentBatchRef) {
      this._currentBatchRef = files;
      this.duplicateBulkAction = null;
    }

    // Synchronous duplicate guard: catch a rapid SECOND add of the same destination
    // BEFORE fileNameExistsInDir (which depends on async setState + isUploading state).
    // The first add registers the key below; a second one is dropped here so it can
    // never materialize a duplicate row.
    const destKey = this.getUploadDestinationKey(resumableFile);
    if (this.activeUploadNameKeys.has(destKey)) {
      this.resumable.removeFile(resumableFile);
      toaster.warning(gettext('This file is already queued for upload.'));
      return;
    }

    // Duplicate-name detection runs for EVERY file (single OR batch, small OR large)
    // and BEFORE the block-flow diversion below, so a duplicate inside a batch — or a
    // large file routed to the block flow — is offered the replace dialog instead of
    // silently landing as "name (1).ext". The matched file is held OUT of the queue
    // and the rendered list until the user decides.
    if (isFile && !isCustomPermission && this.fileNameExistsInDir(resumableFile.fileName)) {
      this.activeUploadNameKeys.add(destKey); // reserve so a rapid second drop is caught
      this.handleDuplicateFile(resumableFile, files);
      return;
    }

    // Committed to upload — reserve the destination key synchronously.
    this.activeUploadNameKeys.add(destKey);

    // Web content-addressed (block) upload flow, behind enableBlockUpload. Large
    // non-encrypted files are diverted out of resumable.js. Anything ineligible
    // falls through to resumable.js.
    if (this.maybeBlockUpload(resumableFile)) {
      return;
    }

    // Normal legacy file → upload-link ('upload') mode via the target-mode scheduler.
    // A single file resumes from its uploaded-bytes offset; a batch just joins the queue.
    const isSingleFile = isFile && files.length === 1;
    this.enqueueLegacyUpload(resumableFile, 'upload', { resume: isSingleFile });
  };

  // ensureSharedUploadTarget fetches the session upload link EXACTLY ONCE per batch
  // and sets the shared this.resumable.opts.target, returning a cached promise that
  // resolves when the target is set. Fetching once matters: re-minting a token and
  // overwriting the shared target reroutes any in-flight file onto a different
  // server-side tracker (keyed by token). Both onFileAdded and the duplicate flow
  // await this, so no file POSTs to the empty default target (→ 405). The target is
  // OVERWRITTEN with a fresh token per batch (matching the original onFileAdded) but
  // NEVER cleared elsewhere — a legacy retry reuses the last token.
  ensureSharedUploadTarget = () => {
    if (this._uploadTargetPromise) {
      return this._uploadTargetPromise;
    }
    this.isUploadLinkLoaded = true;
    const { repoID, path } = this.props;
    this._uploadTargetPromise = seafileAPI.getFileServerUploadLink(repoID, path).then(res => {
      this.resumable.opts.target = res.data + '?ret-json=1';
      return this.resumable.opts.target;
    }).catch(error => {
      // Allow a later add/retry to try again.
      this.isUploadLinkLoaded = false;
      this._uploadTargetPromise = null;
      throw error;
    });
    return this._uploadTargetPromise;
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
  // entries (which live outside this.resumable.files) AND the scheduler-held legacy
  // files (pulled out of resumable.files but rendered "Waiting…"), deduped by
  // uniqueIdentifier. Rebuilding from this.resumable.files alone erased block rows the
  // moment a legacy file was added, and would drop held files entirely.
  mergeUploadFileList = (resumableFiles = this.resumable ? this.resumable.files : [], uploadFileList = this.state.uploadFileList) => {
    const merged = uploadFileList.filter(item => item && item.isBlockUpload);
    const seen = new Set(merged.map(item => item.uniqueIdentifier));
    const addAll = (files) => (files || []).forEach(file => {
      if (file && !seen.has(file.uniqueIdentifier)) {
        merged.push(file);
        seen.add(file.uniqueIdentifier);
      }
    });
    addAll(resumableFiles);
    addAll((this.legacyHold || []).map(h => h.resumableFile));
    return merged;
  };

  setUploadFileList = (resumableFiles = this.resumable.files) => {
    // Functional setState so an in-flight block entry added by a not-yet-committed
    // setState is preserved (see renderLegacyList).
    this.setState(prev => {
      const uploadFileList = this.mergeUploadFileList(resumableFiles, prev.uploadFileList);
      return {
        uploadFileList,
        isUploadProgressDialogShow: true,
        totalProgress: this.calculateTotalProgress(uploadFileList),
        uploadBitrate: this.calculateUploadBitrate(uploadFileList),
      };
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
    // Release the synchronous duplicate key on success, so a LATER drop of the same
    // name is offered the Replace? prompt (via fileNameExistsInDir's isSaved check)
    // instead of being silently blocked as "already queued".
    this.activeUploadNameKeys.delete(this.getUploadDestinationKey(resumableFile));
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
    // Batch done: drop the cached link promises so the next batch / next replace group
    // re-fetches a fresh token. The target itself is NOT cleared (a retry reuses the
    // last token).
    this.isUploadLinkLoaded = false;
    this._uploadTargetPromise = null;
    this._replaceUpdateLinkPromise = null;
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
    // The legacy queue is idle — run the next held target-mode group (if any).
    this.onLegacyQueueIdle();
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
      this._uploadTargetPromise = null;
      this.onLegacyQueueIdle(); // queue stopped → switch to the next held mode group
    }
  };

  onFileRetry = () => {
    noteAdaptiveUploadRetry(this.resumable);
  };

  onBeforeCancel = () => {
    // todo, giving a pop message ?
  };

  onCancel = () => {
    // The whole queue was cancelled → run any held mode group.
    this.onLegacyQueueIdle();
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
    // Reset the scheduler FIRST so a resumable 'cancel' fired by cancelActiveUploads()
    // (→ onCancel → onLegacyQueueIdle) cannot start a held mode group mid-teardown.
    this._resettingUploads = true;
    this.legacyHold = [];
    this.activeLegacyMode = null;
    this.cancelActiveUploads();
    this.clearBlockUploadQueue();
    if (this.blockLimiter) {
      this.blockLimiter.reset();
    }
    this.loaded = 0;
    this.legacyUploadBitrate = 0;
    this.resumable.files = [];
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
    this.restoreConcurrencyIfIdle();
    // reset upload link loaded + cached target promises so the next batch re-fetches
    this.isUploadLinkLoaded = false;
    this._uploadTargetPromise = null;
    this._replaceUpdateLinkPromise = null;
    // Scheduler already reset at the top; clear the synchronous duplicate guard too.
    this.activeUploadNameKeys.clear();
    // Drop any undecided duplicates and the bulk choice so the next session starts clean.
    this.pendingDuplicates = [];
    this.duplicateBulkAction = null;
    this._resettingUploads = false;
    this.setState({ isUploadProgressDialogShow: false, uploadFileList: [], forbidUploadFileList: [], retryFileList: [], totalProgress: 0, uploadBitrate: 0, isUploadRemindDialogShow: false, currentResumableFile: null, duplicateBatchActive: false });
  };

  onUploadCancel = (uploadingItem) => {
    // Drop it from the file-level queue if it was only waiting (never started); if it
    // is the active block file, entry.cancel() below aborts it and the queue advances.
    this.removeQueuedBlockUpload(uploadingItem);
    // Release its destination key and drop it from the scheduler hold list (if held).
    this.activeUploadNameKeys.delete(this.getUploadDestinationKey(uploadingItem));
    this.legacyHold = this.legacyHold.filter(h => h.resumableFile.uniqueIdentifier !== uploadingItem.uniqueIdentifier);

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
    // Reset the scheduler FIRST so a resumable 'cancel' fired by item.cancel() below
    // (→ onCancel → onLegacyQueueIdle) cannot start a held mode group mid-cancel.
    this._resettingUploads = true;
    this.legacyHold = [];
    this.activeLegacyMode = null;

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
    this.clearBlockUploadQueue();
    if (this.blockLimiter) {
      this.blockLimiter.reset();
    }
    // Release the synchronous duplicate guard for every cancelled entry (saved entries
    // stay caught by fileNameExistsInDir).
    this.activeUploadNameKeys.clear();

    this.setState({
      totalProgress: 100,
      uploadFileList: uploadFileList,
      uploadBitrate: this.calculateUploadBitrate(uploadFileList),
    });
    resetAdaptiveUploadConcurrency(this.resumable, getBaselineSimultaneousUploads(this.props.simultaneousUploads || resumableSimultaneousUploads));
    this.restoreConcurrencyIfIdle();
    // reset upload link loaded + cached target promises
    this.isUploadLinkLoaded = false;
    this._uploadTargetPromise = null;
    this._replaceUpdateLinkPromise = null;
    this._resettingUploads = false;
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
        this.enqueueBlockUpload(item, item.file);
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
      this.enqueueBlockUpload(entry, entry.file);
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

  // Replace dialog actions. Each resolves the currently-prompted duplicate and, if
  // "apply to all" was checked, the rest of the held queue with the same choice.
  //   Replace       → overwrite the existing file (update link + replace flag / block replace)
  //   Don't replace → upload anyway (backend auto-renames to "name (1).ext")
  //   Cancel        → skip this duplicate (drop the held file)
  replaceRepetitionFile = (applyToAll = false) => {
    this.resolveDuplicate('replace', applyToAll);
  };

  uploadFile = (applyToAll = false) => {
    this.resolveDuplicate('keep', applyToAll);
  };

  cancelFileUpload = (applyToAll = false) => {
    this.resolveDuplicate('cancel', applyToAll);
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
            // key by file so the dialog remounts per duplicate — a checked
            // "apply to all" never leaks into the next prompt.
            key={this.state.currentResumableFile && this.state.currentResumableFile.uniqueIdentifier}
            currentResumableFile={this.state.currentResumableFile}
            replaceRepetitionFile={this.replaceRepetitionFile}
            uploadFile={this.uploadFile}
            cancelFileUpload={this.cancelFileUpload}
            showApplyToAll={this.state.duplicateBatchActive}
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