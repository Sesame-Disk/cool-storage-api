import axios from 'axios';
import cookie from 'react-cookies';
import { siteRoot } from './constants';

class SubscriptionAPI {
  initForSeahubUsage({ siteRoot, xcsrfHeaders }) {
    if (siteRoot && siteRoot.charAt(siteRoot.length - 1) === '/') {
      var server = siteRoot.substring(0, siteRoot.length - 1);
      this.server = server;
    } else {
      this.server = siteRoot;
    }

    this.req = axios.create({
      headers: {
        'X-CSRFToken': xcsrfHeaders,
      }
    });
    return this;
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
