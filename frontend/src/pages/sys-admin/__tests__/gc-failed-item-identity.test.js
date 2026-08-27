import GC from '../gc';

// gc.js pulls the API client, which pulls seafile-js -> axios (ESM) and cannot be
// parsed by this jest setup. None of it is involved in building a selector, so it is
// stubbed out — babel-plugin-jest-hoist lifts this call above the import above, which
// is why the module never loads for real.
jest.mock('../../../utils/seafile-api', () => ({ seafileAPI: {} }));

// The DLQ admin endpoints reject a selector that does not name the lifecycle it acts
// on: identity_at is part of the primary key of gc_failed_items for EVERY item type,
// and a block row additionally names the exact physical incarnation
// P = (storage_class, storage_key) its candidate was created for. So these two
// helpers are the whole contract between the table the operator is looking at and the
// row the server will delete or requeue — get the mapping wrong and every action in
// this page fails with a 400, or (worse, before the server required it) acts on a
// different lifecycle.
//
// The list endpoint answers in snake_case today, but this page has always accepted
// both spellings, and that tolerance is exactly the kind of thing that rots silently.
describe('sys-admin GC failed-item identity', () => {
    // The methods under test are instance fields, so they need an instance — but not a
    // mounted one: the constructor only seeds state, and componentDidMount (which
    // fetches) never runs.
    const gc = new GC({});

    const blockItem = {
        org_id: '11111111-1111-1111-1111-111111111111',
        failed_at: '2026-08-26T10:00:00Z',
        item_type: 'block',
        item_id: 'abc123',
        identity_at: '2026-08-26T09:00:00Z',
        block_gc_candidate_identity: {
            target: { storage_class: 'standard', storage_key: 'blocks/org/abc123-v1' },
            candidate_at: '2026-08-26T09:00:00Z'
        }
    };

    it('names identity_at alone for a non-block item', () => {
        const payload = gc.getFailedItemPayload({
            org_id: '22222222-2222-2222-2222-222222222222',
            failed_at: '2026-08-26T10:00:00Z',
            item_type: 'commit',
            item_id: 'commit-1',
            identity_at: '2026-08-26T09:30:00Z'
        });

        expect(payload.identity_at).toBe('2026-08-26T09:30:00Z');
        // A non-block row carries no incarnation, and sending empty strings would make
        // the server reject the selector rather than treat them as "absent".
        expect(payload).not.toHaveProperty('candidate_storage_class');
        expect(payload).not.toHaveProperty('candidate_storage_key');
        expect(payload).not.toHaveProperty('candidate_at');
    });

    it('names the exact incarnation for a block item', () => {
        const payload = gc.getFailedItemPayload(blockItem);

        expect(payload).toMatchObject({
            org_id: blockItem.org_id,
            failed_at: blockItem.failed_at,
            item_type: 'block',
            item_id: 'abc123',
            identity_at: '2026-08-26T09:00:00Z',
            candidate_storage_class: 'standard',
            candidate_storage_key: 'blocks/org/abc123-v1',
            candidate_at: '2026-08-26T09:00:00Z'
        });
        // The server refuses a block selector whose candidate_at is not the identity
        // instant, so these must never drift apart here.
        expect(payload.candidate_at).toBe(payload.identity_at);
    });

    it('reads a camelCase response the same way', () => {
        const payload = gc.getFailedItemPayload({
            orgID: blockItem.org_id,
            failedAt: blockItem.failed_at,
            itemType: 'block',
            itemID: 'abc123',
            identityAt: '2026-08-26T09:00:00Z',
            blockGCCandidateIdentity: {
                Target: { StorageClass: 'standard', StorageKey: 'blocks/org/abc123-v1' },
                CandidateAt: '2026-08-26T09:00:00Z'
            }
        });

        expect(payload).toMatchObject({
            identity_at: '2026-08-26T09:00:00Z',
            candidate_storage_class: 'standard',
            candidate_storage_key: 'blocks/org/abc123-v1',
            candidate_at: '2026-08-26T09:00:00Z'
        });
    });

    it('falls back to identity_at when a block row carries no candidate_at', () => {
        const payload = gc.getFailedItemPayload({
            ...blockItem,
            block_gc_candidate_identity: {
                target: { storage_class: 'standard', storage_key: 'blocks/org/abc123-v1' }
            }
        });

        expect(payload.candidate_at).toBe('2026-08-26T09:00:00Z');
    });

    it('gives two incarnations of one block distinct action keys', () => {
        const p2 = {
            ...blockItem,
            identity_at: '2026-08-26T09:05:00Z',
            block_gc_candidate_identity: {
                target: { storage_class: 'standard', storage_key: 'blocks/org/abc123-v2' },
                candidate_at: '2026-08-26T09:05:00Z'
            }
        };

        // Same org, same block, same item_type: only P and the lifecycle differ. A key
        // that collapsed them would mark both rows busy on one click, leaving the
        // operator unable to tell which action is in flight.
        expect(gc.getFailedItemActionKey('requeue', blockItem))
            .not.toBe(gc.getFailedItemActionKey('requeue', p2));
        // ...and the action itself is still part of the key.
        expect(gc.getFailedItemActionKey('requeue', blockItem))
            .not.toBe(gc.getFailedItemActionKey('delete', blockItem));
    });
});
