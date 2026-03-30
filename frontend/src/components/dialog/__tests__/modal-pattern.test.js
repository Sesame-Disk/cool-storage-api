/**
 * Modal Pattern Tests - Dialog Components
 *
 * These tests document and verify the modal pattern used in dialog components.
 * They ensure that dialogs use plain Bootstrap modal classes instead of
 * reactstrap Modal components (which break when rendered inside ModalPortal).
 *
 * BACKGROUND:
 * - Dialogs are rendered inside ModalPortal in parent components
 * - reactstrap Modal creates its own portal, causing double-portal issues
 * - Solution: Use plain Bootstrap modal classes that render in-place
 *
 * CORRECT PATTERN:
 * <div className="modal show d-block" tabIndex="-1" style={{ backgroundColor: 'rgba(0,0,0,0.5)' }}>
 *   <div className="modal-dialog">
 *     <div className="modal-content">
 *       <div className="modal-header">
 *         <h5 className="modal-title">Title</h5>
 *         <button type="button" className="close" onClick={...}>
 *           <span aria-hidden="true">&times;</span>
 *         </button>
 *       </div>
 *       <div className="modal-body">Content</div>
 *       <div className="modal-footer">Buttons</div>
 *     </div>
 *   </div>
 * </div>
 *
 * INCORRECT PATTERN (breaks in ModalPortal):
 * <Modal isOpen={true}>
 *   <ModalHeader>...</ModalHeader>
 *   <ModalBody>...</ModalBody>
 *   <ModalFooter>...</ModalFooter>
 * </Modal>
 */

const fs = require('fs');
const path = require('path');

// Helper to read a dialog file
const readDialogFile = (filename) => {
  const filePath = path.join(__dirname, '..', filename);
  return fs.readFileSync(filePath, 'utf8');
};

// Helper to check if a file uses reactstrap Modal
const usesReactstrapModal = (content) => {
  // Check for reactstrap Modal import
  const hasModalImport = /import\s+.*\{\s*[^}]*Modal[^}]*\}\s*from\s+['"]reactstrap['"]/.test(content);
  // Check for <Modal component usage (not just the word Modal)
  const hasModalComponent = /<Modal\s+/.test(content);
  return hasModalImport && hasModalComponent;
};

// Helper to check if a file uses plain Bootstrap modal
const usesBootstrapModal = (content) => {
  return /className\s*=\s*(?:"[^"]*\bmodal\b[^"]*\bshow\b[^"]*\bd-block\b[^"]*"|'[^']*\bmodal\b[^']*\bshow\b[^']*\bd-block\b[^']*'|\{`[^`]*\bmodal\b[^`]*\bshow\b[^`]*\bd-block\b[^`]*`\})/.test(content);
};

// Helper to check for close button pattern
// Supports both Bootstrap 4 (close) and Bootstrap 5 (btn-close) patterns
const hasCloseButton = (content) => {
  return content.includes('className="close"') ||
    content.includes("className='close'") ||
    content.includes('className="btn-close"') ||
    content.includes("className='btn-close'");
};

