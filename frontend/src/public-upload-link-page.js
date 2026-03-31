import { loadUploadLinkBootstrap, renderPublicBootstrapError } from './bootstrap/share-runtime-bootstrap';

import './services/css.css';

loadUploadLinkBootstrap()
    .then((data) => {
        if (data?.bootstrapError) {
            renderPublicBootstrapError(data.message, 'Unable to load upload link');
            return;
        }

        if (data?.title) {
            document.title = data.title;
        }

        return import(/* webpackChunkName: "uploadLinkPage" */ './pages/upload-link');
    })
    .catch((error) => {
        renderPublicBootstrapError(error?.message || 'Failed to initialize the upload link page.', 'Unable to load upload link');
    });