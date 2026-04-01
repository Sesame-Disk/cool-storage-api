import { Utils } from '../utils';

describe('Utils', () => {
  describe('bytesToSize', () => {
    test('returns empty string for undefined', () => {
      expect(Utils.bytesToSize(undefined)).toBe(' ');
    });

    test('returns -- for negative values', () => {
      expect(Utils.bytesToSize(-1)).toBe('--');
    });

    test('returns 0 bytes for zero', () => {
      expect(Utils.bytesToSize(0)).toBe('0 bytes');
    });

    test('handles bytes', () => {
      expect(Utils.bytesToSize(500)).toBe('500 bytes');
    });

    test('handles kilobytes', () => {
      expect(Utils.bytesToSize(1024)).toBe('1.0 KB');
      expect(Utils.bytesToSize(1500)).toBe('1.5 KB');
    });

    test('handles megabytes', () => {
      expect(Utils.bytesToSize(1000000)).toBe('1.0 MB');
      expect(Utils.bytesToSize(5500000)).toBe('5.5 MB');
    });

    test('handles gigabytes', () => {
      expect(Utils.bytesToSize(1000000000)).toBe('1.0 GB');
    });

    test('handles terabytes', () => {
      expect(Utils.bytesToSize(1000000000000)).toBe('1.0 TB');
    });
  });

  describe('getFileName', () => {
    test('extracts filename from path', () => {
      expect(Utils.getFileName('/path/to/file.txt')).toBe('file.txt');
    });

    test('handles nested paths', () => {
      expect(Utils.getFileName('/a/b/c/d/e.pdf')).toBe('e.pdf');
    });

    test('handles root level files', () => {
      expect(Utils.getFileName('/file.txt')).toBe('file.txt');
    });
  });

  describe('encodePath', () => {
    test('encodes special characters', () => {
      expect(Utils.encodePath('/path/with spaces/file.txt')).toBe('/path/with%20spaces/file.txt');
    });

    test('handles empty path', () => {
      expect(Utils.encodePath('')).toBe('');
    });

    test('handles null/undefined', () => {
      expect(Utils.encodePath(null)).toBe('');
      expect(Utils.encodePath(undefined)).toBe('');
    });

    test('preserves slashes', () => {
      expect(Utils.encodePath('/a/b/c')).toBe('/a/b/c');
    });
  });

  describe('getPaths', () => {
    test('returns array of cumulative paths', () => {
      expect(Utils.getPaths('/abc/bc/cb')).toEqual(['/abc', '/abc/bc', '/abc/bc/cb']);
    });

    test('handles single level path', () => {
      expect(Utils.getPaths('/folder')).toEqual(['/folder']);
    });

    test('handles two level path', () => {
      expect(Utils.getPaths('/a/b')).toEqual(['/a', '/a/b']);
    });
  });

  describe('isEditableOfficeFile', () => {
    test('returns true for docx', () => {
      expect(Utils.isEditableOfficeFile('document.docx')).toBe(true);
    });

    test('returns true for pptx', () => {
      expect(Utils.isEditableOfficeFile('presentation.pptx')).toBe(true);
    });

    test('returns true for xlsx', () => {
      expect(Utils.isEditableOfficeFile('spreadsheet.xlsx')).toBe(true);
    });

    test('returns false for doc (old format)', () => {
      expect(Utils.isEditableOfficeFile('document.doc')).toBe(false);
    });

    test('returns false for txt', () => {
      expect(Utils.isEditableOfficeFile('readme.txt')).toBe(false);
    });

    test('returns false for files without extension', () => {
      expect(Utils.isEditableOfficeFile('noextension')).toBe(false);
    });

    test('is case insensitive', () => {
      expect(Utils.isEditableOfficeFile('DOCUMENT.DOCX')).toBe(true);
    });
  });

  describe('videoCheck', () => {
    test('returns true for mp4', () => {
      expect(Utils.videoCheck('video.mp4')).toBe(true);
    });

    test('returns true for webm', () => {
      expect(Utils.videoCheck('video.webm')).toBe(true);
    });

    test('returns true for mov', () => {
      expect(Utils.videoCheck('video.mov')).toBe(true);
    });

    test('returns false for non-video files', () => {
      expect(Utils.videoCheck('image.png')).toBe(false);
    });

    test('returns false for files without extension', () => {
      expect(Utils.videoCheck('noextension')).toBe(false);
    });
  });

  describe('checkDuplicatedNameInList', () => {
    const list = [
      { name: 'file1.txt' },
      { name: 'file2.txt' },
      { name: 'folder' }
    ];

    test('returns true for existing name', () => {
      expect(Utils.checkDuplicatedNameInList(list, 'file1.txt')).toBe(true);
    });

    test('returns false for non-existing name', () => {
      expect(Utils.checkDuplicatedNameInList(list, 'file3.txt')).toBe(false);
    });

    test('handles empty list', () => {
      expect(Utils.checkDuplicatedNameInList([], 'file.txt')).toBe(false);
    });
  });

  describe('generateDialogTitle', () => {
    test('replaces placeholder with target', () => {
      const title = 'Delete {placeholder}';
      expect(Utils.generateDialogTitle(title, 'file.txt')).toBe('Delete file.txt');
    });

    test('handles special characters in target', () => {
      const title = 'Rename {placeholder}';
      expect(Utils.generateDialogTitle(title, 'file<script>.txt')).toBe('Rename file<script>.txt');
    });
  });

  describe('getShareLinkPermissionStr', () => {
    test('returns preview_download', () => {
      expect(Utils.getShareLinkPermissionStr({
        can_edit: false,
        can_download: true,
        can_upload: false
      })).toBe('preview_download');
    });

    test('returns preview_only', () => {
      expect(Utils.getShareLinkPermissionStr({
        can_edit: false,
        can_download: false,
        can_upload: false
      })).toBe('preview_only');
    });

    test('returns download_upload', () => {
      expect(Utils.getShareLinkPermissionStr({
        can_edit: false,
        can_download: true,
        can_upload: true
      })).toBe('download_upload');
    });

    test('returns edit_download', () => {
      expect(Utils.getShareLinkPermissionStr({
        can_edit: true,
        can_download: true,
        can_upload: false
      })).toBe('edit_download');
    });

    test('returns cloud_edit', () => {
      expect(Utils.getShareLinkPermissionStr({
        can_edit: true,
        can_download: false,
        can_upload: false
      })).toBe('cloud_edit');
    });
  });

  describe('FILEEXT_ICON_MAP', () => {
    test('has text file mappings', () => {
      expect(Utils.FILEEXT_ICON_MAP['txt']).toBe('txt.png');
      expect(Utils.FILEEXT_ICON_MAP['md']).toBe('txt.png');
    });

    test('has document mappings', () => {
      expect(Utils.FILEEXT_ICON_MAP['pdf']).toBe('pdf.png');
      expect(Utils.FILEEXT_ICON_MAP['docx']).toBe('word.png');
      expect(Utils.FILEEXT_ICON_MAP['xlsx']).toBe('excel.png');
      expect(Utils.FILEEXT_ICON_MAP['pptx']).toBe('ppt.png');
    });

    test('has video mappings', () => {
      expect(Utils.FILEEXT_ICON_MAP['mp4']).toBe('video.png');
      expect(Utils.FILEEXT_ICON_MAP['mkv']).toBe('video.png');
    });

    test('has image mappings', () => {
      expect(Utils.FILEEXT_ICON_MAP['jpg']).toBe('pic.png');
      expect(Utils.FILEEXT_ICON_MAP['png']).toBe('pic.png');
    });

    test('has audio mappings', () => {
      expect(Utils.FILEEXT_ICON_MAP['mp3']).toBe('music.png');
      expect(Utils.FILEEXT_ICON_MAP['wav']).toBe('music.png');
    });

    test('has code file mappings', () => {
      expect(Utils.FILEEXT_ICON_MAP['js']).toBe('txt.png');
      expect(Utils.FILEEXT_ICON_MAP['py']).toBe('txt.png');
      expect(Utils.FILEEXT_ICON_MAP['go']).toBe('txt.png');
    });
  });

  describe('setFavicon', () => {
    afterEach(() => {
      document.head.innerHTML = '';
    });

    test('updates existing favicon element', () => {
      document.head.innerHTML = '<link id="favicon" rel="icon" href="/old.png">';

      Utils.setFavicon('/new.png');

      expect(document.getElementById('favicon').getAttribute('href')).toBe('/new.png');
    });

    test('creates favicon element when missing', () => {
      Utils.setFavicon('/created.png');

      const favicon = document.getElementById('favicon');
      expect(favicon).not.toBeNull();
      expect(favicon.getAttribute('href')).toBe('/created.png');
      expect(favicon.getAttribute('rel')).toBe('icon');
    });
  });

  describe('keyCodes', () => {
    test('has standard key codes', () => {
      expect(Utils.keyCodes.enter).toBe(13);
      expect(Utils.keyCodes.esc).toBe(27);
      expect(Utils.keyCodes.space).toBe(32);
      expect(Utils.keyCodes.tab).toBe(9);
      expect(Utils.keyCodes.up).toBe(38);
      expect(Utils.keyCodes.down).toBe(40);
    });
  });
});
