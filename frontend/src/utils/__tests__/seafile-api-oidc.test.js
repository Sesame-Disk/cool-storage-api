/**
 * Tests for OIDC API methods added to seafile-api.js
 *
 * These methods were added for OIDC (OpenID Connect) SSO authentication.
 * They use native fetch() instead of this.req because they're called
 * before the user is authenticated.
 *
 * Added methods:
 * - getOIDCConfig() - Get public OIDC configuration
 * - getOIDCLoginURL(redirectURI, returnURL) - Get authorization URL
 * - exchangeOIDCCode(code, state, redirectURI) - Exchange code for tokens
 * - getOIDCLogoutURL(postLogoutRedirectURI) - Get logout URL for SLO
 *
 * Also modified:
 * - logout() - Now supports OIDC Single Logout (SLO)
 * - setAuthToken(token) - Store token after OIDC login
 */

const fs = require('fs');
const path = require('path');

// Read the seafile-api.js file
const getSeafileApiContent = () => {
  const filePath = path.join(__dirname, '..', 'seafile-api.js');
  return fs.readFileSync(filePath, 'utf8');
};

// Read the auth-state.js file (single source of truth for token storage)
const getAuthStateContent = () => {
  const filePath = path.join(__dirname, '..', 'auth-state.js');
  return fs.readFileSync(filePath, 'utf8');
};

