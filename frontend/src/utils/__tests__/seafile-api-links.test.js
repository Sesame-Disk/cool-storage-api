const fs = require('fs');
const path = require('path');

const getSeafileApiContent = () => {
    const filePath = path.join(__dirname, '..', 'seafile-api.js');
    return fs.readFileSync(filePath, 'utf8');
};

describe('Share/Upload Links API methods in seafile-api.js', () => {

    let apiContent;

    beforeAll(() => {
        apiContent = getSeafileApiContent();
    });

    test('sysAdminListShareLinks supports sort params', () => {
        expect(apiContent).toContain('seafileAPI.sysAdminListShareLinks');
        expect(apiContent).toMatch(/sysAdminListShareLinks\s*=\s*function\s*\(\s*page\s*,\s*perPage\s*,\s*sortBy\s*,\s*sortOrder/);
        expect(apiContent).toMatch(/if \(sortBy\) params\.set\('order_by', sortBy\)/);
        expect(apiContent).toMatch(/if \(sortOrder\) params\.set\('direction', sortOrder\)/);
    });

    test('sysAdminListAllUploadLinks supports sort params', () => {
        expect(apiContent).toContain('seafileAPI.sysAdminListAllUploadLinks');
        expect(apiContent).toMatch(/sysAdminListAllUploadLinks\s*=\s*function\s*\(\s*page\s*,\s*perPage\s*,\s*sortBy\s*,\s*sortOrder/);
        expect(apiContent).toMatch(/if \(sortBy\) params\.set\('order_by', sortBy\)/);
        expect(apiContent).toMatch(/if \(sortOrder\) params\.set\('direction', sortOrder\)/);
    });

    test('upload links list still keeps active/expired filters', () => {
        const uploadSectionMatch = apiContent.match(/sysAdminListAllUploadLinks[\s\S]*?return this\.req\.get\(url\);/);
        expect(uploadSectionMatch).toBeTruthy();
        const uploadSection = uploadSectionMatch[0];
        expect(uploadSection).toContain("params.set('active', active)");
        expect(uploadSection).toContain("params.set('expired', expired)");
    });

    test('sysAdminListUsers supports status filter', () => {
        expect(apiContent).toContain('seafileAPI.sysAdminListUsers');
        expect(apiContent).toMatch(/sysAdminListUsers\s*=\s*function\s*\(\s*page\s*,\s*perPage\s*,\s*isLDAPImported\s*,\s*sortBy\s*,\s*sortOrder\s*,\s*status\s*\)/);
        expect(apiContent).toContain("params.set('status', status)");
    });

    test('sysAdminRestoreUser endpoint exists', () => {
        expect(apiContent).toContain('seafileAPI.sysAdminRestoreUser');
        expect(apiContent).toContain("/api/v2.1/admin/users/");
        expect(apiContent).toContain("/restore/");
    });

    test('sysAdminSearchUsers forwards pagination params', () => {
        expect(apiContent).toContain('seafileAPI.sysAdminSearchUsers');
        expect(apiContent).toContain("params.set('page', page)");
        expect(apiContent).toContain("params.set('per_page', perPage)");
    });
});
