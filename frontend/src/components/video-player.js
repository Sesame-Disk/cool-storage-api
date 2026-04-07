import React from 'react';

class VideoPlayer extends React.Component {
  constructor(props) {
    super(props);
    this.state = {
      nativeVideoIsPortrait: false
    };
  }

  componentDidUpdate(prevProps) {
    if (prevProps.sources !== this.props.sources) {
      if (this.state.nativeVideoIsPortrait) {
        this.setState({ nativeVideoIsPortrait: false });
      }
    }
  }

  render() {
    const videoProps = { ...this.props };
    const sources = videoProps.sources || [];
    const className = videoProps.className || 'video-js';
    const controls = videoProps.controls !== undefined ? videoProps.controls : true;
    const autoPlay = videoProps.autoPlay !== undefined ? videoProps.autoPlay : false;
    const preload = videoProps.preload || 'metadata';
    const playsInline = videoProps.playsInline !== undefined ? videoProps.playsInline : true;
    delete videoProps.sources;
    delete videoProps.className;
    delete videoProps.controls;
    delete videoProps.autoPlay;
    delete videoProps.preload;
    delete videoProps.playsInline;
    delete videoProps.fallbackToNative;
    delete videoProps.playbackRates;
    delete videoProps.preferNative;

    const nativePlayerStyle = this.state.nativeVideoIsPortrait
      ? {
        width: 'auto',
        maxWidth: 'min(100%, 28rem)',
        maxHeight: '70vh',
        margin: '0 auto',
        display: 'block',
        backgroundColor: '#000'
      }
      : {
        maxHeight: '70vh',
        margin: '0 auto',
        display: 'block',
        backgroundColor: '#000'
      };

    return (
      <video
        className={className}
        controls={controls}
        autoPlay={autoPlay}
        playsInline={playsInline}
        preload={preload}
        onLoadedMetadata={this.handleNativeMetadata}
        style={nativePlayerStyle}
        {...videoProps}
      >
        {sources.map((source, index) => (
          <source key={`${source.src || 'source'}-${index}`} src={source.src} type={source.type} />
        ))}
        Your browser does not support video playback.
      </video>
    );
  }

  handleNativeMetadata = (event) => {
    const { videoWidth, videoHeight } = event.currentTarget;
    const nativeVideoIsPortrait = Boolean(videoWidth && videoHeight && videoHeight > videoWidth);
    if (nativeVideoIsPortrait !== this.state.nativeVideoIsPortrait) {
      this.setState({ nativeVideoIsPortrait });
    }
  };
}

export default VideoPlayer;
