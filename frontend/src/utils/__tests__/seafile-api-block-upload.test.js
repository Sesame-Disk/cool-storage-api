// finding 9 (docs/WEB-BLOCK-UPLOAD.md): the web block-upload session id must
// travel via the X-Block-Upload-Session header, not the ?session= query
// string, so it does not land in access/proxy logs.
//
// seafile-api.js imports seafile-js, which imports axios as an ESM module that
// this project's Jest/Babel config cannot transform from node_modules (every
// other real-import test either mocks seafile-api entirely or, like this one,
// inspects the source directly) — see block-upload-orchestrator.test.js's
// `jest.mock('../../../utils/seafile-api', ...)` for the same constraint.

const fs = require('fs');
const path = require('path');

const getSeafileApiContent = () => {
  const filePath = path.join(__dirname, '..', 'seafile-api.js');
  return fs.readFileSync(filePath, 'utf8');
};

describe('seafileAPI block-upload session id transport', () => {
  let apiContent;

  beforeAll(() => {
    apiContent = getSeafileApiContent();
  });

  test('checkBlocks and uploadBlock no longer build a ?session= query string', () => {
    const checkBlocksBody = apiContent.match(/seafileAPI\.checkBlocks = function[\s\S]*?\n};/)[0];
    const uploadBlockBody = apiContent.match(/seafileAPI\.uploadBlock = function[\s\S]*?\n};/)[0];

    expect(checkBlocksBody).not.toMatch(/session=/);
    expect(uploadBlockBody).not.toMatch(/session=/);
  });

  test('checkBlocks and uploadBlock route the session through the header helper', () => {
    const checkBlocksBody = apiContent.match(/seafileAPI\.checkBlocks = function[\s\S]*?\n};/)[0];
    const uploadBlockBody = apiContent.match(/seafileAPI\.uploadBlock = function[\s\S]*?\n};/)[0];

    expect(checkBlocksBody).toMatch(/withBlockUploadSessionHeader\([\s\S]*session\)/);
    expect(uploadBlockBody).toMatch(/withBlockUploadSessionHeader\([\s\S]*session\)/);
  });

  test('block-upload requests opt into propagated 401 handling', () => {
    expect(apiContent).toMatch(/function withBlockUploadAuthHandling\(config\)/);

    const createSessionBody = apiContent.match(/seafileAPI\.createBlockUploadSession = function[\s\S]*?\n};/)[0];
    const checkBlocksBody = apiContent.match(/seafileAPI\.checkBlocks = function[\s\S]*?\n};/)[0];
    const uploadBlockBody = apiContent.match(/seafileAPI\.uploadBlock = function[\s\S]*?\n};/)[0];
    const commitBody = apiContent.match(/seafileAPI\.createFileFromBlocks = function[\s\S]*?\n};/)[0];
    const interceptorBody = apiContent.match(/function setupResponseInterceptor\(\)[\s\S]*?\n}/)[0];

    expect(createSessionBody).toMatch(/withBlockUploadAuthHandling\(config\)/);
    expect(checkBlocksBody).toMatch(/withBlockUploadAuthHandling\(config\)/);
    expect(uploadBlockBody).toMatch(/withBlockUploadAuthHandling\(config\)/);
    expect(commitBody).toMatch(/withBlockUploadAuthHandling\(config\)/);
    expect(interceptorBody).toMatch(/error\.config && error\.config\._propagate401/);
    expect(interceptorBody).toMatch(/return Promise\.reject\(error\);/);
  });

  test('withBlockUploadSessionHeader sets X-Block-Upload-Session and never mutates the caller config', () => {
    expect(apiContent).toMatch(/function withBlockUploadSessionHeader\(config, session\)/);
    const helperBody = apiContent.match(/function withBlockUploadSessionHeader[\s\S]*?\n}/)[0];

    expect(helperBody).toMatch(/'X-Block-Upload-Session':\s*session/);
    // Object.assign({}, ...) into a NEW object, never `config.headers[...] = ...`.
    expect(helperBody).toMatch(/Object\.assign\(\{\}, requestConfig/);
    expect(helperBody).not.toMatch(/requestConfig\.headers\[/);
  });

  test('withBlockUploadSessionHeader throws on a missing session instead of dropping the header', () => {
    // A falsy session must fail loudly, not return a header-less config. The
    // server removed the no-session path (400 block_upload_session_required), so
    // silently dropping the header would fire a doomed request whose bytes leak as
    // an S3 orphan and whose commit is later refused — with no error surfaced.
    const helperBody = apiContent.match(/function withBlockUploadSessionHeader[\s\S]*?\n}/)[0];
    expect(helperBody).toMatch(/if\s*\(!session\)\s*\{[\s\S]*?throw /);
    expect(helperBody).not.toMatch(/if\s*\(!session\)\s*\{[\s\S]*?return requestConfig/);
  });
});
