import { resolveCalendarLocaleKey } from './locale-utils';

const zhCN = require('@seafile/seafile-calendar/lib/locale/zh_CN');
const zhTW = require('@seafile/seafile-calendar/lib/locale/zh_TW');
const enUS = require('@seafile/seafile-calendar/lib/locale/en_US');
const frFR = require('@seafile/seafile-calendar/lib/locale/fr_FR');
const deDE = require('@seafile/seafile-calendar/lib/locale/de_DE');
const esES = require('@seafile/seafile-calendar/lib/locale/es_ES');
const plPL = require('@seafile/seafile-calendar/lib/locale/pl_PL');
const csCZ = require('@seafile/seafile-calendar/lib/locale/cs_CZ');
const ruRU = require('@seafile/seafile-calendar/lib/locale/ru_RU');

const CALENDAR_LOCALES = {
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  en: enUS,
  fr: frFR,
  de: deDE,
  es: esES,
  pl: plPL,
  cs: csCZ,
  ru: ruRU,
};

function translateCalendar() {
  const locale = resolveCalendarLocaleKey(window.app?.config?.lang);
  return CALENDAR_LOCALES[locale] || enUS;
}

export { translateCalendar };
