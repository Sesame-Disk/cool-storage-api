const { shouldProxyRepoPath } = require('./setupProxy');

describe('shouldProxyRepoPath', () => {
    test('proxies backend file view routes', () => {
        expect(shouldProxyRepoPath('/repo/123/raw/path/to/file.png')).toBe(true);
        expect(shouldProxyRepoPath('/repo/123/history/download?obj_id=abc')).toBe(true);
        expect(shouldProxyRepoPath('/repo/123/history/view?obj_id=abc')).toBe(true);
        expect(shouldProxyRepoPath('/repo/123/history/raw?obj_id=abc')).toBe(true);
        expect(shouldProxyRepoPath('/repo/123/history/download?obj_id=abc#preview')).toBe(true);
    });

    test('does not proxy SPA repo pages', () => {
        expect(shouldProxyRepoPath('/repo/123/trash/')).toBe(false);
        expect(shouldProxyRepoPath('/repo/123/snapshot/')).toBe(false);
        expect(shouldProxyRepoPath('/repo/history/123/')).toBe(false);
        expect(shouldProxyRepoPath('/repo/file_revisions/123/')).toBe(false);
    });
});