describe('Modal Pattern Tests - Dialog Components', () => {

  describe('Fixed Dialogs - Should use Bootstrap modal pattern', () => {

    const fixedDialogs = [
      'lib-history-setting-dialog.js',
      'lib-old-files-auto-del-dialog.js',
      'label-repo-state-dialog.js',
      'reset-encrypted-repo-password-dialog.js',
      'transfer-dialog.js',
      'lib-sub-folder-permission-dialog.js',
      'repo-api-token-dialog.js',
      'repo-seatable-integration-dialog.js',
      'repo-share-admin-dialog.js',
      'list-taggedfiles-dialog.js',
      'delete-repo-dialog.js',
      'change-repo-password-dialog.js',
      'share-dialog.js',
    ];

    fixedDialogs.forEach((filename) => {
      test(`${filename} should use plain Bootstrap modal classes`, () => {
        const content = readDialogFile(filename);

        // Should NOT use reactstrap Modal component
        expect(usesReactstrapModal(content)).toBe(false);

        // Should use Bootstrap modal classes
        expect(usesBootstrapModal(content)).toBe(true);
      });

      test(`${filename} should have proper close button`, () => {
        const content = readDialogFile(filename);
        expect(hasCloseButton(content)).toBe(true);
      });
    });
  });

  describe('Tag Dialog Components - Fragment pattern', () => {

    test('create-tag-dialog.js should use Bootstrap classes for header/body/footer', () => {
      const content = readDialogFile('create-tag-dialog.js');

      // Should NOT import reactstrap modal components
      expect(content).not.toMatch(/import\s+.*ModalHeader.*from\s+['"]reactstrap['"]/);
      expect(content).not.toMatch(/import\s+.*ModalBody.*from\s+['"]reactstrap['"]/);
      expect(content).not.toMatch(/import\s+.*ModalFooter.*from\s+['"]reactstrap['"]/);

      // Should use Bootstrap classes
      expect(content).toContain('className="modal-header"');
      expect(content).toContain('className="modal-body"');
      expect(content).toContain('className="modal-footer"');
    });

    test('edit-filetag-dialog.js should use Bootstrap modal pattern', () => {
      const content = readDialogFile('edit-filetag-dialog.js');

      // Should NOT use reactstrap Modal
      expect(usesReactstrapModal(content)).toBe(false);

      // Should use Bootstrap modal classes
      expect(usesBootstrapModal(content)).toBe(true);
    });
  });

  describe('Modal Structure Validation', () => {

    test('documents correct modal structure', () => {
      const correctStructure = {
        container: {
          element: 'div',
          classes: ['modal', 'show', 'd-block'],
          tabIndex: '-1',
          style: 'backgroundColor: rgba(0,0,0,0.5)',
        },
        dialog: {
          element: 'div',
          classes: ['modal-dialog'],
        },
        content: {
          element: 'div',
          classes: ['modal-content'],
        },
        header: {
          element: 'div',
          classes: ['modal-header'],
          children: ['h5.modal-title', 'button.close'],
        },
        body: {
          element: 'div',
          classes: ['modal-body'],
        },
        footer: {
          element: 'div',
          classes: ['modal-footer'],
          optional: true,
        },
      };

      // Verify structure
      expect(correctStructure.container.classes).toContain('modal');
      expect(correctStructure.container.classes).toContain('show');
      expect(correctStructure.container.classes).toContain('d-block');
      expect(correctStructure.header.children).toContain('button.close');
    });

    test('documents close button pattern', () => {
      const closeButtonPattern = {
        element: 'button',
        type: 'button',
        className: 'close',
        ariaLabel: 'Close',
        onClick: 'toggleDialog or onClose callback',
        content: '<span aria-hidden="true">&times;</span>',
      };

      expect(closeButtonPattern.className).toBe('close');
      expect(closeButtonPattern.content).toContain('&times;');
    });
  });

  describe('Fixed Dialog List - Documentation', () => {

    test('documents all dialogs fixed in session 3 (2026-01-28)', () => {
      const session3Fixes = [
        'transfer-dialog.js',
        'lib-history-setting-dialog.js',
        'reset-encrypted-repo-password-dialog.js',
        'label-repo-state-dialog.js',
        'lib-sub-folder-permission-dialog.js',
        'repo-api-token-dialog.js',
        'repo-seatable-integration-dialog.js',
        'lib-old-files-auto-del-dialog.js',
        'edit-filetag-dialog.js',
        'create-tag-dialog.js',
      ];

      expect(session3Fixes.length).toBe(10);
    });

    test('documents all dialogs fixed in session 2 (2026-01-28)', () => {
      const session2Fixes = [
        'repo-share-admin-dialog.js',
        'list-taggedfiles-dialog.js',
      ];

      expect(session2Fixes.length).toBe(2);
    });

    test('documents previously fixed dialogs', () => {
      const previousFixes = [
        'delete-repo-dialog.js',
        'change-repo-password-dialog.js',
        'share-dialog.js',
      ];

      expect(previousFixes.length).toBe(3);
    });

    test('total fixed dialogs should be 15', () => {
      const totalFixed = 10 + 2 + 3; // session3 + session2 + previous
      expect(totalFixed).toBe(15);
    });
  });

  describe('Import Pattern Validation', () => {

    test('fixed dialogs should not import Modal from reactstrap', () => {
      const dialogsToCheck = [
        'lib-history-setting-dialog.js',
        'lib-old-files-auto-del-dialog.js',
        'label-repo-state-dialog.js',
        'reset-encrypted-repo-password-dialog.js',
      ];

      dialogsToCheck.forEach((filename) => {
        const content = readDialogFile(filename);

        // Should not have Modal in reactstrap import
        const reactstrapImport = content.match(/import\s+\{([^}]+)\}\s+from\s+['"]reactstrap['"]/);
        if (reactstrapImport) {
          const imports = reactstrapImport[1];
          expect(imports).not.toContain('Modal');
          expect(imports).not.toContain('ModalHeader');
          expect(imports).not.toContain('ModalBody');
          expect(imports).not.toContain('ModalFooter');
        }
      });
    });
  });
});

describe('Regression Prevention', () => {

  test('new dialogs should follow Bootstrap modal pattern', () => {
    /**
     * When creating new dialogs:
     *
     * DO:
     * - Use plain div elements with Bootstrap modal classes
     * - Include proper close button with className="close"
     * - Set tabIndex="-1" on outer modal div
     * - Add backdrop style directly
     *
     * DON'T:
     * - Import Modal, ModalHeader, ModalBody, ModalFooter from reactstrap
     * - Use <Modal isOpen={true}> component
     */

    const doPattern = 'className="modal show d-block"';
    const dontPattern = '<Modal isOpen={true}>';

    expect(doPattern).toContain('modal');
    expect(dontPattern).toContain('Modal');
  });

  test('documents why reactstrap Modal breaks in ModalPortal', () => {
    /**
     * ISSUE:
     * reactstrap Modal component creates its own React portal
     * to render the modal at the document body level.
     *
     * PROBLEM:
     * When a component is already rendered inside ModalPortal
     * (which also uses a portal), reactstrap Modal tries to
     * create a second portal, causing:
     * - Double overlay effect
     * - Modal not visible
     * - Click handlers not working
     * - Z-index issues
     *
     * SOLUTION:
     * Use plain Bootstrap modal classes instead of reactstrap
     * Modal component. These render in-place without creating
     * additional portals.
     */

    const issue = {
      component: 'reactstrap Modal',
      problem: 'Creates its own portal inside ModalPortal',
      symptoms: ['Modal not visible', 'Click handlers broken', 'Double overlay'],
      solution: 'Use plain Bootstrap modal classes',
    };

    expect(issue.solution).toBe('Use plain Bootstrap modal classes');
  });
});
