import { SeafileAPI } from 'seafile-js';
import { siteRoot, serviceURL } from './constants';

const TOKEN_KEY = 'sesamefs_auth_token';

let seafileAPI = new SeafileAPI();

// Initialize with token from localStorage if available
function initAPI() {
  const token = localStorage.getItem(TOKEN_KEY);
  const server = serviceURL || window.location.origin;

  if (token) {
    // Token-based authentication for SesameFS
    seafileAPI.init({ server, token });
  } else {
    // Initialize without auth - will need to login
    seafileAPI.init({ server });
  }
}

// Check if user is authenticated
function isAuthenticated() {
  return !!localStorage.getItem(TOKEN_KEY);
}

// Login and store token
async function login(username, password) {
  const server = serviceURL || window.location.origin;

  // Call the auth-token endpoint
  const response = await fetch(`${server}/api2/auth-token/`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
    },
    body: new URLSearchParams({
      username: username,
      password: password,
    }),
  });

  if (!response.ok) {
    const error = await response.json().catch(() => ({}));
    throw new Error(error.non_field_errors?.[0] || error.detail || 'Login failed');
  }

  const data = await response.json();

  if (data.token) {
    localStorage.setItem(TOKEN_KEY, data.token);
    // Reinitialize API with the new token
    seafileAPI.init({ server, token: data.token });
    return data;
  }

  throw new Error('No token received');
}

// Logout - clear token
function logout() {
  localStorage.removeItem(TOKEN_KEY);
  // Redirect to login
  window.location.href = '/login/';
}

// Get stored token
function getToken() {
  return localStorage.getItem(TOKEN_KEY);
}

// Initialize on load
initAPI();

export { seafileAPI, isAuthenticated, login, logout, getToken, initAPI };
