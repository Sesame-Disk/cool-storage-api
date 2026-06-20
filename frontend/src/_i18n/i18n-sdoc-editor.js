import i18n from 'i18next';
import Backend from 'i18next-http-backend';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';
import { mediaUrl } from '../utils/constants';
import { ensureSupportedLocale, firstValue, resolveSdocEditorLocaleAsset, SUPPORTED_UI_LOCALES } from '../utils/locale-utils';

const lang = ensureSupportedLocale(window.app?.config?.lang);

i18n
  .use(Backend)
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    lng: lang,
    fallbackLng: 'en',
    ns: ['sdoc-editor'],
    defaultNS: 'sdoc-editor',

    whitelist: SUPPORTED_UI_LOCALES,

    backend: {
      loadPath: (languages, namespaces) => {
        const locale = resolveSdocEditorLocaleAsset(firstValue(languages));
        const namespace = firstValue(namespaces);
        return mediaUrl + 'sdoc-editor/locales/' + locale + '/' + namespace + '.json';
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
