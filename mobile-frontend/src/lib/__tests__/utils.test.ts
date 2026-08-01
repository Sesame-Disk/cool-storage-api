/// <reference types="vitest/globals" />
import { getFileExtension, getViewerType, isImageFile, isVideoFile, getVideoMimeType, isAudioFile, getAudioMimeType } from '../utils';

describe('getFileExtension', () => {
  it('returns extension for normal file', () => {
    expect(getFileExtension('photo.jpg')).toBe('jpg');
  });

  it('returns last extension for multiple dots', () => {
    expect(getFileExtension('archive.tar.gz')).toBe('gz');
  });

  it('returns empty string for no extension', () => {
    expect(getFileExtension('README')).toBe('');
  });

  it('lowercases extension', () => {
    expect(getFileExtension('File.PNG')).toBe('png');
  });
});

describe('getViewerType', () => {
  it('returns image for image files', () => {
    expect(getViewerType('photo.jpg')).toBe('image');
    expect(getViewerType('icon.svg')).toBe('image');
    expect(getViewerType('pic.webp')).toBe('image');
  });

  it('returns video for video files', () => {
    expect(getViewerType('clip.mp4')).toBe('video');
    expect(getViewerType('movie.webm')).toBe('video');
  });

  it('returns pdf for pdf files', () => {
    expect(getViewerType('doc.pdf')).toBe('pdf');
  });

  it('returns code for code files', () => {
    expect(getViewerType('app.ts')).toBe('code');
    expect(getViewerType('main.go')).toBe('code');
    expect(getViewerType('style.css')).toBe('code');
  });

  it('returns text for text files', () => {
    expect(getViewerType('notes.txt')).toBe('text');
    expect(getViewerType('data.json')).toBe('text');
  });

  it('returns markdown for markdown files', () => {
    expect(getViewerType('readme.md')).toBe('markdown');
    expect(getViewerType('NOTES.markdown')).toBe('markdown');
  });

  it('returns audio for audio files', () => {
    expect(getViewerType('song.mp3')).toBe('audio');
    expect(getViewerType('voice.wav')).toBe('audio');
    expect(getViewerType('sound.ogg')).toBe('audio');
    expect(getViewerType('clip.m4a')).toBe('audio');
    expect(getViewerType('track.flac')).toBe('audio');
    expect(getViewerType('beep.aac')).toBe('audio');
  });

  it('returns office for OnlyOffice document types', () => {
    expect(getViewerType('report.docx')).toBe('office');
    expect(getViewerType('legacy.doc')).toBe('office');
    expect(getViewerType('budget.xlsx')).toBe('office');
    expect(getViewerType('sheet.xls')).toBe('office');
    expect(getViewerType('deck.pptx')).toBe('office');
    expect(getViewerType('slides.ppt')).toBe('office');
    expect(getViewerType('notes.odt')).toBe('office');
    expect(getViewerType('data.ods')).toBe('office');
    expect(getViewerType('talk.odp')).toBe('office');
  });

  it('keeps native viewers for formats with a dedicated mobile viewer', () => {
    // pdf/csv/txt/html have first-class mobile viewers and must NOT route to
    // OnlyOffice even though the backend can also render them.
    expect(getViewerType('doc.pdf')).toBe('pdf');
    expect(getViewerType('table.csv')).toBe('text');
    expect(getViewerType('page.html')).toBe('code');
  });

  it('returns generic for unknown extensions', () => {
    expect(getViewerType('file.xyz')).toBe('generic');
  });

  it('returns generic for no extension', () => {
    expect(getViewerType('Makefile')).toBe('generic');
  });
});

describe('isImageFile', () => {
  it('returns true for image files', () => {
    expect(isImageFile('photo.png')).toBe(true);
    expect(isImageFile('icon.gif')).toBe(true);
  });

  it('returns false for non-image files', () => {
    expect(isImageFile('doc.pdf')).toBe(false);
    expect(isImageFile('app.js')).toBe(false);
  });
});

describe('isVideoFile', () => {
  it('returns true for video files', () => {
    expect(isVideoFile('clip.mp4')).toBe(true);
    expect(isVideoFile('movie.webm')).toBe(true);
  });

  it('returns false for non-video files', () => {
    expect(isVideoFile('photo.jpg')).toBe(false);
  });
});

describe('getVideoMimeType', () => {
  it('returns correct mime types', () => {
    expect(getVideoMimeType('clip.mp4')).toBe('video/mp4');
    expect(getVideoMimeType('clip.webm')).toBe('video/webm');
  });

  it('defaults to video/mp4 for unknown', () => {
    expect(getVideoMimeType('clip.avi')).toBe('video/mp4');
  });
});

describe('getAudioMimeType', () => {
  it('returns correct mime types', () => {
    expect(getAudioMimeType('song.mp3')).toBe('audio/mpeg');
    expect(getAudioMimeType('voice.wav')).toBe('audio/wav');
    expect(getAudioMimeType('sound.ogg')).toBe('audio/ogg');
    expect(getAudioMimeType('clip.m4a')).toBe('audio/mp4');
    expect(getAudioMimeType('track.flac')).toBe('audio/flac');
    expect(getAudioMimeType('beep.aac')).toBe('audio/aac');
  });

  it('defaults to audio/mpeg for unknown', () => {
    expect(getAudioMimeType('mystery.xyz')).toBe('audio/mpeg');
  });
});

describe('isAudioFile', () => {
  it('returns true for audio files', () => {
    expect(isAudioFile('song.mp3')).toBe(true);
    expect(isAudioFile('track.flac')).toBe(true);
  });

  it('returns false for non-audio files', () => {
    expect(isAudioFile('clip.mp4')).toBe(false);
    expect(isAudioFile('photo.jpg')).toBe(false);
  });
});
