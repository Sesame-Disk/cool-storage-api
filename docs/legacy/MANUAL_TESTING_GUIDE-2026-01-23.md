# Manual Testing Guide - SesameFS Backend API

**Last Updated**: 2026-01-22
**Purpose**: Step-by-step guide for manually testing newly implemented backend endpoints

---

## Prerequisites

### Setup
1. Start the SesameFS server:
   ```bash
   go run cmd/sesamefs/main.go
   ```

2. Get an authentication token (dev mode):
   ```bash
   curl -X POST http://localhost:8080/api2/auth-token/ \
     -d "username=test@example.com" \
     -d "password=test123"
   ```
   Save the token from the response: `{"token": "your-token-here"}`

3. Set environment variable for convenience:
   ```bash
   export TOKEN="your-token-here"
   export REPO_ID="your-repo-uuid"
   ```

---

## 1. File Sharing to Users/Groups

### 1.1 List Shared Items for a File/Folder
```bash
# List all shares for a path
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?p=/&share_type=user"

# Expected: 200 OK with array of shares
# Response: [{"share_id":"...","share_type":"user","repo_id":"...","permission":"r",...}]
```

### 1.2 Share File/Folder to Users
```bash
# Share to single user
curl -X PUT -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?p=/test.txt" \
  -d "share_type=user" \
  -d "permission=r" \
  -d "username=alice@example.com"

# Share to multiple users
curl -X PUT -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?p=/folder" \
  -d "share_type=user" \
  -d "permission=rw" \
  -d "username=alice@example.com" \
  -d "username=bob@example.com"

# Expected: 200 OK with created shares
```

### 1.3 Share to Groups
```bash
# Share to group
curl -X PUT -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?p=/folder" \
  -d "share_type=group" \
  -d "permission=r" \
  -d "group_id=GROUP_UUID"

# Expected: 200 OK
```

### 1.4 Update Share Permission
```bash
# Update user share permission
curl -X POST -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?p=/test.txt&share_type=user&username=alice@example.com" \
  -d "permission=rw"

# Expected: 200 OK {"success":true}
```

### 1.5 Delete Share
```bash
# Delete user share
curl -X DELETE -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?p=/test.txt&share_type=user&username=alice@example.com"

# Expected: 200 OK {"success":true}
```

### 1.6 List Shared Repos (I shared with others)
```bash
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/shared-repos/"

# Expected: 200 OK with array of repos I've shared
```

### 1.7 List BeShared Repos (Shared with me)
```bash
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/beshared-repos/"

# Expected: 200 OK with array of repos shared with me
```

---

## 2. Groups Management

### 2.1 List My Groups
```bash
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/"

# Expected: 200 OK with array of groups
# Response: [{"id":"uuid","name":"Team A","creator":"user@example.com","member_count":5}]
```

### 2.2 Create Group
```bash
# Using form data
curl -X POST -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/" \
  -d "group_name=Engineering Team"

# Using JSON
curl -X POST -H "Authorization: Token $TOKEN" \
  -H "Content-Type: application/json" \
  "http://localhost:8080/api/v2.1/groups/" \
  -d '{"group_name":"Marketing Team"}'

# Expected: 201 Created with group details
# Response: {"id":"uuid","name":"Engineering Team","creator":"user@example.com",...}
```

### 2.3 Get Group Details
```bash
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/GROUP_UUID/"

# Expected: 200 OK with group details
```

### 2.4 Update Group (Rename)
```bash
curl -X PUT -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/GROUP_UUID/" \
  -d "group_name=New Team Name"

# Expected: 200 OK {"success":true}
# Note: Only owner or admin can rename
```

### 2.5 Delete Group
```bash
curl -X DELETE -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/GROUP_UUID/"

# Expected: 200 OK {"success":true}
# Note: Only owner can delete
```

### 2.6 List Group Members
```bash
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/GROUP_UUID/members/"

# Expected: 200 OK with array of members
# Response: [{"email":"user@example.com","name":"John Doe","role":"owner",...}]
```

### 2.7 Add Group Member
```bash
curl -X POST -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/GROUP_UUID/members/" \
  -d "email=newmember@example.com" \
  -d "role=member"

# Roles: "owner", "admin", "member"
# Expected: 200 OK {"success":true}
# Note: Only owner or admin can add members
```

### 2.8 Remove Group Member
```bash
curl -X DELETE -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/GROUP_UUID/members/member@example.com/"

# Expected: 200 OK {"success":true}
# Note: Cannot remove owner, only owner/admin can remove members
```

---

## 3. File Tags

### 3.1 List Repository Tags
```bash
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/repo-tags/"

# Expected: 200 OK with array of tags
# Response: [{"repo_tag_id":1,"tag_name":"Important","tag_color":"#ff0000","files_count":3}]
```

### 3.2 Create Repository Tag
```bash
curl -X POST -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/repo-tags/" \
  -d "name=Important" \
  -d "color=#ff0000"

# Expected: 201 Created
# Response: {"repo_tag_id":1,"repo_id":"...","tag_name":"Important","tag_color":"#ff0000"}
```

### 3.3 Update Repository Tag
```bash
curl -X PUT -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/repo-tags/1/" \
  -d "name=Critical" \
  -d "color=#ff5500"

# Expected: 200 OK {"success":true}
```

### 3.4 Delete Repository Tag
```bash
curl -X DELETE -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/repo-tags/1/"

# Expected: 200 OK {"success":true}
# Note: Also deletes all file tags using this repo tag
```

### 3.5 Get File Tags
```bash
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/file-tags/?file_path=/document.pdf"

# Expected: 200 OK with array of tags for this file
# Response: [{"file_tag_id":1,"repo_tag_id":1,"tag_name":"Important","tag_color":"#ff0000"}]
```

