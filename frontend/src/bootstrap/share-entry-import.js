import {
  loadShareBootstrap,
  loadUploadLinkBootstrap,
  renderPublicBootstrapError,
} from './share-runtime-bootstrap';

export function bootstrapShareModule(loader) {
  loadShareBootstrap()
    .then((data) => {
      if (data?.bootstrapError) {
        renderPublicBootstrapError(data.message);
        return;
      }
      return loader();
    })
    .catch(() => {
      renderPublicBootstrapError('Failed to initialize the public share page.');
    });
}

export function bootstrapUploadLinkModule(loader) {
  loadUploadLinkBootstrap()
    .then((data) => {
      if (data?.bootstrapError) {
        renderPublicBootstrapError(data.message);
        return;
      }
      return loader();
    })
    .catch(() => {
      renderPublicBootstrapError('Failed to initialize the public upload page.');
    });
}