import cookie from 'react-cookies';
import { siteRoot } from './constants';
import { initAxiosForSeahubUsage } from './seahub-client';

class SubscriptionAPI {
  initForSeahubUsage({ siteRoot, xcsrfHeaders }) {
    return initAxiosForSeahubUsage(this, { siteRoot, xcsrfHeaders });
  }

  getSubscription() {
    const url = this.server + '/api/v2.1/subscription/';
    return this.req.get(url);
  }

}

let subscriptionAPI = new SubscriptionAPI();
let xcsrfHeaders = cookie.load('sfcsrftoken');
subscriptionAPI.initForSeahubUsage({ siteRoot, xcsrfHeaders });

export { subscriptionAPI };
