import { rewriteSharedMarkdownNode, shareLinkMarkdownUrlHelpers } from '../share-link-markdown-url';

describe('share-link markdown URL helpers', () => {
    const context = {
        repoID: 'repo-1',
        sharedToken: 'share-token',
        currentFilePath: '/docs/guides/readme.md',
    };

    test('resolves relative share paths from the current markdown file', () => {
        expect(shareLinkMarkdownUrlHelpers.resolveShareRelativePath('/docs/guides/readme.md', '../images/logo.png'))
            .toBe('/docs/images/logo.png');
    });

    test('rewrites relative markdown links to shared file routes', () => {
        const node = { type: 'link', url: '../other.md#intro' };

        rewriteSharedMarkdownNode(node, context);

        expect(node.url).toBe('/d/share-token/files/?p=%2Fdocs%2Fother.md#intro');
    });

    test('rewrites internal repo links to shared file routes', () => {
        const node = { type: 'link', url: 'http://localhost:8000/lib/repo-1/file/docs/manual.pdf' };

        rewriteSharedMarkdownNode(node, context);

        expect(node.url).toBe('/d/share-token/files/?p=%2Fdocs%2Fmanual.pdf');
    });

    test('rewrites same-origin absolute repo links to shared file routes', () => {
        const node = { type: 'link', url: `${window.location.origin}/lib/repo-1/file/docs/video.mov` };

        rewriteSharedMarkdownNode(node, context);

        expect(node.url).toBe('/d/share-token/files/?p=%2Fdocs%2Fvideo.mov');
    });

    test('rewrites root-relative internal directory links to shared dir routes', () => {
        const node = { type: 'link', url: '/library/repo-1/My%20Library/docs/guides/' };

        rewriteSharedMarkdownNode(node, context);

        expect(node.url).toBe('/d/share-token/?p=%2Fdocs%2Fguides');
    });

    test('rewrites smart links using the bootstrap smart-link map', () => {
        const node = { type: 'link', url: `${window.location.origin}/smart-link/xWNtuPDwlOSDkiIl-IVzNA` };

        rewriteSharedMarkdownNode(node, {
            ...context,
            smartLinkMap: {
                'xWNtuPDwlOSDkiIl-IVzNA': {
                    path: '/docs/internal-target.md',
                    isDir: false,
                },
            },
        });

        expect(node.url).toBe('/d/share-token/files/?p=%2Fdocs%2Finternal-target.md');
    });

    test('rewrites smart links for directories using the bootstrap smart-link map', () => {
        const node = { type: 'link', url: `${window.location.origin}/smart-link/xWNtuPDwlOSDkiIl-IVzNA` };

        rewriteSharedMarkdownNode(node, {
            ...context,
            smartLinkMap: {
                'xWNtuPDwlOSDkiIl-IVzNA': {
                    path: '/pepep',
                    isDir: true,
                },
            },
        });

        expect(node.url).toBe('/d/share-token/?p=%2Fpepep');
    });

    test('rewrites relative images to the share image proxy', () => {
        const node = { type: 'image', data: { src: './images/logo.png' } };

        rewriteSharedMarkdownNode(node, context);

        expect(node.data.src).toBe('http://localhost:8000/view-image-via-share-link/?token=share-token&path=%2Fdocs%2Fguides%2Fimages%2Flogo.png');
    });

    test('leaves anchors untouched', () => {
        const node = { type: 'link', url: '#section-1' };

        rewriteSharedMarkdownNode(node, context);

        expect(node.url).toBe('#section-1');
    });
});