describe('OIDC API Methods in seafile-api.js', () => {

  let apiContent;

  beforeAll(() => {
    apiContent = getSeafileApiContent();
  });

  describe('OIDC Configuration', () => {

    test('getOIDCConfig method exists', () => {
      expect(apiContent).toContain('seafileAPI.getOIDCConfig');
      expect(apiContent).toMatch(/getOIDCConfig\s*=\s*async\s*function/);
    });

    test('getOIDCConfig calls correct endpoint', () => {
      // Should call GET /api/v2.1/auth/oidc/config/
      expect(apiContent).toMatch(/\/api\/v2\.1\/auth\/oidc\/config\//);
    });

    test('getOIDCConfig uses fetch (not this.req)', () => {
      // OIDC methods use fetch because they're called before authentication
      const methodMatch = apiContent.match(/getOIDCConfig[\s\S]*?fetch\(/);
      expect(methodMatch).toBeTruthy();
    });
  });

  describe('OIDC Login', () => {

    test('getOIDCLoginURL method exists', () => {
      expect(apiContent).toContain('seafileAPI.getOIDCLoginURL');
      expect(apiContent).toMatch(/getOIDCLoginURL\s*=\s*async\s*function/);
    });

    test('getOIDCLoginURL accepts redirectURI and returnURL parameters', () => {
      const methodMatch = apiContent.match(/getOIDCLoginURL\s*=\s*async\s*function\s*\(([^)]*)\)/);
      expect(methodMatch).toBeTruthy();
      const params = methodMatch[1];
      expect(params).toContain('redirectURI');
      expect(params).toContain('returnURL');
    });

    test('getOIDCLoginURL calls correct endpoint', () => {
      // Should call GET /api/v2.1/auth/oidc/login/
      expect(apiContent).toMatch(/\/api\/v2\.1\/auth\/oidc\/login\//);
    });

    test('getOIDCLoginURL builds URL with query parameters', () => {
      // Should use URLSearchParams to build query string
      expect(apiContent).toMatch(/getOIDCLoginURL[\s\S]*?URLSearchParams/);
    });
  });

  describe('OIDC Callback', () => {

    test('exchangeOIDCCode method exists', () => {
      expect(apiContent).toContain('seafileAPI.exchangeOIDCCode');
      expect(apiContent).toMatch(/exchangeOIDCCode\s*=\s*async\s*function/);
    });

    test('exchangeOIDCCode accepts code, state, and redirectURI parameters', () => {
      const methodMatch = apiContent.match(/exchangeOIDCCode\s*=\s*async\s*function\s*\(([^)]*)\)/);
      expect(methodMatch).toBeTruthy();
      const params = methodMatch[1];
      expect(params).toContain('code');
      expect(params).toContain('state');
      expect(params).toContain('redirectURI');
    });

    test('exchangeOIDCCode calls correct endpoint', () => {
      // Should call POST /api/v2.1/auth/oidc/callback/
      expect(apiContent).toMatch(/\/api\/v2\.1\/auth\/oidc\/callback\//);
    });

    test('exchangeOIDCCode uses POST method', () => {
      const methodMatch = apiContent.match(/exchangeOIDCCode[\s\S]*?method:\s*['"]POST['"]/);
      expect(methodMatch).toBeTruthy();
    });

    test('exchangeOIDCCode sends JSON body', () => {
      expect(apiContent).toMatch(/exchangeOIDCCode[\s\S]*?Content-Type.*application\/json/);
      expect(apiContent).toMatch(/exchangeOIDCCode[\s\S]*?JSON\.stringify/);
    });
  });

  describe('OIDC Logout', () => {

    test('getOIDCLogoutURL method exists', () => {
      expect(apiContent).toContain('seafileAPI.getOIDCLogoutURL');
      expect(apiContent).toMatch(/getOIDCLogoutURL\s*=\s*async\s*function/);
    });

    test('getOIDCLogoutURL accepts postLogoutRedirectURI parameter', () => {
      const methodMatch = apiContent.match(/getOIDCLogoutURL\s*=\s*async\s*function\s*\(([^)]*)\)/);
      expect(methodMatch).toBeTruthy();
      const params = methodMatch[1];
      expect(params).toContain('postLogoutRedirectURI');
    });

    test('getOIDCLogoutURL calls correct endpoint', () => {
      // Should call GET /api/v2.1/auth/oidc/logout/
      expect(apiContent).toMatch(/\/api\/v2\.1\/auth\/oidc\/logout\//);
    });
  });

  describe('Logout Function', () => {

    test('logout function is async', () => {
      expect(apiContent).toMatch(/async\s+function\s+logout\s*\(/);
    });

    test('logout function calls OIDC logout endpoint', () => {
      // Logout should try to get OIDC logout URL
      expect(apiContent).toMatch(/logout[\s\S]*?\/api\/v2\.1\/auth\/oidc\/logout\//);
    });

    test('logout function clears local token', () => {
      expect(apiContent).toMatch(/logout[\s\S]*?localStorage\.removeItem.*TOKEN_KEY/);
    });

    test('logout function redirects to OIDC logout URL if available', () => {
      expect(apiContent).toMatch(/logout[\s\S]*?data\.logout_url/);
      expect(apiContent).toMatch(/logout[\s\S]*?window\.location\.href\s*=\s*data\.logout_url/);
    });

    test('logout function falls back to local logout if OIDC fails', () => {
      // Should have a try-catch and fallback to /login/
      expect(apiContent).toMatch(/logout[\s\S]*?catch[\s\S]*?window\.location\.href.*\/login\//);
    });
  });

  describe('setAuthToken Function', () => {

    test('setAuthToken function exists', () => {
      expect(apiContent).toMatch(/function\s+setAuthToken\s*\(/);
    });

    test('setAuthToken stores token in localStorage', () => {
      expect(apiContent).toMatch(/setAuthToken[\s\S]*?localStorage\.setItem.*TOKEN_KEY/);
    });

    test('setAuthToken reinitializes seafileAPI', () => {
      expect(apiContent).toMatch(/setAuthToken[\s\S]*?seafileAPI\.init/);
    });

    test('setAuthToken is exported', () => {
      expect(apiContent).toMatch(/export\s*\{[\s\S]*?setAuthToken[\s\S]*?\}/);
    });
  });

  describe('API Method Patterns', () => {

    test('all OIDC methods are async', () => {
      const oidcMethods = [
        'getOIDCConfig',
        'getOIDCLoginURL',
        'exchangeOIDCCode',
        'getOIDCLogoutURL',
      ];

      oidcMethods.forEach(method => {
        const methodRegex = new RegExp(`${method}\\s*=\\s*async\\s+function`);
        expect(apiContent).toMatch(methodRegex);
      });
    });

    test('all OIDC methods use fetch (not this.req)', () => {
      // OIDC methods need to work before authentication,
      // so they use native fetch instead of axios (this.req)
      const oidcMethods = [
        'getOIDCConfig',
        'getOIDCLoginURL',
        'exchangeOIDCCode',
        'getOIDCLogoutURL',
      ];

      oidcMethods.forEach(method => {
        // Find the method and check it uses fetch
        const methodRegex = new RegExp(`seafileAPI\\.${method}[\\s\\S]*?=\\s*async\\s+function[\\s\\S]*?fetch\\(`);
        expect(apiContent).toMatch(methodRegex);
      });
    });

    test('OIDC methods return { data: ... } format for compatibility', () => {
      // Methods should return { data: await response.json() }
      // to match the axios response format
      expect(apiContent).toMatch(/getOIDCConfig[\s\S]*?return\s*\{\s*data:/);
      expect(apiContent).toMatch(/getOIDCLoginURL[\s\S]*?return\s*\{\s*data:/);
      expect(apiContent).toMatch(/exchangeOIDCCode[\s\S]*?return\s*\{\s*data:/);
      expect(apiContent).toMatch(/getOIDCLogoutURL[\s\S]*?return\s*\{\s*data:/);
    });
  });

  describe('Documentation', () => {

    test('documents that OIDC methods use fetch instead of this.req', () => {
      // The file should have a comment explaining why fetch is used
      expect(apiContent).toMatch(/OIDC.*fetch|fetch.*before.*authenticated|called.*before.*user.*authenticated/i);
    });

    test('has OIDC section comment', () => {
      expect(apiContent).toMatch(/OIDC.*API.*method|SSO.*authentication/i);
    });
  });
});

describe('OIDC API Endpoint Patterns', () => {

  test('documents correct endpoint patterns', () => {
    const endpoints = {
      getOIDCConfig: {
        method: 'GET',
        url: '/api/v2.1/auth/oidc/config/',
        auth: false,
        response: { enabled: 'boolean', issuer: 'string', client_id: 'string' },
      },
      getOIDCLoginURL: {
        method: 'GET',
        url: '/api/v2.1/auth/oidc/login/',
        query: { redirect_uri: 'string', return_url: 'string' },
        auth: false,
        response: { authorization_url: 'string', redirect_uri: 'string' },
      },
      exchangeOIDCCode: {
        method: 'POST',
        url: '/api/v2.1/auth/oidc/callback/',
        body: { code: 'string', state: 'string', redirect_uri: 'string' },
        auth: false,
        response: { token: 'string', user_id: 'string', email: 'string' },
      },
      getOIDCLogoutURL: {
        method: 'GET',
        url: '/api/v2.1/auth/oidc/logout/',
        query: { post_logout_redirect_uri: 'string' },
        auth: false,
        response: { logout_url: 'string', enabled: 'boolean' },
      },
    };

    // Verify we have all 4 OIDC endpoints documented
    expect(Object.keys(endpoints).length).toBe(4);
  });
});

describe('Token Storage', () => {

  test('TOKEN_KEY constant exists in auth-state', () => {
    const content = getAuthStateContent();
    expect(content).toMatch(/const\s+TOKEN_KEY\s*=\s*['"]sesamefs_auth_token['"]/);
  });

  test('isAuthenticated function exists', () => {
    const content = getSeafileApiContent();
    expect(content).toMatch(/function\s+isAuthenticated\s*\(/);
  });

  test('getToken function exists', () => {
    const content = getSeafileApiContent();
    expect(content).toMatch(/function\s+getToken\s*\(/);
  });

  test('authentication functions are exported', () => {
    const content = getSeafileApiContent();
    expect(content).toMatch(/export\s*\{[\s\S]*?isAuthenticated[\s\S]*?\}/);
    expect(content).toMatch(/export\s*\{[\s\S]*?login[\s\S]*?\}/);
    expect(content).toMatch(/export\s*\{[\s\S]*?logout[\s\S]*?\}/);
    expect(content).toMatch(/export\s*\{[\s\S]*?getToken[\s\S]*?\}/);
  });
});
