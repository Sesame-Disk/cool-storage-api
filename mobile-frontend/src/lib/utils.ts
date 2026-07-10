const IMAGE_EXTS = ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'svg', 'webp', 'ico', 'heic', 'heif', 'avif'];
const VIDEO_EXTS = ['mp4', 'webm', 'mov', 'avi', 'mkv'];
const AUDIO_EXTS = ['mp3', 'wav', 'ogg', 'm4a', 'flac', 'aac'];
const MARKDOWN_EXTS = ['md', 'markdown'];
const TEXT_EXTS = ['txt', 'json', 'yml', 'yaml', 'xml', 'csv', 'log', 'ini', 'toml', 'conf', 'cfg'];
const CODE_EXTS = [
  'js', 'ts', 'jsx', 'tsx', 'py', 'go', 'java', 'c', 'cpp', 'h', 'hpp',
  'rs', 'sh', 'bash', 'zsh', 'rb', 'php', 'swift', 'kt', 'scala', 'r',
  'sql', 'html', 'css', 'scss', 'less', 'vue', 'svelte', 'lua', 'pl',
  'ex', 'exs', 'erl', 'hs', 'clj', 'dart', 'zig', 'nim', 'v',
  'makefile', 'dockerfile', 'cmake',
];
const PDF_EXTS = ['pdf'];

export type FileViewerType =
  | 'image'
  | 'video'
  | 'audio'
  | 'markdown'
  | 'text'
  | 'code'
  | 'pdf'
  | 'generic';

export function getFileExtension(filename: string): string {
  const parts = filename.split('.');
  if (parts.length < 2) return '';
  return parts[parts.length - 1].toLowerCase();
}

export function getViewerType(filename: string): FileViewerType {
  const ext = getFileExtension(filename);
  if (!ext) return 'generic';
  if (IMAGE_EXTS.includes(ext)) return 'image';
  if (VIDEO_EXTS.includes(ext)) return 'video';
  if (AUDIO_EXTS.includes(ext)) return 'audio';
  if (MARKDOWN_EXTS.includes(ext)) return 'markdown';
  if (PDF_EXTS.includes(ext)) return 'pdf';
  if (CODE_EXTS.includes(ext)) return 'code';
  if (TEXT_EXTS.includes(ext)) return 'text';
  return 'generic';
}

export function isImageFile(filename: string): boolean {
  return IMAGE_EXTS.includes(getFileExtension(filename));
}

export function isVideoFile(filename: string): boolean {
  return VIDEO_EXTS.includes(getFileExtension(filename));
}

export function isAudioFile(filename: string): boolean {
  return AUDIO_EXTS.includes(getFileExtension(filename));
}

export function getVideoMimeType(filename: string): string {
  const ext = getFileExtension(filename);
  const mimeMap: Record<string, string> = {
    mp4: 'video/mp4',
    webm: 'video/webm',
  };
  return mimeMap[ext] || 'video/mp4';
}

export function getAudioMimeType(filename: string): string {
  const ext = getFileExtension(filename);
  const mimeMap: Record<string, string> = {
    mp3: 'audio/mpeg',
    wav: 'audio/wav',
    ogg: 'audio/ogg',
    m4a: 'audio/mp4',
    flac: 'audio/flac',
    aac: 'audio/aac',
  };
  return mimeMap[ext] || 'audio/mpeg';
}
