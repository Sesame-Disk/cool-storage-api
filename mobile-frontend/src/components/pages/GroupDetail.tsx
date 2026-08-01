import React, { useState, useEffect, useCallback } from 'react';
import { Users, BookOpen, ArrowLeft, Trash2, LogOut } from 'lucide-react';
import {
  listGroups,
  listGroupRepos,
  listGroupMembers,
  getAccountInfo,
  deleteGroup,
  removeGroupMember,
} from '../../lib/api';
import type { Group, GroupRepo, GroupMember } from '../../lib/api';
import GroupMemberManager from '../groups/GroupMemberManager';

interface GroupDetailProps {
  groupId?: string;
}

type Tab = 'libraries' | 'members';

export default function GroupDetail({ groupId }: GroupDetailProps) {
  const [group, setGroup] = useState<Group | null>(null);
  const [repos, setRepos] = useState<GroupRepo[]>([]);
  const [members, setMembers] = useState<GroupMember[]>([]);
  const [currentEmail, setCurrentEmail] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [activeTab, setActiveTab] = useState<Tab>('libraries');
  const [toast, setToast] = useState('');
  const [confirm, setConfirm] = useState<null | 'delete' | 'leave'>(null);
  const [working, setWorking] = useState(false);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 2500);
  }, []);

  const fetchData = useCallback(async () => {
    if (!groupId) return;
    try {
      const [groups, repoData, memberData, account] = await Promise.all([
        listGroups(),
        listGroupRepos(groupId),
        listGroupMembers(groupId),
        getAccountInfo().catch(() => null),
      ]);
      const found = groups.find((g) => String(g.id) === groupId);
      if (!found) {
        setError('Group not found');
        return;
      }
      setGroup(found);
      setRepos(repoData);
      setMembers(memberData);
      setCurrentEmail(account?.email ?? '');
      setError('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load group');
    }
  }, [groupId]);

  useEffect(() => {
    fetchData().finally(() => setLoading(false));
  }, [fetchData]);

  // The caller's role in this group governs which actions are available.
  const callerRole =
    members.find((m) => m.email === currentEmail)?.role?.toLowerCase() ??
    (group && group.owner === currentEmail ? 'owner' : '');
  const isOwner = callerRole === 'owner';
  const isMemberOfGroup = callerRole !== '';

  const handleDelete = async () => {
    if (!groupId) return;
    setWorking(true);
    try {
      await deleteGroup(groupId);
      window.location.href = '/groups/';
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to delete group');
      setWorking(false);
      setConfirm(null);
    }
  };

  const handleLeave = async () => {
    if (!groupId || !currentEmail) return;
    setWorking(true);
    try {
      await removeGroupMember(groupId, currentEmail);
      window.location.href = '/groups/';
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to leave group');
      setWorking(false);
      setConfirm(null);
    }
  };

  if (!groupId) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center">
        <Users className="w-12 h-12 text-gray-300 mb-4" />
        <p className="text-gray-500">No group selected</p>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="flex flex-col gap-4 p-4">
        <div className="animate-pulse">
          <div className="h-6 bg-gray-200 rounded w-1/2 mb-2" />
          <div className="h-4 bg-gray-200 rounded w-1/3 mb-4" />
          <div className="h-10 bg-gray-200 rounded mb-4" />
          <div className="h-16 bg-gray-200 rounded mb-2" />
          <div className="h-16 bg-gray-200 rounded mb-2" />
          <div className="h-16 bg-gray-200 rounded" />
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center p-8 text-center">
        <p role="alert" className="text-red-500 mb-4">{error}</p>
        <button
          onClick={() => {
            setLoading(true);
            setError('');
            fetchData().finally(() => setLoading(false));
          }}
          className="text-primary font-medium min-h-[44px]"
        >
          Retry
        </button>
      </div>
    );
  }

  if (!group) return null;

  return (
    <div className="flex flex-col h-full">
      {/* Back button */}
      <div className="px-4 pt-2">
        <a
          href="/groups"
          className="inline-flex items-center gap-1 text-primary text-sm font-medium min-h-[44px]"
        >
          <ArrowLeft className="w-4 h-4" />
          Groups
        </a>
      </div>

      {/* Group header */}
      <div className="px-4 py-3">
        <div className="flex items-center gap-3 mb-2">
          <div className="flex items-center justify-center w-12 h-12 rounded-full bg-primary/10">
            <Users className="w-6 h-6 text-primary" />
          </div>
          <div className="flex-1 min-w-0">
            <h1 className="text-xl font-semibold text-text truncate">{group.name}</h1>
            <p className="text-sm text-gray-500">
              {group.member_count} {group.member_count === 1 ? 'member' : 'members'} · Owner: {group.owner}
            </p>
          </div>
          {/* Owner can delete the group; any other member can leave it. */}
          {isOwner ? (
            <button
              onClick={() => setConfirm('delete')}
              data-testid="delete-group-btn"
              aria-label="Delete group"
              className="min-h-[44px] min-w-[44px] flex items-center justify-center text-red-500"
            >
              <Trash2 className="w-5 h-5" />
            </button>
          ) : isMemberOfGroup ? (
            <button
              onClick={() => setConfirm('leave')}
              data-testid="leave-group-btn"
              aria-label="Leave group"
              className="min-h-[44px] min-w-[44px] flex items-center justify-center text-gray-500"
            >
              <LogOut className="w-5 h-5" />
            </button>
          ) : null}
        </div>
      </div>

      {/* Tabs */}
      <div className="flex border-b border-gray-200 dark:border-dark-border px-4">
        <button
          onClick={() => setActiveTab('libraries')}
          className={`flex items-center gap-1.5 px-4 py-2 text-sm font-medium border-b-2 min-h-[44px] ${
            activeTab === 'libraries'
              ? 'border-primary text-primary'
              : 'border-transparent text-gray-500'
          }`}
        >
          <BookOpen className="w-4 h-4" />
          Libraries ({repos.length})
        </button>
        <button
          onClick={() => setActiveTab('members')}
          data-testid="members-tab"
          className={`flex items-center gap-1.5 px-4 py-2 text-sm font-medium border-b-2 min-h-[44px] ${
            activeTab === 'members'
              ? 'border-primary text-primary'
              : 'border-transparent text-gray-500'
          }`}
        >
          <Users className="w-4 h-4" />
          Members ({members.length})
        </button>
      </div>

      {/* Tab content */}
      <div className="flex-1 overflow-y-auto px-4 py-3 pb-20">
        {activeTab === 'libraries' && (
          <>
            {repos.length === 0 ? (
              <div className="flex flex-col items-center justify-center p-8 text-center">
                <BookOpen className="w-10 h-10 text-gray-300 mb-3" />
                <p className="text-gray-500">No libraries in this group</p>
              </div>
            ) : (
              <div className="flex flex-col gap-2">
                {repos.map((repo) => (
                  <div
                    key={repo.repo_id}
                    className="flex items-center gap-3 bg-white rounded-lg px-4 py-3 shadow-sm dark:bg-dark-surface dark:border-dark-border"
                  >
                    <BookOpen className="w-5 h-5 text-primary flex-shrink-0" />
                    <div className="flex-1 min-w-0">
                      <div className="font-medium text-text truncate">{repo.repo_name}</div>
                      <div className="text-xs text-gray-500">
                        {repo.owner_name} · {repo.permission}
                        {repo.encrypted && ' · Encrypted'}
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </>
        )}

        {activeTab === 'members' && (
          <GroupMemberManager
            groupId={groupId}
            members={members}
            currentEmail={currentEmail}
            callerRole={callerRole}
            onChanged={fetchData}
            onToast={showToast}
          />
        )}
      </div>

      {/* Confirm dialog for destructive header actions */}
      {confirm && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center p-4"
          data-testid="group-confirm-dialog"
        >
          <div className="fixed inset-0 bg-black/30" onClick={() => !working && setConfirm(null)} />
          <div className="relative bg-white dark:bg-dark-surface rounded-2xl shadow-xl w-full max-w-sm p-6">
            <h3 className="text-lg font-medium text-text mb-2">
              {confirm === 'delete' ? `Delete "${group.name}"?` : `Leave "${group.name}"?`}
            </h3>
            <p className="text-sm text-gray-500 mb-6">
              {confirm === 'delete'
                ? 'This permanently removes the group for all members.'
                : 'You will lose access to libraries shared with this group.'}
            </p>
            <div className="flex justify-end gap-2">
              <button
                onClick={() => setConfirm(null)}
                disabled={working}
                className="px-4 py-2 text-gray-600 min-h-[44px]"
              >
                Cancel
              </button>
              <button
                onClick={confirm === 'delete' ? handleDelete : handleLeave}
                disabled={working}
                data-testid="group-confirm-yes"
                className="px-4 py-2 bg-red-500 text-white rounded-lg font-medium min-h-[44px] disabled:opacity-50"
              >
                {working ? 'Working…' : confirm === 'delete' ? 'Delete' : 'Leave'}
              </button>
            </div>
          </div>
        </div>
      )}

      {toast && (
        <div
          className="fixed bottom-24 left-1/2 -translate-x-1/2 z-50 bg-black/80 text-white text-sm px-4 py-2 rounded-full"
          data-testid="group-toast"
        >
          {toast}
        </div>
      )}
    </div>
  );
}
