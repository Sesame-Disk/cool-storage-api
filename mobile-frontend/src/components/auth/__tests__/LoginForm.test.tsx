import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import LoginForm from '../LoginForm';

vi.mock('../../../lib/api', () => ({
  login: vi.fn(),
  localLogin: vi.fn(),
  // Default: advertise OIDC (keeps the SSO button rendered) but not local, so
  // the legacy dev/password login() path these tests assert stays in effect.
  getAuthMethods: vi.fn(() => Promise.resolve({ local: false, oidc: true })),
}));

vi.mock('../../../lib/oidc', () => ({
  getOIDCLoginURL: vi.fn(),
  isDevBypass: vi.fn(() => false),
}));

vi.mock('../../../lib/config', () => ({
  getConfig: () => ({
    siteTitle: 'Test App',
    mediaUrl: '/static/',
    logoPath: 'img/logo.png',
  }),
  // Getter exports the <Logo> component reads directly.
  siteRoot: () => '/',
  mediaUrl: () => '/static/',
  logoPath: () => 'img/logo.png',
  logoHeight: () => 64,
  siteTitle: () => 'Test App',
}));

import { login } from '../../../lib/api';
import { getOIDCLoginURL, isDevBypass } from '../../../lib/oidc';

const mockLogin = vi.mocked(login);
const mockGetOIDCLoginURL = vi.mocked(getOIDCLoginURL);
const mockIsDevBypass = vi.mocked(isDevBypass);

describe('LoginForm', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockIsDevBypass.mockReturnValue(false);
    Object.defineProperty(window, 'location', {
      value: { href: '', search: '' },
      writable: true,
    });
  });

  it('renders email and password inputs', () => {
    render(<LoginForm />);
    expect(screen.getByPlaceholderText('Email')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Password')).toBeInTheDocument();
  });

  it('renders Log In and SSO buttons', () => {
    render(<LoginForm />);
    expect(screen.getByRole('button', { name: 'Log In' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign in with SSO' })).toBeInTheDocument();
  });

  it('shows error on failed login', async () => {
    mockLogin.mockRejectedValue(new Error('Invalid credentials'));
    render(<LoginForm />);

    fireEvent.change(screen.getByPlaceholderText('Email'), {
      target: { value: 'test@test.com' },
    });
    fireEvent.change(screen.getByPlaceholderText('Password'), {
      target: { value: 'wrong' },
    });
    fireEvent.submit(screen.getByRole('button', { name: /log in/i }));

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Invalid credentials');
    });
  });

  it('calls login() on form submit', async () => {
    mockLogin.mockResolvedValue('token123');
    render(<LoginForm />);

    fireEvent.change(screen.getByPlaceholderText('Email'), {
      target: { value: 'user@test.com' },
    });
    fireEvent.change(screen.getByPlaceholderText('Password'), {
      target: { value: 'pass123' },
    });
    fireEvent.submit(screen.getByRole('button', { name: /log in/i }));

    await waitFor(() => {
      expect(mockLogin).toHaveBeenCalledWith('user@test.com', 'pass123');
    });
  });

  it('SSO button calls getOIDCLoginURL', async () => {
    mockGetOIDCLoginURL.mockResolvedValue('https://sso.example.com/auth');
    render(<LoginForm />);

    fireEvent.click(screen.getByRole('button', { name: 'Sign in with SSO' }));

    await waitFor(() => {
      expect(mockGetOIDCLoginURL).toHaveBeenCalled();
    });
  });

  it('shows expired message when ?expired=1', () => {
    Object.defineProperty(window, 'location', {
      value: { href: '', search: '?expired=1' },
      writable: true,
    });
    render(<LoginForm />);
    expect(screen.getByText('Session expired, please log in again')).toBeInTheDocument();
  });

  it('auto-redirects when dev bypass is enabled', () => {
    mockIsDevBypass.mockReturnValue(true);
    render(<LoginForm />);
    expect(window.location.href).toBe('/libraries/');
  });

  it('toggles password visibility', () => {
    render(<LoginForm />);
    const passwordInput = screen.getByPlaceholderText('Password');
    expect(passwordInput).toHaveAttribute('type', 'password');

    fireEvent.click(screen.getByText('Show'));
    expect(passwordInput).toHaveAttribute('type', 'text');

    fireEvent.click(screen.getByText('Hide'));
    expect(passwordInput).toHaveAttribute('type', 'password');
  });
});
