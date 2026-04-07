const path = require('path');

function getVendorPublicAssetMappings(appPath) {
    return [
        {
            route: '/static/locales',
            source: path.join(appPath, 'node_modules', '@seafile', 'seafile-editor', 'public', 'locales'),
        },
        {
            route: '/static/media',
            source: path.join(appPath, 'node_modules', '@seafile', 'seafile-editor', 'public', 'media'),
        },
        {
            route: '/static/sdoc-editor/locales',
            source: path.join(appPath, 'node_modules', '@seafile', 'sdoc-editor', 'public', 'locales'),
        },
        {
            route: '/static/sdoc-editor/media',
            source: path.join(appPath, 'node_modules', '@seafile', 'sdoc-editor', 'public', 'media'),
        },
    ];
}

module.exports = {
    getVendorPublicAssetMappings,
};