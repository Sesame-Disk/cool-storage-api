/* eslint-disable */
import React from 'react';
import PropTypes from 'prop-types';
import { Modal, ModalHeader, ModalBody, ModalFooter, Button, Badge, Input } from 'reactstrap';
import copy from '../components/copy-to-clipboard';
import { gettext } from '../utils/constants';
import toaster from '../components/toast';
import { changeLinkToChina } from './links';

const propTypes = {
    link: PropTypes.string.isRequired,
    password: PropTypes.string,
    viewCount: PropTypes.number,
    isShareLink: PropTypes.bool, // default: false (upload link)
    toggleDialog: PropTypes.func.isRequired
};

class ShareAdminLinkEnhanced extends React.Component {

    constructor(props) {
        super(props);
        this.state = {
            showPassword: false,
            showChinaInfo: false
        };
    }

    togglePasswordVisibility = () => {
        this.setState({ showPassword: !this.state.showPassword });
    };

    toggleChinaInfo = () => {
        this.setState({ showChinaInfo: !this.state.showChinaInfo });
    };

    copyLink = () => {
        copy(this.props.link);
        toaster.success(gettext('Link copied to clipboard.'), { duration: 2 });
    };

    copyPassword = () => {
        if (this.props.password) {
            copy(this.props.password);
            toaster.success(gettext('Password copied to clipboard.'), { duration: 2 });
        }
    };

    copyLinkAndPassword = () => {
        const { link, password } = this.props;
        const text = password
            ? `${gettext('Link')}: ${link}\n${gettext('Password')}: ${password}`
            : link;
        copy(text);
        toaster.success(gettext('Link and password copied to clipboard.'), { duration: 2 });
    };

