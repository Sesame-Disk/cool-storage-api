import React from 'react';
import PropTypes from 'prop-types';
import Logo from './logo';
import MainSideNav from './main-side-nav';
import SideNavFooter from './side-nav-footer';

const propTypes = {
  isSidePanelClosed: PropTypes.bool.isRequired,
  currentTab: PropTypes.oneOfType([PropTypes.string, PropTypes.number]).isRequired,
  onCloseSidePanel: PropTypes.func.isRequired,
  tabItemClick: PropTypes.func.isRequired,
};

class SidePanel extends React.Component {

  render() {
    const isOpen = !this.props.isSidePanelClosed;
    return (
      <React.Fragment>
        <div
          className={`side-panel-backdrop ${isOpen ? 'show' : ''}`}
          onClick={this.props.onCloseSidePanel}
          aria-hidden="true"
        />
        <div
          className={`side-panel ${isOpen ? 'left-zero' : ''}`}
          role="navigation"
          aria-label="Main"
          aria-hidden={!isOpen ? 'true' : 'false'}
        >
          <div className="side-panel-north">
            <Logo onCloseSidePanel={this.props.onCloseSidePanel}/>
          </div>
          <div className="side-panel-center">
            <MainSideNav tabItemClick={this.props.tabItemClick} currentTab={this.props.currentTab} />
          </div>
          <div className="side-panel-footer">
            <SideNavFooter />
          </div>
        </div>
      </React.Fragment>
    );
  }
}

SidePanel.propTypes = propTypes;

export default SidePanel;
