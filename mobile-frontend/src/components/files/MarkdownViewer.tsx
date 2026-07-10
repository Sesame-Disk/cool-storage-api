import React, { useState, useEffect } from 'react';
import { X, Download, Copy } from 'lucide-react';
import { downloadFile } from '../../lib/share';

interface MarkdownViewerProps {
  url: string;
  fileName: string;
  onClose: () => void;
  onToast?: (msg: string) => void;
}

/**
 * Escape HTML so raw markdown content can never inject markup. We build the
 * rendered HTML from already-escaped text, so this is XSS-safe.
 */
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/** Apply inline formatting (code, bold, italic, links) to already-escaped text. */
function renderInline(text: string): string {
  // Inline code — first, so its contents aren't further transformed.
  let out = text.replace(/`([^`]+)`/g, (_m, code) => `<code>${code}</code>`);
  // Bold
  out = out.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  out = out.replace(/__([^_]+)__/g, '<strong>$1</strong>');
  // Italic
  out = out.replace(/\*([^*]+)\*/g, '<em>$1</em>');
  out = out.replace(/(^|[^_])_([^_]+)_/g, '$1<em>$2</em>');
  // Links [text](url) — url is already HTML-escaped; block javascript: schemes.
  out = out.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_m, label, href) => {
    const safe = /^(https?:|mailto:|\/|#)/i.test(href) ? href : '#';
    return `<a href="${safe}" target="_blank" rel="noopener noreferrer">${label}</a>`;
  });
  return out;
}

/**
 * A minimal, dependency-free Markdown renderer covering the common subset:
 * headings, fenced/indented code, blockquotes, unordered/ordered lists,
 * horizontal rules, paragraphs and inline formatting. No markdown library is
 * installed in this project, so we render a safe subset rather than add a dep.
 */
function renderMarkdown(md: string): string {
  const lines = md.replace(/\r\n/g, '\n').split('\n');
  const html: string[] = [];
  let i = 0;
  let inList: 'ul' | 'ol' | null = null;

  const closeList = () => {
    if (inList) {
      html.push(`</${inList}>`);
      inList = null;
    }
  };

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block
    const fence = line.match(/^\s*```(.*)$/);
    if (fence) {
      closeList();
      const code: string[] = [];
      i++;
      while (i < lines.length && !/^\s*```/.test(lines[i])) {
        code.push(lines[i]);
        i++;
      }
      i++; // skip closing fence
      html.push(`<pre><code>${escapeHtml(code.join('\n'))}</code></pre>`);
      continue;
    }

    // Horizontal rule
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      closeList();
      html.push('<hr />');
      i++;
      continue;
    }

    // Heading
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      closeList();
      const level = heading[1].length;
      html.push(`<h${level}>${renderInline(escapeHtml(heading[2].trim()))}</h${level}>`);
      i++;
      continue;
    }

    // Blockquote
    if (/^\s*>\s?/.test(line)) {
      closeList();
      const quote: string[] = [];
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        quote.push(lines[i].replace(/^\s*>\s?/, ''));
        i++;
      }
      html.push(`<blockquote>${renderInline(escapeHtml(quote.join(' ')))}</blockquote>`);
      continue;
    }

    // Unordered list
    if (/^\s*[-*+]\s+/.test(line)) {
      if (inList !== 'ul') {
        closeList();
        html.push('<ul>');
        inList = 'ul';
      }
      html.push(`<li>${renderInline(escapeHtml(line.replace(/^\s*[-*+]\s+/, '')))}</li>`);
      i++;
      continue;
    }

    // Ordered list
    if (/^\s*\d+\.\s+/.test(line)) {
      if (inList !== 'ol') {
        closeList();
        html.push('<ol>');
        inList = 'ol';
      }
      html.push(`<li>${renderInline(escapeHtml(line.replace(/^\s*\d+\.\s+/, '')))}</li>`);
      i++;
      continue;
    }

    // Blank line
    if (/^\s*$/.test(line)) {
      closeList();
      i++;
      continue;
    }

    // Paragraph (gather consecutive non-blank, non-block lines)
    closeList();
    const para: string[] = [line];
    i++;
    while (
      i < lines.length &&
      !/^\s*$/.test(lines[i]) &&
      !/^\s*```/.test(lines[i]) &&
      !/^(#{1,6})\s+/.test(lines[i]) &&
      !/^\s*>\s?/.test(lines[i]) &&
      !/^\s*[-*+]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i])
    ) {
      para.push(lines[i]);
      i++;
    }
    html.push(`<p>${renderInline(escapeHtml(para.join('\n')))}</p>`);
  }

  closeList();
  return html.join('\n');
}

export default function MarkdownViewer({ url, fileName, onClose, onToast }: MarkdownViewerProps) {
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    fetch(url)
      .then(res => {
        if (!res.ok) throw new Error('Failed to load file');
        return res.text();
      })
      .then(text => {
        setContent(text);
        setLoading(false);
      })
      .catch(err => {
        setError(err.message);
        setLoading(false);
      });
  }, [url]);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(content);
      onToast?.('Copied to clipboard');
    } catch {
      onToast?.('Failed to copy');
    }
  };

  const rendered = !loading && !error ? renderMarkdown(content) : '';

  return (
    <div className="fixed inset-0 z-[60] bg-white flex flex-col" data-testid="markdown-viewer">
      {/* Top bar */}
      <div className="flex items-center justify-between p-2 border-b border-gray-200">
        <button
          onClick={onClose}
          className="min-h-[44px] min-w-[44px] flex items-center justify-center text-gray-600"
          aria-label="Close"
        >
          <X className="w-6 h-6" />
        </button>
        <p className="text-text text-sm truncate mx-2 flex-1 text-center font-medium">{fileName}</p>
        <div className="flex gap-1">
          <button
            onClick={handleCopy}
            className="min-h-[44px] min-w-[44px] flex items-center justify-center text-gray-600"
            aria-label="Copy"
          >
            <Copy className="w-5 h-5" />
          </button>
          <button
            onClick={() => downloadFile(url, fileName)}
            className="min-h-[44px] min-w-[44px] flex items-center justify-center text-gray-600"
            aria-label="Download"
          >
            <Download className="w-5 h-5" />
          </button>
        </div>
      </div>

      {/* Content */}
      <div className="flex-1 overflow-auto p-4">
        {loading && <p className="text-center text-gray-500 py-8">Loading...</p>}
        {error && <p className="text-center text-red-500 py-4">{error}</p>}
        {!loading && !error && (
          <div
            className="markdown-body prose prose-sm max-w-none text-text leading-relaxed"
            data-testid="markdown-content"
            dangerouslySetInnerHTML={{ __html: rendered }}
          />
        )}
      </div>
    </div>
  );
}