### 3.6 Add Tag to File
```bash
curl -X POST -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/file-tags/" \
  -d "file_path=/document.pdf" \
  -d "repo_tag_id=1"

# Expected: 201 Created
# Response: {"file_tag_id":1,"repo_tag_id":1,"tag_name":"Important","tag_color":"#ff0000"}
```

### 3.7 Remove Tag from File
```bash
curl -X DELETE -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/file-tags/1/"

# Expected: 200 OK {"success":true}
```

---

## 4. Share Links (Already Tested)

### 4.1 List Share Links
```bash
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/share-links/?p=/file.txt"

# Expected: 200 OK with array of share links
```

### 4.2 Create Share Link
```bash
curl -X POST -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/share-links/" \
  -d "repo_id=$REPO_ID" \
  -d "path=/file.txt" \
  -d "permissions=download" \
  -d "expire_days=7" \
  -d "password=secret123"

# Permissions: "download", "preview_download", "preview_only", "upload", "edit"
# Expected: 200 OK with share link details
```

### 4.3 Delete Share Link
```bash
curl -X DELETE -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/repos/$REPO_ID/share-links/SHARE_TOKEN/"

# Expected: 200 OK {"success":true}
```

---

## 5. Error Testing

### 5.1 Test Invalid Repo ID
```bash
# Should return 400 Bad Request
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/not-a-uuid/dir/shared_items/"

# Expected: 400 {"error":"invalid repo_id"}
```

### 5.2 Test Missing Authorization
```bash
# Should return 401 Unauthorized
curl "http://localhost:8080/api/v2.1/groups/"

# Expected: 401
```

### 5.3 Test Permission Denied
```bash
# Try to delete group you're not owner of
curl -X DELETE -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/SOMEONE_ELSES_GROUP/"

# Expected: 403 {"error":"only group owner can delete the group"}
```

### 5.4 Test Not Found
```bash
# Non-existent group
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/00000000-0000-0000-0000-000000000000/"

# Expected: 404 {"error":"group not found"}
```

---

## 6. Performance Testing

### 6.1 Batch Share Creation
```bash
# Share to 10 users at once
curl -X PUT -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?p=/folder" \
  -d "share_type=user" \
  -d "permission=r" \
  -d "username=user1@example.com" \
  -d "username=user2@example.com" \
  -d "username=user3@example.com" \
  -d "username=user4@example.com" \
  -d "username=user5@example.com" \
  -d "username=user6@example.com" \
  -d "username=user7@example.com" \
  -d "username=user8@example.com" \
  -d "username=user9@example.com" \
  -d "username=user10@example.com"

# Monitor response time - should complete in < 500ms
```

### 6.2 List Large Groups
```bash
# Create group with 100 members, then list
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/GROUP_UUID/members/"

# Should return all members efficiently
```

---

## 7. Integration Testing

### 7.1 Full Sharing Workflow
```bash
# 1. Create a group
GROUP_ID=$(curl -X POST -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/" \
  -d "group_name=Test Group" | jq -r '.id')

# 2. Add members to group
curl -X POST -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/$GROUP_ID/members/" \
  -d "email=alice@example.com" \
  -d "role=member"

# 3. Share repo to group
curl -X PUT -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?p=/" \
  -d "share_type=group" \
  -d "permission=rw" \
  -d "group_id=$GROUP_ID"

# 4. Verify share exists
curl -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api2/repos/$REPO_ID/dir/shared_items/?share_type=group"

# 5. Clean up
curl -X DELETE -H "Authorization: Token $TOKEN" \
  "http://localhost:8080/api/v2.1/groups/$GROUP_ID/"
```

---

## 8. Common Issues and Solutions

### Issue: "database not available"
**Cause**: Cassandra not running or not connected
**Solution**:
```bash
docker-compose up -d cassandra
# Wait 30 seconds for Cassandra to start
go run cmd/sesamefs/main.go
```

### Issue: "user not found"
**Cause**: User doesn't exist in users_by_email table
**Solution**: Create user first via authentication endpoint

### Issue: "permission denied"
**Cause**: User doesn't have required role (owner/admin)
**Solution**: Use the group owner's token or add user as admin

### Issue: "invalid repo_id"
**Cause**: Repo UUID format is incorrect
**Solution**: Use proper UUID format: `123e4567-e89b-12d3-a456-426614174000`

---

## 9. Automated Testing Scripts

### Test All Endpoints Script
```bash
#!/bin/bash
# save as test_all_endpoints.sh

TOKEN="your-token-here"
REPO_ID="your-repo-uuid"
BASE_URL="http://localhost:8080"

echo "Testing Groups..."
curl -s -H "Authorization: Token $TOKEN" "$BASE_URL/api/v2.1/groups/" | jq .

echo "Testing Shares..."
curl -s -H "Authorization: Token $TOKEN" "$BASE_URL/api2/repos/$REPO_ID/dir/shared_items/" | jq .

echo "Testing Tags..."
curl -s -H "Authorization: Token $TOKEN" "$BASE_URL/api/v2.1/repos/$REPO_ID/repo-tags/" | jq .

echo "All tests completed!"
```

---

## 10. Next Steps

After manual testing, consider:
1. **Load testing** with Apache Bench or wrk
2. **Integration testing** with Postman collections
3. **End-to-end testing** with Seafile desktop client
4. **Frontend integration** - test with React UI

---

## Support

If you encounter issues during testing:
1. Check server logs: `docker-compose logs -f sesamefs`
2. Check Cassandra logs: `docker-compose logs -f cassandra`
3. Enable debug logging in `config.yml`
4. File issue with reproduction steps
