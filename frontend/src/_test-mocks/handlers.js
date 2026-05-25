// MSW request handlers for unit tests. Add per-feature handlers here
// and override in individual tests via server.use(...).
import { http, HttpResponse } from 'msw';

export const handlers = [
  http.get('/api2/account/info/', () => {
    return HttpResponse.json({
      email: 'test@example.com',
      name: 'Test User',
      usage: 0,
      total: 1_073_741_824,
      institution: '',
    });
  }),

  http.get('/api2/repos/', () => {
    return HttpResponse.json([]);
  }),

  http.get('/api/v2.1/notifications/', () => {
    return HttpResponse.json({ notification_list: [], count: 0, unseen_count: 0 });
  }),
];
