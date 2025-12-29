/* eslint-disable */
import React from 'react';
import PropTypes from 'prop-types';
import { isPro, gettext, shareLinkExpireDaysMin, shareLinkExpireDaysMax, shareLinkExpireDaysDefault } from '../../utils/constants';
import { seafileAPI } from '../../utils/seafile-api';
import { Utils } from '../../utils/utils';
import ShareLink from '../../models/share-link';
import toaster from '../../components/toast';
import Loading from '../../components/loading';
import LinkDetails from '../../components/share-link-panel/link-details';
import LinkCreation from './link-creation';
import LinkList from './link-list';
import { changeLinkToChina } from '../links';

const propTypes = {
  itemPath: PropTypes.string.isRequired,
  repoID: PropTypes.string.isRequired,
  closeShareDialog: PropTypes.func.isRequired,
  userPerm: PropTypes.string,
  itemType: PropTypes.string,
  shareLinks: PropTypes.array,
  setShareLinks: PropTypes.func
};

class ShareLinkPanel extends React.Component {

  constructor(props) {
    super(props);

    this.isExpireDaysNoLimit = (shareLinkExpireDaysMin === 0 && shareLinkExpireDaysMax === 0 && shareLinkExpireDaysDefault == 0);
    this.defaultExpireDays = this.isExpireDaysNoLimit ? '' : shareLinkExpireDaysDefault;

    this.state = {
      isLoading: true,
      isLoadingMore: false,
      page: 1,
      mode: '',
      sharedLinkInfo: null,
      shareLinks: [],
      permissionOptions: [],
      currentPermission: '',
      justDeletedLink: false
    };
  }

  componentDidMount() {
    const { page } = this.state;
    const { repoID, itemPath: path } = this.props;
    seafileAPI.listShareLinks({ repoID, path, page }).then((res) => {
      const filteredLinks = (res.data.map(item => new ShareLink(item))).filter(i => i.password !== '').map(l => changeLinkToChina(l));

      // Si hay exactamente un link, mostrar vista previa automáticamente
      const newState = {
        isLoading: false,
        shareLinks: filteredLinks
      };

      if (filteredLinks.length === 1) {
        newState.mode = 'displayLinkDetails';
        newState.sharedLinkInfo = filteredLinks[0];
      }

      this.setState(newState);
    }).catch(error => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });

