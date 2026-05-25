// Vitest example test to verify the new test runner is wired correctly.
// New tests should be authored as *.vitest.js; existing *.test.js files
// continue to run under Jest. Both runners share most of the API.
import { describe, it, expect } from 'vitest';
import { validateName } from '../utils';

describe('validateName', () => {
  it('rejects empty names', () => {
    const result = validateName('');
    expect(result.isValid).toBe(false);
  });

  it('rejects names containing forward slash', () => {
    const result = validateName('foo/bar');
    expect(result.isValid).toBe(false);
  });

  it('accepts a plain name', () => {
    const result = validateName('hello.txt');
    expect(result.isValid).toBe(true);
  });
});
