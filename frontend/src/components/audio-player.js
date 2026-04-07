import React from 'react';

class AudioPlayer extends React.Component {
  render() {
    const audioProps = { ...this.props };
    const sources = audioProps.sources || [];
    const className = audioProps.className || 'video-js vjs-has-started';
    const controls = audioProps.controls !== undefined ? audioProps.controls : true;
    const autoPlay = audioProps.autoPlay !== undefined ? audioProps.autoPlay : false;
    const preload = audioProps.preload || 'metadata';
    delete audioProps.sources;
    delete audioProps.className;
    delete audioProps.controls;
    delete audioProps.autoPlay;
    delete audioProps.preload;
    delete audioProps.fallbackToNative;
    delete audioProps.playbackRates;
    delete audioProps.preferNative;

    return (
      <audio className={className} controls={controls} autoPlay={autoPlay} preload={preload} {...audioProps}>
        {sources.map((source, index) => (
          <source key={`${source.src || 'source'}-${index}`} src={source.src} type={source.type} />
        ))}
        Your browser does not support audio playback.
      </audio>
    );
  }
}

export default AudioPlayer;
