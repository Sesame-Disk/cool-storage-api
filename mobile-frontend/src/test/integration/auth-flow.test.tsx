import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import LoginForm from '../../components/auth/LoginForm';

// Mock the API and OIDC modules
const mockLogin = vi.fn().mockResolvedValue('mock-token-123');
const mockIsDevBypass = vi.fn().mockReturnValue(false);
const mockGetOIDCLoginURL = vi.fn().mockResolvedValue('http://sso.example.com/authorize');
const mockClearAuthToken = vi.fn();

vi.mock('../../lib/api', () => ({
  login: (...args: unknown[]) => mockLogin(...args),
  localLogin: (...args: unknown[]) => mockLogin(...args),
  getAuthMethods: vi.fn().mockResolvedValue({ local: false, oidc: true }),
  getAuthToken: vi.fn().mockReturnValue('mock-token'),
  setAuthToken: vi.fn(),
  clearAuthToken: (...args: unknown[]) => mockClearAuthToken(...args),
}));

vi.mock('../../lib/oidc', () => ({
  isDevBypass: () => mockIsDevBypass(),
  getOIDCLoginURL: () => mockGetOIDCLoginURL(),
  setAuthToken: vi.fn(),
}));

vi.mock('../../lib/config', () => ({
  getConfig: () => ({
    siteRoot: '/',
    loginUrl: '/login/',
    serviceURL: 'http://localhost:8080',
    mediaUrl: '/static/',
    siteTitle: 'Sesame Disk',
    siteName: 'Sesame Disk',
    logoPath: 'img/logo.png',
    logoWidth: 147,
    logoHeight: 64,
  }),
  getPageOptions: () => ({
    name: 'Dev User',
    username: 'dev@sesamefs.local',
  }),
  serviceURL: () => 'http://localhost:8080',
  // Getter exports the <Logo> component (rendered inside LoginForm) reads.
  siteRoot: () => '/',
  mediaUrl: () => '/static/',
  logoPath: () => 'img/logo.png',
  logoHeight: () => 64,
  siteTitle: () => 'Sesame Disk',
}));

describe('Auth Flow', () => {
  beforeEach(() => {
    mockLogin.mockClear().mockResolvedValue('mock-token-123');
    mockIsDevBypass.mockClear().mockReturnValue(false);
    mockGetOIDCLoginURL.mockClear().mockResolvedValue('http://sso.example.com/authorize');
    mockClearAuthToken.mockClear();
    // Reset window.location
    Object.defineProperty(window, 'location', {
      value: { href: 'http://localhost:3000/login/', search: '', pathname: '/login/' },
      writable: true,
    });
  });

  it('renders login form with email and password fields', () => {
    render(<LoginForm />);

    expect(screen.getByPlaceholderText('Email')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Password')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /log in/i })).toBeInTheDocument();
  });

  it('submits login form with credentials', async () => {
    render(<LoginForm />);

    const emailInput = screen.getByPlaceholderText('Email');
    const passwordInput = screen.getByPlaceholderText('Password');
    const submitBtn = screen.getByRole('button', { name: /log in/i });

    fireEvent.change(emailInput, { target: { value: 'user@example.com' } });
    fireEvent.change(passwordInput, { target: { value: 'password123' } });
    fireEvent.click(submitBtn);

    await waitFor(() => {
      expect(mockLogin).toHaveBeenCalledWith('user@example.com', 'password123');
    });
  });

  it('shows error on login failure', async () => {
    mockLogin.mockRejectedValueOnce(new Error('Invalid credentials'));

    render(<LoginForm />);

    fireEvent.change(screen.getByPlaceholderText('Email'), { target: { value: 'bad@example.com' } });
    fireEvent.change(screen.getByPlaceholderText('Password'), { target: { value: 'wrong' } });
    fireEvent.click(screen.getByRole('button', { name: /log in/i }));

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Invalid credentials');
    });
  });

  it('dev bypass mode skips login and redirects to libraries', () => {
    mockIsDevBypass.mockReturnValue(true);

    const { container } = render(<LoginForm />);

    // When dev bypass is active, LoginForm renders null
    expect(container.innerHTML).toBe('');
  });

  it('SSO button triggers redirect', async () => {
    render(<LoginForm />);

    const ssoButton = screen.getByRole('button', { name: /sign in with sso/i });
    fireEvent.click(ssoButton);

    await waitFor(() => {
      expect(mockGetOIDCLoginURL).toHaveBeenCalled();
    });
  });

  it('shows expired session message when expired=1', () => {
    Object.defineProperty(window, 'location', {
      value: { href: 'http://localhost:3000/login/?expired=1', search: '?expired=1', pathname: '/login/' },
      writable: true,
    });

    render(<LoginForm />);

    expect(screen.getByText(/session expired/i)).toBeInTheDocument();
  });

  it('shows SSO error message when error=sso_failed', () => {
    Object.defineProperty(window, 'location', {
      value: { href: 'http://localhost:3000/login/?error=sso_failed', search: '?error=sso_failed', pathname: '/login/' },
      writable: true,
    });

    render(<LoginForm />);

    expect(screen.getByText(/sso login failed/i)).toBeInTheDocument();
  });

  it('toggles password visibility', () => {
    render(<LoginForm />);

    const passwordInput = screen.getByPlaceholderText('Password');
    expect(passwordInput).toHaveAttribute('type', 'password');

    const toggleBtn = screen.getByRole('button', { name: /show/i });
    fireEvent.click(toggleBtn);

    expect(passwordInput).toHaveAttribute('type', 'text');
  });

  it('disables form while logging in', async () => {
    // Create a promise that we control
    let resolveLogin: (value: string) => void;
    mockLogin.mockImplementation(() => new Promise(resolve => { resolveLogin = resolve; }));

    render(<LoginForm />);

    fireEvent.change(screen.getByPlaceholderText('Email'), { target: { value: 'user@example.com' } });
    fireEvent.change(screen.getByPlaceholderText('Password'), { target: { value: 'pass' } });
    fireEvent.click(screen.getByRole('button', { name: /log in/i }));

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /logging in/i })).toBeDisabled();
    });

    // Resolve to clean up
    resolveLogin!('token');
  });
});
