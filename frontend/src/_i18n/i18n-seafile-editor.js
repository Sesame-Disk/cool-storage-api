import i18n from 'i18next';
import Backend from 'i18next-http-backend';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import { lang, mediaUrl } from '../utils/constants';
import { firstValue, resolveSeafileEditorLocaleAsset, SUPPORTED_UI_LOCALES } from '../utils/locale-utils';

i18n
  .use(Backend)
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    lng: lang,
    fallbackLng: 'en',
    ns: ['seafile-editor'],
    defaultNS: 'seafile-editor',

    whitelist: SUPPORTED_UI_LOCALES,

    backend: {
      loadPath: (languages, namespaces) => {
        const locale = resolveSeafileEditorLocaleAsset(firstValue(languages));
        const namespace = firstValue(namespaces);
        return mediaUrl + 'locales/' + locale + '/' + namespace + '.json';
      },
    },

    debug: false,

    interpolation: {
      escapeValue: false,
    },

    load: 'currentOnly',

    react: {
      wait: true,
    }
  });

export default i18n;
