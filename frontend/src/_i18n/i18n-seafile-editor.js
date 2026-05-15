import i18n from 'i18next';
import Backend from 'i18next-http-backend';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import { lang, mediaUrl } from '../utils/constants';

i18n
  .use(Backend)
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    lng: lang,
    fallbackLng: 'en',
    ns: ['seafile-editor'],
    defaultNS: 'seafile-editor',

    whitelist: ['en', 'zh-CN', 'fr', 'de', 'cs', 'es', 'es-AR', 'es-MX', 'ru'],

    backend: {
      loadPath: mediaUrl + 'locales/{{ lng }}/{{ ns }}.json',
      // loadPath: '/media/locales/{{lng}}/{{ns}}.json',
    },

    debug: false, // console log if debug: true

    interpolation: {
      escapeValue: false, // not needed for react!!
    },


    load: 'currentOnly',

    react: {
      wait: true,
    }
  });

export default i18n;
