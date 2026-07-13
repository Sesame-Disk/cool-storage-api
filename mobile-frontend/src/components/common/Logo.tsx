import React from 'react';
import { siteRoot, mediaUrl, logoPath, logoHeight, siteTitle } from '../../lib/config';

interface LogoProps {
  /**
   * Pixel height for the logo image. Defaults to the configured `logoHeight`,
   * matching the web frontend's <Logo>; the app bar passes a smaller value.
   */
  height?: number;
  /** Extra classes for the anchor wrapper (e.g. spacing on the login screen). */
  className?: string;
}

/**
 * Brand logo, ported from the web frontend's config-driven mechanism
 * (`frontend/src/components/logo.js`) so the PWA resolves and renders the logo
 * identically: `mediaUrl + logoPath` (or an already-absolute `image-view` path),
 * sized by `logoHeight`, titled with `siteTitle`, linking to `siteRoot`.
 */
export default function Logo({ height, className }: LogoProps) {
  const path = logoPath();
  // Mirror the web frontend: an "image-view" logoPath is already an absolute
  // URL; every other value is relative to mediaUrl.
  const src = path.indexOf('image-view') !== -1 ? path : `${mediaUrl()}${path}`;
  const title = siteTitle();

  return (
    <a href={siteRoot()} id="logo" aria-label={title || 'Home'} className={className}>
      <img
        src={src}
        height={height ?? logoHeight()}
        style={{ width: 'auto', height: height ?? logoHeight() }}
        title={title}
        alt={title || 'logo'}
        data-testid="app-logo"
      />
    </a>
  );
}
