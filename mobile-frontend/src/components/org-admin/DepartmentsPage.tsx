import React, { useState, useEffect, useCallback } from 'react';
import { Building2, Plus, ArrowLeft, Trash2 } from 'lucide-react';
import { getAccountInfo, listMyDepartments, invalidateApiCache } from '../../lib/api';
import type { Department } from '../../lib/api';
import {
  getOrgId,
  listOrgDepartments,
  createDepartment,
  getOrgDepartment,
  deleteDepartment,
} from '../../lib/api/org-admin';
import type { OrgDepartment, OrgDepartmentMember } from '../../lib/api/org-admin';
import GroupMemberManager from '../groups/GroupMemberManager';

/**
 * Departments (address-book groups).
 *   - Org admins / superadmins: create departments, manage their members, delete.
 *   - Everyone else: a read-only list of the departments they belong to.
 * The access level is derived from the account (is_staff / is_org_staff).
 */
export default function DepartmentsPage() {
  const [isAdmin, setIsAdmin] = useState(false);
  const [email, setEmail] = useState('');
  const [orgId, setOrgId] = useState('');
  const [orgDepts, setOrgDepts] = useState<OrgDepartment[]>([]);
  const [myDepts, setMyDepts] = useState<Department[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [toast, setToast] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [newName, setNewName] = useState('');
  const [selected, setSelected] = useState<{ id: string; name: string; members: OrgDepartmentMember[] } | null>(null);
  const [confirmDel, setConfirmDel] = useState<OrgDepartment | null>(null);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    setTimeout(() => setToast(''), 2500);
  }, []);

  const load = useCallback(async () => {
    setError('');
    try {
      const info = await getAccountInfo();
      setEmail(info.email);
      const admin = Boolean(info.is_staff) || Boolean(info.is_org_staff);
      setIsAdmin(admin);
      if (admin) {
        const oid = await getOrgId();
        setOrgId(oid);
        setOrgDepts(await listOrgDepartments(oid));
      } else {
        setMyDepts(await listMyDepartments());
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load departments');
    }
  }, []);

  useEffect(() => {
    load().finally(() => setLoading(false));
  }, [load]);

  const handleCreate = async () => {
    const name = newName.trim();
    if (!name) return;
    setSubmitting(true);
    try {
      await createDepartment(orgId, name);
      setNewName('');
      showToast('Department created');
      setOrgDepts(await listOrgDepartments(orgId));
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to create department');
    } finally {
      setSubmitting(false);
    }
  };

  const openDept = async (d: OrgDepartment) => {
    try {
      setSelected(await getOrgDepartment(orgId, d.id));
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to open department');
    }
  };

  const refreshSelected = async () => {
    if (!selected) return;
    // The department detail (member list) is a cached GET; drop it so a just-
    // added/removed member is reflected immediately.
    await invalidateApiCache(`/api/v2.1/org/${orgId}/admin/address-book/groups/${selected.id}`);
    setSelected(await getOrgDepartment(orgId, selected.id));
  };

  const handleDelete = async () => {
    if (!confirmDel) return;
    try {
      await deleteDepartment(orgId, confirmDel.id);
      showToast('Department deleted');
      setConfirmDel(null);
      setOrgDepts(await listOrgDepartments(orgId));
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to delete');
      setConfirmDel(null);
    }
  };

  if (loading) {
    return <div className="p-4 text-gray-500" data-testid="departments-loading">Loading…</div>;
  }
  if (error) {
    return (
      <div className="flex flex-col items-center p-8 text-center">
        <p role="alert" className="text-red-500 mb-4">{error}</p>
        <button onClick={() => { setLoading(true); load().finally(() => setLoading(false)); }} className="text-primary min-h-[44px]">Retry</button>
      </div>
    );
  }

  // Managing a single department's members (admin only).
  if (selected) {
    return (
      <div className="flex flex-col h-full" data-testid="department-detail">
        <div className="px-4 pt-2">
          <button onClick={() => setSelected(null)} className="inline-flex items-center gap-1 text-primary text-sm font-medium min-h-[44px]">
            <ArrowLeft className="w-4 h-4" /> Departments
          </button>
        </div>
        <div className="px-4 py-3 flex items-center gap-3">
          <div className="flex items-center justify-center w-12 h-12 rounded-full bg-primary/10">
            <Building2 className="w-6 h-6 text-primary" />
          </div>
          <h1 className="text-xl font-semibold text-text truncate">{selected.name}</h1>
        </div>
        <div className="flex-1 overflow-y-auto px-4 py-3 pb-20">
          {/* The admin owns the department group, so full member management is available. */}
          <GroupMemberManager
            groupId={selected.id}
            members={selected.members as any}
            currentEmail={email}
            callerRole="owner"
            onChanged={refreshSelected}
            onToast={showToast}
          />
        </div>
        {toast && <Toast msg={toast} />}
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full" data-testid="departments-page">
      <div className="px-4 py-3">
        <h1 className="text-xl font-semibold text-text flex items-center gap-2">
          <Building2 className="w-6 h-6 text-primary" /> Departments
        </h1>
        {!isAdmin && (
          <p className="text-sm text-gray-500 mt-1">Departments you belong to. Managed by your organization's admins.</p>
        )}
      </div>

      <div className="flex-1 overflow-y-auto px-4 pb-20">
        {isAdmin && (
          <div className="mb-3">
            {showCreate ? (
              <div className="bg-white dark:bg-dark-surface rounded-lg p-3 shadow-sm flex flex-wrap gap-2 w-full" data-testid="create-department-panel">
                <input
                  autoFocus
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  placeholder="Department name"
                  data-testid="department-name-input"
                  className="flex-1 min-w-[8rem] px-3 py-2 border border-gray-300 rounded-lg text-text min-h-[44px]"
                />
                <button onClick={handleCreate} disabled={submitting} data-testid="create-department-submit" className="px-4 bg-primary text-white rounded-lg min-h-[44px] disabled:opacity-50">Add</button>
                <button onClick={() => { setShowCreate(false); setNewName(''); }} className="px-3 text-gray-500 min-h-[44px]">Cancel</button>
              </div>
            ) : (
              <button onClick={() => setShowCreate(true)} data-testid="new-department-btn" className="w-full flex items-center justify-center gap-2 py-2.5 rounded-lg border border-dashed border-primary/50 text-primary text-sm font-medium min-h-[44px]">
                <Plus className="w-4 h-4" /> New department
              </button>
            )}
          </div>
        )}

        {(isAdmin ? orgDepts : myDepts).length === 0 ? (
          <div className="flex flex-col items-center justify-center p-8 text-center">
            <Building2 className="w-10 h-10 text-gray-300 mb-3" />
            <p className="text-gray-500">{isAdmin ? 'No departments yet' : 'You are not in any departments'}</p>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            {isAdmin
              ? orgDepts.map((d) => (
                  <div key={d.id} data-testid="department-row" data-name={d.name} className="flex items-center gap-3 bg-white rounded-lg px-4 py-3 shadow-sm dark:bg-dark-surface">
                    <button onClick={() => openDept(d)} className="flex items-center gap-3 flex-1 min-w-0 text-left">
                      <Building2 className="w-5 h-5 text-primary flex-shrink-0" />
                      <span className="font-medium text-text truncate">{d.name}</span>
                    </button>
                    <button onClick={() => setConfirmDel(d)} data-testid={`delete-department-${d.name}`} aria-label={`Delete ${d.name}`} className="min-h-[36px] min-w-[36px] flex items-center justify-center text-red-400">
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                ))
              : myDepts.map((d) => (
                  <div key={d.id} data-testid="department-row" data-name={d.name} className="flex items-center gap-3 bg-white rounded-lg px-4 py-3 shadow-sm dark:bg-dark-surface">
                    <Building2 className="w-5 h-5 text-primary flex-shrink-0" />
                    <span className="font-medium text-text truncate">{d.name}</span>
                  </div>
                ))}
          </div>
        )}
      </div>

      {confirmDel && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4" data-testid="department-confirm-dialog">
          <div className="fixed inset-0 bg-black/30" onClick={() => setConfirmDel(null)} />
          <div className="relative bg-white dark:bg-dark-surface rounded-2xl shadow-xl w-full max-w-sm p-6">
            <h3 className="text-lg font-medium text-text mb-2">Delete "{confirmDel.name}"?</h3>
            <p className="text-sm text-gray-500 mb-6">This removes the department and its memberships.</p>
            <div className="flex justify-end gap-2">
              <button onClick={() => setConfirmDel(null)} className="px-4 py-2 text-gray-600 min-h-[44px]">Cancel</button>
              <button onClick={handleDelete} data-testid="department-confirm-yes" className="px-4 py-2 bg-red-500 text-white rounded-lg font-medium min-h-[44px]">Delete</button>
            </div>
          </div>
        </div>
      )}
      {toast && <Toast msg={toast} />}
    </div>
  );
}

function Toast({ msg }: { msg: string }) {
  return (
    <div className="fixed bottom-24 left-1/2 -translate-x-1/2 z-50 bg-black/80 text-white text-sm px-4 py-2 rounded-full" data-testid="department-toast">
      {msg}
    </div>
  );
}
