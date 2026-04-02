/**
 * Quota warning utilities.
 *
 * The backend sends an X-Quota-Warning header on successful responses when the
 * org's usage is above 80% of a limit (storage, traffic, etc.). This module
 * provides the message mapping and an axios response interceptor.
 */
import toaster from '../components/toast';
import { gettext } from './constants';

// Stable toast ID — prevents duplicate toasts when multiple API calls return
// the same warning in quick succession (e.g. chunked uploads).
export const QUOTA_WARNING_TOAST_ID = 'quota-warning';

// How long the warning toast stays visible (seconds, matches toaster API).
export const QUOTA_WARNING_DURATION = 15;

const MESSAGES = {
  'storage': () => gettext('Your storage space is almost full. Please free up space or upgrade your plan.'),
  'traffic-combined': () => gettext('Your monthly traffic limit is almost reached. Transfers may be restricted soon.'),
  'traffic-upload': () => gettext('Your monthly upload traffic limit is almost reached.'),
  'traffic-download': () => gettext('Your monthly download traffic limit is almost reached.'),
};

/**
 * Map an X-Quota-Warning header value to a user-facing message.
 * Returns null if the header is absent or empty.
 */
export function getQuotaWarningMessage(headerValue) {
  if (!headerValue) return null;
  const msgFn = MESSAGES[headerValue];
  if (msgFn) return msgFn();
  return gettext('You are approaching your quota limit. Consider upgrading your plan.');
}

/**
 * Axios response interceptor callback. Checks for the X-Quota-Warning header
 * on successful responses (2xx) and shows a toast warning.
 *
 * Usage: axiosInstance.interceptors.response.use(quotaWarningInterceptor)
 */
export function quotaWarningInterceptor(response) {
  const warning = response.headers && response.headers['x-quota-warning'];
  if (warning) {
    const msg = getQuotaWarningMessage(warning);
    if (msg) {
      toaster.warning(msg, {
        id: QUOTA_WARNING_TOAST_ID,
        duration: QUOTA_WARNING_DURATION,
      });
    }
  }
  return response;
}
