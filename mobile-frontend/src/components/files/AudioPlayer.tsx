import React from 'react';
import { X, Download, Music } from 'lucide-react';
import { downloadFile } from '../../lib/share';
import { getAudioMimeType } from '../../lib/utils';

interface AudioPlayerProps {
  url: string;
  fileName: string;
  onClose: () => void;
}

export default function AudioPlayer({ url, fileName, onClose }: AudioPlayerProps) {
  const mimeType = getAudioMimeType(fileName);

  return (
    <div className="fixed inset-0 z-[60] bg-black flex flex-col" data-testid="audio-player">
      {/* Top bar */}
      <div className="flex items-center justify-between p-2">
        <button
          onClick={onClose}
          className="min-h-[44px] min-w-[44px] flex items-center justify-center text-white"
          aria-label="Close"
        >
          <X className="w-6 h-6" />
        </button>
        <p className="text-white text-sm truncate mx-2 flex-1 text-center">{fileName}</p>
        <button
          onClick={() => downloadFile(url, fileName)}
          className="min-h-[44px] min-w-[44px] flex items-center justify-center text-white"
          aria-label="Download"
        >
          <Download className="w-5 h-5" />
        </button>
      </div>

      {/* Audio */}
      <div className="flex-1 flex flex-col items-center justify-center gap-8 p-6">
        <div className="flex flex-col items-center gap-4 text-white/80">
          <Music className="w-24 h-24" />
          <p className="text-sm truncate max-w-[80vw] text-center">{fileName}</p>
        </div>
        <audio
          controls
          className="w-full max-w-md"
          data-testid="audio-element"
        >
          <source src={url} type={mimeType} />
          Your browser does not support audio playback.
        </audio>
      </div>
    </div>
  );
}
