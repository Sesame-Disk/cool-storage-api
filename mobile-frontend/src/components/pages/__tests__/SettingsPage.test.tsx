import React from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import SettingsPage from '../SettingsPage';

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
const mockUpdateAccountInfo = vi.fn();

vi.mock('../../../lib/api', () => ({
  getAccountInfo: (...args: unknown[]) => mockGetAccountInfo(...args),
  updateAccountInfo: (...args: unknown[]) => mockUpdateAccountInfo(...args),
}));

describe('SettingsPage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetAccountInfo.mockResolvedValue(mockAccountInfo);
  });

  it('renders the settings page with the current name', async () => {
    render(<SettingsPage />);
    await waitFor(() => {
      expect(screen.getByTestId('settings-page')).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.getByTestId('settings-name-input')).toHaveValue('Test User');
    });
    expect(screen.getByTestId('settings-email')).toHaveTextContent('user@example.com');
  });

  it('renders the storage usage bar', async () => {
    render(<SettingsPage />);
    await waitFor(() => {
      expect(screen.getByTestId('storage-usage-bar')).toBeInTheDocument();
    });
  });

  it('disables save until the name changes', async () => {
    render(<SettingsPage />);
    await waitFor(() => {
      expect(screen.getByTestId('settings-name-input')).toHaveValue('Test User');
    });
    expect(screen.getByTestId('settings-name-save')).toBeDisabled();

    fireEvent.change(screen.getByTestId('settings-name-input'), {
      target: { value: 'New Name' },
    });
    expect(screen.getByTestId('settings-name-save')).not.toBeDisabled();
  });

  it('saves the new display name', async () => {
    mockUpdateAccountInfo.mockResolvedValue({ ...mockAccountInfo, name: 'New Name' });
    render(<SettingsPage />);
    await waitFor(() => {
      expect(screen.getByTestId('settings-name-input')).toHaveValue('Test User');
    });

    fireEvent.change(screen.getByTestId('settings-name-input'), {
      target: { value: 'New Name' },
    });
    fireEvent.click(screen.getByTestId('settings-name-save'));

    await waitFor(() => {
      expect(mockUpdateAccountInfo).toHaveBeenCalledWith({ name: 'New Name' });
    });
  });
});
