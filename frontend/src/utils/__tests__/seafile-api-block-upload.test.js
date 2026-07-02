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

    expect(checkBlocksBody).toMatch(/withBlockUploadSessionHeader\(config, session\)/);
    expect(uploadBlockBody).toMatch(/withBlockUploadSessionHeader\(config, session\)/);
  });

  test('withBlockUploadSessionHeader sets X-Block-Upload-Session and never mutates the caller config', () => {
    expect(apiContent).toMatch(/function withBlockUploadSessionHeader\(config, session\)/);
    const helperBody = apiContent.match(/function withBlockUploadSessionHeader[\s\S]*?\n}/)[0];

    expect(helperBody).toMatch(/'X-Block-Upload-Session':\s*session/);
    // Object.assign({}, ...) into a NEW object, never `config.headers[...] = ...`.
    expect(helperBody).toMatch(/Object\.assign\(\{\}, requestConfig/);
    expect(helperBody).not.toMatch(/requestConfig\.headers\[/);
  });
});
