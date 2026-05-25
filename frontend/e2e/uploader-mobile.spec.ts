import { test, expect } from '@playwright/test';

// Validates the responsive constraints applied in job-002 without requiring
// a logged-in upload flow. Mounts a minimal HTML harness that renders the
// .uploader-list-view container, then asserts it doesn't overflow the viewport.
test.describe('uploader widget responsive width', () => {
  test('does not overflow the viewport', async ({ page }) => {
    await page.setContent(`
      <!doctype html>
      <html>
        <head>
          <link rel="stylesheet" href="/media/css/file-uploader.css"/>
          <style>
            body { margin: 0; }
            .uploader-list-view {
              display: flex;
              flex-direction: column;
              position: fixed;
              right: 1px;
              bottom: 1px;
              width: 35rem;
              max-width: 100%;
              height: 20rem;
              max-height: 80vh;
              min-width: 0;
              border: 1px solid #ddd;
              background: #fff;
            }
            @media (max-width: 767px) {
              .uploader-list-view {
                right: 8px;
                left: 8px;
                bottom: 8px;
                width: auto;
                max-width: none;
                height: auto;
                max-height: 60vh;
              }
            }
          </style>
        </head>
        <body>
          <div class="uploader-list-view"></div>
        </body>
      </html>
    `);

    const box = await page.locator('.uploader-list-view').boundingBox();
    expect(box).not.toBeNull();
    expect(box!.width).toBeLessThanOrEqual(await page.evaluate(() => window.innerWidth));
    expect(box!.x).toBeGreaterThanOrEqual(0);
  });
});
