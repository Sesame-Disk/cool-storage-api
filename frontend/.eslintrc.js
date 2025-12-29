module.exports = {
  extends: ['react-app'],
  plugins: ['react-hooks'],
  rules: {
    'react-hooks/rules-of-hooks': 'error',
    'react-hooks/exhaustive-deps': 'warn',
    // Allow global browser objects - existing Seahub code uses these
    'no-restricted-globals': 'off',
    'no-unused-expressions': 'warn',
  },
};