    if (isPro) {
      const { itemType, userPerm } = this.props;
      if (itemType == 'library') {
        let permissionOptions = Utils.getShareLinkPermissionList(itemType, userPerm, path);
        this.setState({
          permissionOptions: permissionOptions,
          currentPermission: permissionOptions[0],
        });
      } else {
        let getDirentInfoAPI;
        if (this.props.itemType === 'file') {
          getDirentInfoAPI = seafileAPI.getFileInfo(repoID, path);
        } else if (this.props.itemType === 'dir') {
          getDirentInfoAPI = seafileAPI.getDirInfo(repoID, path);
        }
        getDirentInfoAPI.then((res) => {
          let canEdit = res.data.can_edit;
          let permission = res.data.permission;
          let permissionOptions = Utils.getShareLinkPermissionList(this.props.itemType, permission, path, canEdit);
          this.setState({
            permissionOptions: permissionOptions,
            currentPermission: permissionOptions[0],
          });
        }).catch(error => {
          let errMessage = Utils.getErrorMsg(error);
          toaster.danger(errMessage);
        });
      }
    }
  }

  componentDidUpdate(prevProps) {
    if (prevProps.shareLinks !== this.props.shareLinks) {
      const newShareLinks = this.props.shareLinks.filter(i => i.password !== '').map(l => changeLinkToChina({ ...l }));
      const { mode, sharedLinkInfo, justDeletedLink } = this.state;

      // Si estamos en vista de detalles (creando o viendo un link)
      if (mode === 'displayLinkDetails' && sharedLinkInfo) {
        // Encontrar el link actualizado por token
        const updatedLink = newShareLinks.find(l => l.token === sharedLinkInfo.token);
        if (updatedLink) {
          // Mantener en vista de detalles con el link actualizado
          this.setState({
            shareLinks: newShareLinks,
            sharedLinkInfo: updatedLink
          });
          return;
        }
      }

      // Si acabamos de crear un link (modo displayLinkDetails pero sin sharedLinkInfo aún)
      if (mode === 'displayLinkDetails' && !sharedLinkInfo && newShareLinks.length > 0) {
        // Mostrar el link más reciente (el primero en la lista)
        this.setState({
          shareLinks: newShareLinks,
          sharedLinkInfo: newShareLinks[0]
        });
        return;
      }

      // Si acabamos de eliminar un link, NO aplicar auto-preview
      if (justDeletedLink) {
        this.setState({
          shareLinks: newShareLinks
        });
        return;
      }

      // Si hay exactamente un link, mostrar vista previa automáticamente
      const newState = {
        mode: newShareLinks.length === 1 ? 'displayLinkDetails' : '',
        shareLinks: newShareLinks
      };

      if (newShareLinks.length === 1) {
        newState.sharedLinkInfo = newShareLinks[0];
      }

      this.setState(newState);
    }
  }


  showLinkDetails = (link) => {
    this.setState({
      sharedLinkInfo: link,
      mode: link ? 'displayLinkDetails' : ''
    });
  }

  updateLink = (link) => {
    const { shareLinks } = this.state;
    const updatedLinks = shareLinks.map(item => item.token == link.token ? link : item);
    this.setState({
      sharedLinkInfo: link,
      shareLinks: updatedLinks
    });
    // Actualizar también los links en el componente padre
    const updatedPropsLinks = this.props.shareLinks.map(item => item.token == link.token ? link : item);
    this.props.setShareLinks(updatedPropsLinks);
  }

  deleteLink = (token) => {
    const { shareLinks } = this.state;
    seafileAPI.deleteShareLink(token).then(() => {
      this.setState({
        mode: '',
        sharedLinkInfo: null,
        shareLinks: shareLinks.filter(item => item.token !== token),
        justDeletedLink: true
      });
      this.props.setShareLinks(this.props.shareLinks.filter(item => item.token !== token));
      toaster.success(gettext('Successfully deleted 1 share link'));

      // Reset flag después de un momento
      setTimeout(() => {
        this.setState({ justDeletedLink: false });
      }, 100);
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  }

  deleteShareLinks = () => {
    const { shareLinks } = this.state;
    const tokens = shareLinks.filter(item => item.isSelected).map(link => link.token);
    seafileAPI.deleteShareLinks(tokens).then(res => {
      const { success, failed } = res.data;
      if (success.length) {
        let newShareLinkList = shareLinks.filter(shareLink => {
          return !success.some(deletedShareLink => {
            return deletedShareLink.token == shareLink.token;
          });
        });
        this.setState({
          shareLinks: newShareLinkList
        });
        let newShareLinkList2 = this.props.shareLinks.filter(shareLink => {
          return !success.some(deletedShareLink => {
            return deletedShareLink.token == shareLink.token;
          });
        });
        this.props.setShareLinks(newShareLinkList2);
        const length = success.length;
        const msg = length == 1 ?
          gettext('Successfully deleted 1 share link') :
          gettext('Successfully deleted {number_placeholder} share links')
            .replace('{number_placeholder}', length);
        toaster.success(msg);
      }
      failed.forEach(item => {
        const msg = `${item.token}: ${item.error_msg}`;
        toaster.danger(msg);
      });
    }).catch((error) => {
      let errMessage = Utils.getErrorMsg(error);
      toaster.danger(errMessage);
    });
  }

  updateAfterCreation = (newData) => {
    const { mode, shareLinks: links } = this.state;
    if (mode == 'singleLinkCreation') {
      links.unshift(newData);
      const updatedLinks = links.map(l => changeLinkToChina(l)).filter(i => i.password !== '');
      this.setState({
        mode: 'displayLinkDetails',
        sharedLinkInfo: newData,
        shareLinks: updatedLinks
      });
      // Actualizar también los links en el componente padre
      const newPropsLinks = [newData, ...this.props.shareLinks];
      this.props.setShareLinks(newPropsLinks);
    } else { // 'linksCreation'
      const updatedLinks = (newData.concat(links)).map(l => changeLinkToChina(l)).filter(i => i.password !== '');
      this.setState({
        mode: '',
        shareLinks: updatedLinks
      });
      // Actualizar también los links en el componente padre
      const newPropsLinks = [...newData, ...this.props.shareLinks];
      this.props.setShareLinks(newPropsLinks);
    }
  }

  setMode = (mode) => {
    this.setState({ mode: mode });
  }

  toggleSelectAllLinks = (isSelected) => {
    const { shareLinks: links } = this.state;
    this.setState({
      shareLinks: links.map(item => {
        item.isSelected = isSelected;
        return item;
      })
    });
  }

  toggleSelectLink = (link, isSelected) => {
    const { shareLinks: links } = this.state;
    this.setState({
      shareLinks: links.map(item => {
        if (item.token == link.token) {
          item.isSelected = isSelected;
        }
        return item;
      })
    });
  }

  render() {
    if (this.state.isLoading) {
      return <Loading />;
    }

    const { repoID, itemPath, userPerm } = this.props;
    const { mode, shareLinks, sharedLinkInfo, permissionOptions, currentPermission } = this.state;

    switch (mode) {
      case 'displayLinkDetails':
        return (
          <LinkDetails
            sharedLinkInfo={sharedLinkInfo}
            permissionOptions={permissionOptions}
            defaultExpireDays={this.defaultExpireDays}
            showLinkDetails={this.showLinkDetails}
            updateLink={this.updateLink}
            deleteLink={this.deleteLink}
            closeShareDialog={this.props.closeShareDialog}
          />
        );
      case 'singleLinkCreation':
        return (
          <LinkCreation
            type="single"
            repoID={repoID}
            itemPath={itemPath}
            userPerm={userPerm}
            permissionOptions={permissionOptions}
            currentPermission={currentPermission}
            setMode={this.setMode}
            updateAfterCreation={this.updateAfterCreation}
          />
        );
      case 'linksCreation':
        return (
          <LinkCreation
            type="batch"
            repoID={repoID}
            itemPath={itemPath}
            userPerm={userPerm}
            permissionOptions={permissionOptions}
            currentPermission={currentPermission}
            setMode={this.setMode}
            updateAfterCreation={this.updateAfterCreation}
          />
        );
      default:
        return (
          <LinkList
            shareLinks={shareLinks}
            permissionOptions={permissionOptions}
            setMode={this.setMode}
            showLinkDetails={this.showLinkDetails}
            toggleSelectAllLinks={this.toggleSelectAllLinks}
            toggleSelectLink={this.toggleSelectLink}
            deleteShareLinks={this.deleteShareLinks}
            deleteLink={this.deleteLink}
            isLoadingMore={this.state.isLoadingMore}
            handleScroll={() => { }}
            hideBatchButton={this.props.hideBatchButton}
          />
        );
    }
  }
}

ShareLinkPanel.propTypes = propTypes;

export default ShareLinkPanel;
