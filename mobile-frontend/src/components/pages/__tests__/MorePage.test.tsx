import React from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import MorePage from '../MorePage';

const mockAccountInfo = {
  usage: 524288000,
  total: 1073741824,
  email: 'user@example.com',
  name: 'Test User',
  login_id: 'user@example.com',
  institution: '',
  is_staff: false,
  avatar_url: '',
};

const mockGetAccountInfo = vi.fn();
const mockLogout = vi.fn();

vi.mock('../../../lib/api', () => ({
  getAccountInfo: (...args: unknown[]) => mockGetAccountInfo(...args),
  logout: (...args: unknown[]) => mockLogout(...args),
}));

vi.mock('../../../lib/hooks/useTheme', () => ({
  useTheme: () => ({ theme: 'system', setTheme: vi.fn(), isDark: false }),
}));

vi.mock('../../../lib/sortPreference', () => ({
  getSortPreference: () => ({ field: 'name', direction: 'asc' }),
  setSortPreference: vi.fn(),
}));

describe('MorePage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetAccountInfo.mockResolvedValue(mockAccountInfo);
  });

  it('renders settings title', async () => {
    render(<MorePage />);
    expect(screen.getByText('Settings')).toBeInTheDocument();
  });

  it('displays profile name and email', async () => {
    render(<MorePage />);
    await waitFor(() => {
      expect(screen.getByTestId('profile-name')).toHaveTextContent('Test User');
    });
    expect(screen.getByTestId('profile-email')).toHaveTextContent('user@example.com');
  });

  it('displays storage usage bar', async () => {
    render(<MorePage />);
    await waitFor(() => {
      expect(screen.getByTestId('storage-usage-bar')).toBeInTheDocument();
    });
  });

  it('shows navigation links', async () => {
    render(<MorePage />);
    expect(screen.getByText('Activity')).toBeInTheDocument();
    expect(screen.getByText('My Shares')).toBeInTheDocument();
    expect(screen.getByText('Sort Preference')).toBeInTheDocument();
  });

  it('shows feature links to Settings and Deleted Libraries', async () => {
    render(<MorePage />);
    const settings = screen.getByTestId('more-link-settings');
    expect(settings).toHaveAttribute('href', '/settings/');
    const deleted = screen.getByTestId('more-link-deleted-libraries');
    expect(deleted).toHaveAttribute('href', '/deleted-libraries/');
  });

  it('shows theme options', async () => {
    render(<MorePage />);
    expect(screen.getByText('Light')).toBeInTheDocument();
    expect(screen.getByText('Dark')).toBeInTheDocument();
    expect(screen.getByText('System')).toBeInTheDocument();
  });

  it('shows about section', async () => {
    render(<MorePage />);
    expect(screen.getByText('Version')).toBeInTheDocument();
    expect(screen.getByText('Help')).toBeInTheDocument();
  });

  it('shows logout button', async () => {
    render(<MorePage />);
    expect(screen.getByTestId('logout-button')).toBeInTheDocument();
  });

  it('calls logout on button click', async () => {
    mockLogout.mockResolvedValue(undefined);
    // Mock window.location
    const originalLocation = window.location;
    Object.defineProperty(window, 'location', {
      writable: true,
      value: { ...originalLocation, href: '' },
    });

    render(<MorePage />);
    fireEvent.click(screen.getByTestId('logout-button'));

    await waitFor(() => {
      expect(mockLogout).toHaveBeenCalled();
    });

    // Restore
    Object.defineProperty(window, 'location', {
      writable: true,
      value: originalLocation,
    });
  });
});
