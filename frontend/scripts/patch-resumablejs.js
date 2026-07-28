const fs = require('fs');
const path = require('path');

const resumablePath = path.join(__dirname, '..', 'node_modules', '@seafile', 'resumablejs', 'resumable.js');
let source = fs.readFileSync(resumablePath, 'utf8');

const replacements = [
  {
    before: "maxChunkRetries:100,\n      chunkRetryInterval:undefined,",
    after: "maxChunkRetries:100,\n      throttledErrors:[429],\n      throttledMaxChunkRetries:12,\n      chunkRetryInterval:undefined,",
  },
  {
    before: "var chunkEvent = function(event, message){",
    after: "var chunkEvent = function(event, message, chunk, retryInfo){",
  },
  {
    before: "$.resumableObj.fire('fileRetry', $);",
    after: "$.resumableObj.fire('fileRetry', $, chunk, retryInfo);",
  },
  {
    before: [
      "var status = $.status();\n          if(status=='success'||status=='error') {",
      "var retryInfo = $.xhr ? {\n            status: $.xhr.status,\n            retryAfter: $.xhr.getResponseHeader('Retry-After')\n          } : {status: 0, retryAfter: null};\n          var status = $.status();\n          if(status=='success'||status=='error') {",
    ],
    after: "var retryInfo = {status: $.xhr ? $.xhr.status : 0, retryAfter: null};\n          try {\n            retryInfo.retryAfter = $.xhr ? $.xhr.getResponseHeader('Retry-After') : null;\n          } catch(e) {\n            // Some browsers throw when response headers are unavailable.\n          }\n          var status = $.status();\n          if(status=='success'||status=='error') {",
  },
  {
    before: "$.callback('retry', $.message());",
    after: "$.callback('retry', $.message(), $, retryInfo);",
  },
  {
    before: "} else if($h.contains($.getOpt('permanentErrors'), $.xhr.status) || $.retries >= $.getOpt('maxChunkRetries')) {",
    after: "} else if($h.contains($.getOpt('permanentErrors'), $.xhr.status) || $.retries >= ($h.contains($.getOpt('throttledErrors'), $.xhr.status) ? $.getOpt('throttledMaxChunkRetries') : $.getOpt('maxChunkRetries'))) {",
  },
  {
    before: "setTimeout($.send, retryInterval);",
    after: "$.retryTimer = setTimeout(function(){\n                $.retryTimer = null;\n                $.send();\n              }, retryInterval);",
  },
  {
    before: "$.abort = function(){\n        // Abort and reset\n        if($.xhr) $.xhr.abort();\n        $.xhr = null;\n      };",
    after: "$.abort = function(){\n        // Abort and reset, including a delayed retry that has not fired yet.\n        if($.retryTimer) {\n          clearTimeout($.retryTimer);\n          $.retryTimer = null;\n        }\n        $.pendingRetry = false;\n        if($.xhr) $.xhr.abort();\n        $.xhr = null;\n      };",
  },
];

for (const { before, after } of replacements) {
  if (source.includes(after)) continue;
  // Longest first. A replacement can list several accepted shapes — the pristine
  // one and an earlier patched one it must migrate from — and the pristine shape
  // is a *substring* of the patched shape. Matching in declaration order would
  // pick the short one, splicing the new code in while leaving the superseded
  // copy above it: the old, unguarded `getResponseHeader` call then still runs
  // first and the try/catch this patch adds never sees the throw. Ordering by
  // specificity keeps that from depending on how the list happens to be written.
  const candidates = [...(Array.isArray(before) ? before : [before])].sort((a, b) => b.length - a.length);
  const target = candidates.find((candidate) => source.includes(candidate));
  if (!target) {
    throw new Error(`@seafile/resumablejs patch target not found: ${candidates[0].split('\n')[0]}`);
  }
  source = source.replace(target, after);
}

fs.writeFileSync(resumablePath, source);
