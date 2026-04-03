const paths = require('./paths');

const entryFiles = {
  markdownEditor: '/index.js',
  plainMarkdownEditor: '/pages/plain-markdown-editor/index.js',
  TCAccept: '/tc-accept.js',
  TCView: '/tc-view.js',
  wiki: '/wiki.js',
  fileHistory: '/file-history.js',
  fileHistoryOld: '/file-history-old.js',
  sdocFileHistory: '/pages/sdoc/sdoc-file-history/index.js',
  sdocPublishedRevision: '/pages/sdoc/sdoc-published-revision/index.js',
  app: '/app-entry.js',
  publicSharePage: '/public-share-page.js',
  publicUploadPage: '/public-upload-link-page.js',
  draft: '/draft.js',
  sharedDirView: '/shared-dir-view-entry.js',
  sharedFileViewMarkdown: '/shared-file-view-markdown-entry.js',
  sharedFileViewText: '/shared-file-view-text-entry.js',
  sharedFileViewImage: '/shared-file-view-image-entry.js',
  sharedFileViewVideo: '/shared-file-view-video-entry.js',
  sharedFileViewPDF: '/shared-file-view-pdf-entry.js',
  sharedFileViewSVG: '/shared-file-view-svg-entry.js',
  sharedFileViewAudio: '/shared-file-view-audio-entry.js',
  sharedFileViewDocument: '/shared-file-view-document-entry.js',
  sharedFileViewSpreadsheet: '/shared-file-view-spreadsheet-entry.js',
  sharedFileViewSdoc: '/shared-file-view-sdoc-entry.js',
  sharedFileViewUnknown: '/shared-file-view-unknown-entry.js',
  settings: '/settings.js',
  orgAdmin: '/pages/org-admin/bootstrap-entry.js',
  sysAdmin: '/pages/sys-admin/bootstrap-entry.js',
  search: '/pages/search',
  uploadLink: '/pages/upload-link/bootstrap-entry.js',
  institutionAdmin: '/pages/institution-admin/index.js'
};

const getEntries = (isEnvDevelopment) => {
  let entries = {};
  Object.keys(entryFiles).forEach(key => {
    let entry = [];
    if (isEnvDevelopment) {
      entry.push(require.resolve('react-dev-utils/webpackHotDevClient'));
    }
    entry.push(paths.appSrc + entryFiles[key]);

    entries[key] = entry;
  });
  return entries;
};

module.exports = getEntries;
