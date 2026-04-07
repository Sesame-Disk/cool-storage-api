import {
    buildFileViewURL,
    buildFrontendFilePreviewURL,
    buildHistoricFileViewURL,
    isInlinePreviewableFile,
} from '../file-view-url';

describe('file-view-url helpers', () => {
    beforeEach(() => {
        window.app = {
            pageOptions: {
                inlinePreviewExtensions: ['md', 'mp4', 'png'],
            },
        };
    });

    afterEach(() => {
        delete window.app;
    });

    test('recognizes inline previewable files', () => {
        expect(isInlinePreviewableFile('/docs/readme.md')).toBe(true);
        expect(isInlinePreviewableFile('/media/demo.mp4')).toBe(true);
        expect(isInlinePreviewableFile('/office/report.docx')).toBe(false);
        expect(isInlinePreviewableFile('/archive/blob.bin')).toBe(false);
    });

    test('builds frontend preview URLs for previewable files', () => {
        expect(buildFileViewURL({ repoID: 'repo-1', filePath: '/docs/readme.md', token: 'secret' }))
            .toBe('/file-preview/?repo_id=repo-1&p=%2Fdocs%2Freadme.md');

        expect(buildFrontendFilePreviewURL({ repoID: 'repo-1', filePath: '/docs/readme.md', objID: 'obj-9' }))
            .toBe('/file-preview/?repo_id=repo-1&p=%2Fdocs%2Freadme.md&obj_id=obj-9');
    });

    test('falls back to legacy backend routes when preview is not frontend-owned yet', () => {
        expect(buildFileViewURL({ repoID: 'repo-1', filePath: '/office/report.docx', token: 'secret' }))
            .toBe('/lib/repo-1/file/office/report.docx?token=secret');

        expect(buildHistoricFileViewURL({ repoID: 'repo-1', filePath: '/office/report.docx', objID: 'obj-9', token: 'secret' }))
            .toBe('/repo/repo-1/history/view?obj_id=obj-9&p=%2Foffice%2Freport.docx&token=secret');
    });

    test('uses frontend preview shell for previewable historic files', () => {
        expect(buildHistoricFileViewURL({ repoID: 'repo-1', filePath: '/images/photo.png', objID: 'obj-9' }))
            .toBe('/file-preview/?repo_id=repo-1&p=%2Fimages%2Fphoto.png&obj_id=obj-9');
    });

    test('falls back to backend routes when bootstrap has not provided preview extensions yet', () => {
        delete window.app;

        expect(buildFileViewURL({ repoID: 'repo-1', filePath: '/docs/readme.md' }))
            .toBe('/lib/repo-1/file/docs/readme.md');
    });
});
