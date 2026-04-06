import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import NewLibrarySheet from '../NewLibrarySheet';

const mockCreateRepo = vi.fn();

vi.mock('../../../lib/api', () => ({
  createRepo: (...args: unknown[]) => mockCreateRepo(...args),
}));

vi.mock('../../../lib/config', () => ({
  getPageOptions: () => ({
    storages: [
      { id: 'hot-usa', name: 'USA' },
      { id: 'hot-eu', name: 'EU', is_default: true },
    ],
  }),
}));

describe('NewLibrarySheet', () => {
  const onClose = vi.fn();
  const onCreated = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders form fields when open', () => {
    render(<NewLibrarySheet isOpen={true} onClose={onClose} onCreated={onCreated} />);
    expect(screen.getByLabelText('Name')).toBeInTheDocument();
    expect(screen.getByLabelText('Region')).toBeInTheDocument();
    expect(screen.getByText('Encrypt this library')).toBeInTheDocument();
    expect(screen.getByText('Create Library')).toBeInTheDocument();
  });

  it('shows error when name is empty', async () => {
    render(<NewLibrarySheet isOpen={true} onClose={onClose} onCreated={onCreated} />);
    fireEvent.click(screen.getByText('Create Library'));
    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Library name is required');
    });
  });

  it('shows password fields when encrypted is checked', () => {
    render(<NewLibrarySheet isOpen={true} onClose={onClose} onCreated={onCreated} />);
    fireEvent.click(screen.getByText('Encrypt this library'));
    expect(screen.getByLabelText('Password')).toBeInTheDocument();
    expect(screen.getByLabelText('Confirm Password')).toBeInTheDocument();
  });

  it('calls createRepo and onCreated on success', async () => {
    mockCreateRepo.mockResolvedValue({ repo_id: 'new-repo' });
    render(<NewLibrarySheet isOpen={true} onClose={onClose} onCreated={onCreated} />);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Test Library' } });
    fireEvent.click(screen.getByText('Create Library'));
    await waitFor(() => {
      expect(mockCreateRepo).toHaveBeenCalledWith('Test Library', false, undefined, 'hot-eu');
    });
    expect(onCreated).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });

  it('shows error when passwords do not match', async () => {
    render(<NewLibrarySheet isOpen={true} onClose={onClose} onCreated={onCreated} />);
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Encrypted Lib' } });
    fireEvent.click(screen.getByText('Encrypt this library'));
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'pass1' } });
    fireEvent.change(screen.getByLabelText('Confirm Password'), { target: { value: 'pass2' } });
    fireEvent.click(screen.getByText('Create Library'));
    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Passwords do not match');
    });
  });
});
