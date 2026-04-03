import React from 'react';
import { gettext } from '../../utils/constants';
import { SeahubSelect } from '../common/select';
import { getSettingsPageOptions, getSettingsRoute } from './page-options';

class LanguageSetting extends React.Component {

  onSelectChange = (selectedItem) => {
    // selectedItem: {value: '...', label: '...'}
    location.href = getSettingsRoute('languageChange', 'i18n/?lang={langCode}', {
      langCode: selectedItem.value
    });
  };

  render() {
    const pageOptions = getSettingsPageOptions();
    const currentLang = pageOptions.currentLang || { langCode: pageOptions.langCode || 'en', langName: pageOptions.langCode || 'en' };
    const langList = Array.isArray(pageOptions.langList) && pageOptions.langList.length > 0
      ? pageOptions.langList
      : [currentLang];
    const options = langList.map((item, index) => {
      return {
        value: item.langCode,
        label: item.langName
      };
    });

    return (
      <div className="setting-item" id="lang-setting">
        <h3 className="setting-item-heading">{gettext('Language Setting')}</h3>
        <SeahubSelect
          className='language-selector'
          value={{ value: currentLang.langCode, label: currentLang.langName }}
          options={options}
          onChange={this.onSelectChange}
        />
      </div>
    );
  }
}

export default LanguageSetting;
