import { useEffect } from 'react';
import { startSyncController, stopSyncController } from '../../lib/sync/syncController';

/** Headless island: wires the focus/visibility/online sync triggers once for
 * the authenticated app. Renders nothing; safely no-ops where folder sync is
 * unsupported. */
export default function SyncControllerMount() {
  useEffect(() => {
    startSyncController();
    return () => stopSyncController();
  }, []);
  return null;
}
