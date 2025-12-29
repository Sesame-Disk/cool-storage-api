/* eslint-disable */
import React from 'react';
import { gettext, siteRoot } from '../utils/constants';
import { EvalProFunc } from './ad';
import ChatLauncher from './chat';

export default function RenderRole({ inSidebar, isOrgStaff }) {
  let data;
  const { userRole } = window.app.pageOptions;
  if (userRole === 'personalfree' || userRole === 'restricted') {
    data = {
      url: `${siteRoot}billing/`,
      text: gettext('Upgrade'),
      className: `item ${inSidebar ? 'highlighted-link highlighted-link-p-x' : 'highlight-item'}`,
      title: 'Upgrade your account to get PRO features!'
    };
    let data2 = {
      url: `${siteRoot}org/info/`,
      text: gettext('Organization Admin'),
      className: 'item highlight-item',
      title: 'Add and manage users, securely share with them, delegate tasks and more!',
      click: (e) => {
        e.preventDefault()
        const fn = EvalProFunc(null, { manOrg: true })
        if (fn) fn()
      }
    };
    return data && <>
      <a href={data.url} title={data.title} className={data.className}><span className="sf2-icon-star" style={{ verticalAlign: 'middle' }} /> {data.text}</a>
      {!inSidebar && !isOrgStaff && <a href={data2.url} title={data2.title} className={data2.className} onClick={data2.click}><span className="sf2-icon-star" style={{ verticalAlign: 'middle' }} /> {data2.text}</a>}
      <ChatLauncher linkClassName="item" showNewBadge={true} inSidebar={inSidebar} />
    </>;
  } else if (userRole === 'personalpro' || userRole === 'business') {
    data = {
      url: `${siteRoot}billing/`,
      text: gettext('Billing')
    };
    return data && <>
      <a href={data.url} title={data.text} className="item">{data.text}</a>
      <ChatLauncher linkClassName="item" showNewBadge={true} inSidebar={inSidebar} />
    </>;
  } else if (userRole === 'pay_restricted_owner') {
    data = {
      url: `${siteRoot}billing/`,
      text: gettext('Billing'),
      styte: {
        color: 'red'
      },
      className: `item highlighted-link${inSidebar ? ' highlighted-link-p-x' : ''}`,
      title: 'You have reached the traffic limit. Go to Billing to update it'
    };
    return data && <>
      <a href={data.url} title={data.title} className={data.className} style={data.styte}>{data.text}</a>
      <ChatLauncher linkClassName="item" showNewBadge={true} inSidebar={inSidebar} />
    </>;
  } else {
    return <ChatLauncher linkClassName="item" showNewBadge={true} inSidebar={inSidebar} />;
  }

}
