import React, { useState } from 'react';
import { Shield, Crown, User, Trash2, UserPlus, ShieldOff } from 'lucide-react';
import {
  addGroupMember,
  removeGroupMember,
  setGroupAdmin,
} from '../../lib/api';
import type { GroupMember, SearchedUser } from '../../lib/api';
import UserPicker from '../share/UserPicker';

interface GroupMemberManagerProps {
  groupId: string;
  members: GroupMember[];
  /** The signed-in user's email (to identify "me" and the Leave action). */
  currentEmail: string;
  /** The signed-in user's role in this group: 'owner' | 'admin' | 'member' | ''. */
  callerRole: string;
  onChanged: () => void;
  onToast: (msg: string) => void;
}

/**
 * Member list with role-gated management actions. Access levels mirror the
 * backend exactly:
 *   - owner:  add / remove anyone, promote & demote admins, (delete group above)
 *   - admin:  add members, remove plain members
 *   - member: leave the group (remove self) only
 */
export default function GroupMemberManager({
  groupId,
  members,
  currentEmail,
  callerRole,
  onChanged,
  onToast,
}: GroupMemberManagerProps) {
  const [busy, setBusy] = useState<string>(''); // email currently being mutated
  const [adding, setAdding] = useState(false);

  const isOwner = callerRole === 'owner';
  const isAdmin = callerRole === 'admin';
  const canManage = isOwner || isAdmin;

  const run = async (key: string, fn: () => Promise<void>, ok: string) => {
    setBusy(key);
    try {
      await fn();
      onToast(ok);
      onChanged();
    } catch (err) {
      onToast(err instanceof Error ? err.message : 'Action failed');
    } finally {
      setBusy('');
    }
  };

  const handleAdd = async (u: SearchedUser) => {
    if (members.some((m) => m.email === u.email)) {
      onToast('Already a member');
      return;
    }
    await run(`add:${u.email}`, () => addGroupMember(groupId, u.email), `Added ${u.email}`);
  };

  return (
    <div className="flex flex-col gap-2">
      {/* Add member (owner/admin) */}
      {canManage && (
        <div className="mb-1">
          {adding ? (
            <div className="bg-white dark:bg-dark-surface rounded-lg p-3 shadow-sm" data-testid="add-member-panel">
              <UserPicker selectedUsers={[]} onSelect={handleAdd} onRemove={() => {}} />
              <button
                onClick={() => setAdding(false)}
                className="mt-2 text-sm text-gray-500 min-h-[36px]"
              >
                Done
              </button>
            </div>
          ) : (
            <button
              onClick={() => setAdding(true)}
              data-testid="add-member-btn"
              className="w-full flex items-center justify-center gap-2 py-2.5 rounded-lg border border-dashed border-primary/50 text-primary text-sm font-medium min-h-[44px]"
            >
              <UserPlus className="w-4 h-4" />
              Add member
            </button>
          )}
        </div>
      )}

      {members.map((member) => {
        const isSelf = member.email === currentEmail;
        const memberIsOwner = member.role.toLowerCase() === 'owner';
        const memberIsAdmin = member.role.toLowerCase() === 'admin';
        // Owner can act on everyone except themselves; admin can remove plain
        // members; a member can leave (remove self).
        const canRemove =
          (isOwner && !memberIsOwner) ||
          (isAdmin && !memberIsOwner && !memberIsAdmin && !isSelf) ||
          (isSelf && !memberIsOwner);
        const canToggleAdmin = isOwner && !memberIsOwner;

        return (
          <div
            key={member.email}
            data-testid="group-member-row"
            data-email={member.email}
            data-role={member.role}
            className="flex items-center gap-3 bg-white rounded-lg px-4 py-3 shadow-sm dark:bg-dark-surface dark:border-dark-border"
          >
            {member.avatar_url ? (
              <img src={member.avatar_url} alt="" className="w-8 h-8 rounded-full" />
            ) : (
              <User className="w-8 h-8 text-gray-400" />
            )}
            <div className="flex-1 min-w-0">
              <div className="font-medium text-text truncate">
                {member.name || member.email}
                {isSelf && <span className="text-gray-400 font-normal"> (you)</span>}
              </div>
              <div className="text-xs text-gray-500 truncate">{member.email}</div>
            </div>
            <RoleBadge role={member.role} />

            {canToggleAdmin && (
              <button
                onClick={() =>
                  run(
                    `role:${member.email}`,
                    () => setGroupAdmin(groupId, member.email, !memberIsAdmin),
                    memberIsAdmin ? 'Admin removed' : 'Made admin',
                  )
                }
                disabled={busy === `role:${member.email}`}
                data-testid={`member-toggle-admin-${member.email}`}
                aria-label={memberIsAdmin ? `Remove admin ${member.email}` : `Make admin ${member.email}`}
                className="min-h-[36px] min-w-[36px] flex items-center justify-center text-gray-500 disabled:opacity-40"
              >
                {memberIsAdmin ? <ShieldOff className="w-4 h-4" /> : <Shield className="w-4 h-4" />}
              </button>
            )}
            {canRemove && (
              <button
                onClick={() =>
                  run(
                    `rm:${member.email}`,
                    () => removeGroupMember(groupId, member.email),
                    isSelf ? 'You left the group' : 'Member removed',
                  )
                }
                disabled={busy === `rm:${member.email}`}
                data-testid={`member-remove-${member.email}`}
                aria-label={isSelf ? 'Leave group' : `Remove ${member.email}`}
                className="min-h-[36px] min-w-[36px] flex items-center justify-center text-red-400 disabled:opacity-40"
              >
                <Trash2 className="w-4 h-4" />
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}

function RoleBadge({ role }: { role: string }) {
  const lower = role.toLowerCase();
  if (lower === 'owner') {
    return (
      <span className="inline-flex items-center gap-1 text-xs font-medium px-2 py-1 rounded-full bg-amber-100 text-amber-700">
        <Crown className="w-3 h-3" />
        Owner
      </span>
    );
  }
  if (lower === 'admin') {
    return (
      <span className="inline-flex items-center gap-1 text-xs font-medium px-2 py-1 rounded-full bg-blue-100 text-blue-700">
        <Shield className="w-3 h-3" />
        Admin
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 text-xs font-medium px-2 py-1 rounded-full bg-gray-100 text-gray-600">
      <User className="w-3 h-3" />
      Member
    </span>
  );
}