    render() {
        const { password, viewCount, toggleDialog } = this.props;
        const isShareLink = this.props.isShareLink || false; // default: false (upload link)
        const { showPassword, showChinaInfo } = this.state;
        const hasPassword = password && password.length > 0;

        // Transform link to China domain if it has password (share links) or if it's an upload link
        let link = this.props.link;
        if (hasPassword || !isShareLink) {
            link = changeLinkToChina(link);
        }

        return (
            <Modal isOpen={true} toggle={toggleDialog} size="lg">
                <ModalHeader toggle={toggleDialog}>
                    {gettext('Share Link Details')}
                    {' '}
                    {hasPassword ? (
                        <Badge color="warning" className="ml-2">
                            <i className="fas fa-lock"></i> {gettext('Protected')}
                        </Badge>
                    ) : (
                        <Badge color="success" className="ml-2">
                            <i className="fas fa-globe"></i> {gettext('Public')}
                        </Badge>
                    )}
                </ModalHeader>
                <ModalBody>
                    {/* Link Section */}
                    <div className="mb-4">
                        <h6 className="text-secondary mb-2">
                            <i className="fas fa-link"></i> {gettext('Link')}
                        </h6>
                        <div className="d-flex align-items-center">
                            <Input
                                type="text"
                                value={link}
                                readOnly
                                className="flex-grow-1"
                                style={{ fontFamily: 'monospace', fontSize: '14px' }}
                            />
                            <Button
                                color="primary"
                                size="sm"
                                className="ml-2 px-3"
                                onClick={this.copyLink}
                                style={{ whiteSpace: 'nowrap' }}
                            >
                                <i className="fas fa-copy"></i> {gettext('Copy')}
                            </Button>
                        </div>
                    </div>

                    {/* Password Section */}
                    {hasPassword && (
                        <div className="mb-4">
                            <h6 className="text-secondary mb-2">
                                <i className="fas fa-key"></i> {gettext('Password')}
                            </h6>
                            <div className="d-flex align-items-center">
                                <Input
                                    type={showPassword ? 'text' : 'password'}
                                    value={password}
                                    readOnly
                                    className="flex-grow-1"
                                    style={{ fontFamily: 'monospace', fontSize: '14px' }}
                                />
                                <Button
                                    color="secondary"
                                    size="sm"
                                    className="ml-2 px-3"
                                    onClick={this.togglePasswordVisibility}
                                    style={{ whiteSpace: 'nowrap' }}
                                >
                                    <i className={`fas fa-eye${showPassword ? '-slash' : ''}`}></i> {showPassword ? gettext('Hide') : gettext('Show')}
                                </Button>
                                <Button
                                    color="primary"
                                    size="sm"
                                    className="ml-2 px-3"
                                    onClick={this.copyPassword}
                                    style={{ whiteSpace: 'nowrap' }}
                                >
                                    <i className="fas fa-copy"></i> {gettext('Copy')}
                                </Button>
                            </div>
                        </div>
                    )}

                    {/* China Info Section */}
                    <div className="border-top pt-3">
                        <div className="d-flex justify-content-between align-items-start">
                            <div className="flex-grow-1">
                                {isShareLink && hasPassword ? (
                                    // Share links CON password: válidos para China
                                    <div className="text-muted small">
                                        <i className="fas fa-globe text-success"></i>{' '}
                                        <span style={{ opacity: 0.75 }}>
                                            {gettext('Valid worldwide, including users in China (no VPN needed).')}
                                        </span>
                                        <button
                                            onClick={this.toggleChinaInfo}
                                            className="btn btn-link btn-sm p-0 ml-2"
                                            style={{ fontSize: '12px', textDecoration: 'none', opacity: 0.6 }}
                                        >
                                            {showChinaInfo ? '▼' : '▶'}
                                        </button>
                                        {showChinaInfo && (
                                            <div className="mt-2 p-2 bg-light rounded" style={{ fontSize: '12px', opacity: 0.8 }}>
                                                {gettext('This link uses a China-accessible domain and works worldwide without VPN. Password protection is required for China accessibility.')}
                                            </div>
                                        )}
                                    </div>
                                ) : isShareLink && !hasPassword ? (
                                    // Share links SIN password: NO válidos para China
                                    <div className="text-muted small">
                                        <i className="fas fa-exclamation-triangle text-warning"></i>{' '}
                                        <span style={{ opacity: 0.75 }}>
                                            {gettext('Valid worldwide except in China (VPN required in China). Add password to enable China access.')}
                                        </span>
                                        <button
                                            onClick={this.toggleChinaInfo}
                                            className="btn btn-link btn-sm p-0 ml-2"
                                            style={{ fontSize: '12px', textDecoration: 'none', opacity: 0.6 }}
                                        >
                                            {showChinaInfo ? '▼' : '▶'}
                                        </button>
                                        {showChinaInfo && (
                                            <div className="mt-2 p-2 bg-light rounded" style={{ fontSize: '12px', opacity: 0.8 }}>
                                                {gettext('This link uses the standard domain (app.nihaocloud.com) which is blocked in mainland China. To make it accessible in China without VPN, create a password-protected link using the "Share in China" section.')}
                                            </div>
                                        )}
                                    </div>
                                ) : (
                                    // Upload links: siempre válidos para China
                                    <div className="text-muted small">
                                        <i className="fas fa-globe text-success"></i>{' '}
                                        <span style={{ opacity: 0.75 }}>
                                            {gettext('Valid worldwide, including users in China (no VPN needed).')}
                                        </span>
                                    </div>
                                )}
                            </div>
                            {viewCount !== undefined && (
                                <div className="text-muted small ml-3" style={{ whiteSpace: 'nowrap' }}>
                                    <i className="fas fa-eye"></i> {viewCount}
                                </div>
                            )}
                        </div>
                    </div>
                </ModalBody>
                <ModalFooter>
                    {hasPassword && (
                        <Button color="success" onClick={this.copyLinkAndPassword} className="px-4">
                            <i className="fas fa-clipboard-check"></i> {gettext('Copy Link & Password')}
                        </Button>
                    )}
                    <Button color="secondary" onClick={toggleDialog} className="px-4">
                        {gettext('Close')}
                    </Button>
                </ModalFooter>
            </Modal>
        );
    }
}

ShareAdminLinkEnhanced.propTypes = propTypes;

export default ShareAdminLinkEnhanced;
