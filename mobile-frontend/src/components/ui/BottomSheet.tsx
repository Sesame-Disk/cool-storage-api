import React from 'react';
import { AnimatePresence, motion } from 'framer-motion';
import type { PanInfo } from 'framer-motion';

interface BottomSheetProps {
  isOpen: boolean;
  onClose: () => void;
  title?: string;
  fullScreen?: boolean;
  children: React.ReactNode;
}

export default function BottomSheet({ isOpen, onClose, title, fullScreen, children }: BottomSheetProps) {
  const handleBackdropClick = (e: React.MouseEvent) => {
    if (e.target === e.currentTarget) onClose();
  };

  const handleDragEnd = (_: unknown, info: PanInfo) => {
    if (info.offset.y > 100 || info.velocity.y > 300) {
      onClose();
    }
  };

  return (
    <AnimatePresence>
      {isOpen && (
        <motion.div
          className="fixed inset-0 z-[60] flex items-end justify-center"
          initial={{ backgroundColor: 'rgba(0,0,0,0)' }}
          animate={{ backgroundColor: 'rgba(0,0,0,0.4)' }}
          exit={{ backgroundColor: 'rgba(0,0,0,0)' }}
          transition={{ duration: 0.2 }}
          onClick={handleBackdropClick}
          data-testid="bottom-sheet-backdrop"
        >
          <motion.div
            className={`w-full max-w-lg bg-white dark:bg-dark-surface rounded-t-2xl ${fullScreen ? 'h-[90vh] flex flex-col' : 'p-6'}`}
            role="dialog"
            aria-label={title}
            initial={{ y: '100%' }}
            animate={{ y: 0 }}
            exit={{ y: '100%' }}
            transition={{ type: 'spring', damping: 25, stiffness: 300 }}
            drag={!fullScreen ? 'y' : undefined}
            dragConstraints={{ top: 0 }}
            dragElastic={0.2}
            onDragEnd={!fullScreen ? handleDragEnd : undefined}
          >
            {!fullScreen && <div className="w-10 h-1 bg-gray-300 dark:bg-gray-600 rounded-full mx-auto mb-4" />}
            {title && !fullScreen && (
              <h2 className="text-lg font-semibold text-text dark:text-dark-text mb-4">{title}</h2>
            )}
            {fullScreen && (
              <div className="flex items-center justify-between p-4 border-b border-gray-200 dark:border-dark-border">
                <h2 className="text-lg font-semibold text-text dark:text-dark-text">{title}</h2>
                <button
                  onClick={onClose}
                  className="text-gray-500 min-h-[44px] min-w-[44px] flex items-center justify-center"
                  aria-label="Close"
                >
                  &times;
                </button>
              </div>
            )}
            {fullScreen ? <div className="flex-1 overflow-auto">{children}</div> : children}
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
