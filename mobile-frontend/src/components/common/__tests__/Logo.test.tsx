import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

// Control the config getters so we can exercise both resolution branches.
const cfg = {
  siteRoot: '/',
  mediaUrl: '/static/',
  logoPath: 'img/logo.png',
  logoHeight: 64,
  siteTitle: 'Sesame Disk',
};
vi.mock('../../../lib/config', () => ({
  siteRoot: () => cfg.siteRoot,
  mediaUrl: () => cfg.mediaUrl,
  logoPath: () => cfg.logoPath,
  logoHeight: () => cfg.logoHeight,
  siteTitle: () => cfg.siteTitle,
}));

import Logo from '../Logo';

describe('Logo', () => {
  beforeEach(() => {
    cfg.siteRoot = '/';
    cfg.mediaUrl = '/static/';
    cfg.logoPath = 'img/logo.png';
    cfg.logoHeight = 64;
    cfg.siteTitle = 'Sesame Disk';
  });

  it('resolves the logo as mediaUrl + logoPath (web parity)', () => {
    render(<Logo />);
    const img = screen.getByTestId('app-logo');
    expect(img).toHaveAttribute('src', '/static/img/logo.png');
    expect(img).toHaveAttribute('alt', 'Sesame Disk');
    expect(img).toHaveAttribute('title', 'Sesame Disk');
  });

  it('links to siteRoot and labels the brand with siteTitle', () => {
    render(<Logo />);
    const link = screen.getByRole('link', { name: 'Sesame Disk' });
    expect(link).toHaveAttribute('href', '/');
  });

  it('uses the configured logoHeight by default and honours a height override', () => {
    const { rerender } = render(<Logo />);
    expect(screen.getByTestId('app-logo')).toHaveAttribute('height', '64');
    rerender(<Logo height={28} />);
    expect(screen.getByTestId('app-logo')).toHaveAttribute('height', '28');
  });

  it('treats an image-view logoPath as an already-absolute URL (no mediaUrl prefix)', () => {
    cfg.logoPath = '/image-view/custom-logo.png';
    render(<Logo />);
    expect(screen.getByTestId('app-logo')).toHaveAttribute('src', '/image-view/custom-logo.png');
  });
});
