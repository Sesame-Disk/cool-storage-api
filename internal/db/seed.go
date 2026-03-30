package db

import (
	"fmt"
	"log"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// SeedDatabase creates platform org, default organization, and admin users if they don't exist.
// This runs automatically on application startup.
// firstSuperAdminEmail: if non-empty, seeds a superadmin in the platform org with this email
// so the user can log in via OIDC and be matched to the superadmin account on first login.
func (db *DB) SeedDatabase(cfg *config.Config, devMode bool, firstSuperAdminEmail string) error {
	platformOrgID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
	defaultOrgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

	// Check if both orgs exist (idempotent check)
	// IMPORTANT: differentiate between "not found" (seed needed) and a real
	// connection/query error (abort to avoid wiping existing data on transient failures).
	// NOTE: Scan into string because gocql v2 cannot unmarshal Cassandra UUID into google/uuid.UUID.
	var existingPlatformOrg, existingDefaultOrg string

	platformErr := db.Session().Query(`
		SELECT org_id FROM organizations WHERE org_id = ?
	`, platformOrgID.String()).Scan(&existingPlatformOrg)
	if platformErr != nil && platformErr != gocql.ErrNotFound {
		return fmt.Errorf("seed: failed to check platform org: %w", platformErr)
	}
	platformExists := platformErr == nil

	defaultErr := db.Session().Query(`
		SELECT org_id FROM organizations WHERE org_id = ?
	`, defaultOrgID.String()).Scan(&existingDefaultOrg)
	if defaultErr != nil && defaultErr != gocql.ErrNotFound {
		return fmt.Errorf("seed: failed to check default org: %w", defaultErr)
	}
	defaultExists := defaultErr == nil

	if platformExists && defaultExists {
		log.Println("✓ Core organizations already seeded")
	} else {
		log.Println("→ Seeding database with default data...")
	}

	// Create platform organization first (for superadmin users)
	if !platformExists {
		if err := db.createPlatformOrganization(platformOrgID); err != nil {
			return err
		}
		// Create first superadmin in platform org (production bootstrap)
		if firstSuperAdminEmail != "" {
			if err := db.createSuperAdmin(platformOrgID, uuid.New(), firstSuperAdminEmail, "System Administrator"); err != nil {
				return err
			}
		}
	}

	// Create default organization
	if !defaultExists {
		if err := db.createDefaultOrganization(defaultOrgID, cfg.GetOrganizationTemplate("")); err != nil {
			return err
		}
	}

	// Create test users in dev mode only
	if devMode {
		log.Println("→ Dev mode: Creating test users")
		if err := db.createTestUsers(defaultOrgID); err != nil {
			return err
		}
		if err := db.createSuperAdmin(platformOrgID, uuid.MustParse("00000000-0000-0000-0000-000000000099"), "superadmin@sesamefs.local", "Platform Super Admin"); err != nil {
			return err
		}
	}

	if platformExists && defaultExists && !devMode {
		log.Println("✓ Database already seeded, skipping")
		return nil
	}

	log.Println("✓ Database seeding completed successfully")
	return nil
}

// createPlatformOrganization creates the platform-level organization for superadmin users
func (db *DB) createPlatformOrganization(orgID uuid.UUID) error {
	now := time.Now()

	query := `
		INSERT INTO organizations (
			org_id, name, status, settings, storage_quota, storage_used,
			chunking_polynomial, storage_config, created_at,
			plan, quota_policy, billing_cycle,
			traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
			current_period_started_at, current_period_ends_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	settings := map[string]string{
		"theme":    "default",
		"features": "all",
	}

	storageConfig := map[string]string{
		"default_backend": "s3",
	}

	periodEnd := now.AddDate(0, 1, 0) // +1 month

	err := db.Session().Query(query,
		orgID.String(),
		"SesameFS Platform",
		"active",
		settings,
		int64(-1), // unlimited storage
		int64(0),
		int64(17592186044415),
		storageConfig,
		now,
		"platform", // plan (display)
		"soft",     // quota_policy
		"monthly",  // billing_cycle
		int64(-1),  // traffic_quota unlimited
		int64(-1),  // traffic_upload_quota unlimited
		int64(-1),  // traffic_download_quota unlimited
		int(-1),    // max_users unlimited
		now,        // current_period_started_at
		periodEnd,  // current_period_ends_at
	).Exec()

	if err != nil {
		log.Printf("✗ Failed to create platform organization: %v", err)
		return err
	}

	log.Printf("✓ Created platform organization: %s", orgID)
	return nil
}

// createSuperAdmin creates a superadmin user in the platform org.
// userID is fixed for dev seeds; pass uuid.New() for production bootstrapping.
func (db *DB) createSuperAdmin(platformOrgID uuid.UUID, userID uuid.UUID, email, name string) error {
	now := time.Now()

	batch := db.Session().Batch(gocql.LoggedBatch)

	batch.Query(`
		INSERT INTO users (
			org_id, user_id, email, name, role, status,
			quota_bytes, used_bytes, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		platformOrgID.String(),
		userID.String(),
		email,
		name,
		"superadmin",
		"active",
		int64(-2), // unlimited
		int64(0),
		now,
	)

	batch.Query(`
		INSERT INTO users_by_email (email, user_id, org_id)
		VALUES (?, ?, ?)
	`,
		email,
		userID.String(),
		platformOrgID.String(),
	)

	if err := batch.Exec(); err != nil {
		log.Printf("✗ Failed to create superadmin %s: %v", email, err)
		return err
	}

	log.Printf("✓ Created superadmin: %s (%s) in platform org", email, userID)
	return nil
}

// createDefaultOrganization creates the default organization
func (db *DB) createDefaultOrganization(orgID uuid.UUID, template config.OrganizationTemplate) error {
	now := time.Now()

	query := `
		INSERT INTO organizations (
			org_id, name, status, settings, storage_quota, storage_used,
			chunking_polynomial, storage_config, created_at,
			plan, quota_policy, billing_cycle,
			traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
			current_period_started_at, current_period_ends_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	periodEnd := template.PeriodEnd(now)

	err := db.Session().Query(query,
		orgID.String(),
		"Default Organization",
		"active",
		template.Settings,
		template.StorageQuota,
		int64(0), // 0 bytes used
		template.ChunkingPolynomial,
		template.StorageConfig,
		now,
		template.Plan,
		template.QuotaPolicy,
		template.BillingCycle,
		template.TrafficQuota,
		template.TrafficUploadQuota,
		template.TrafficDownloadQuota,
		template.MaxUsers,
		now,       // current_period_started_at
		periodEnd, // current_period_ends_at
	).Exec()

	if err != nil {
		log.Printf("✗ Failed to create default organization: %v", err)
		return err
	}

	log.Printf("✓ Created default organization: %s", orgID)
	return nil
}

// createTestUsers creates test users for development/testing
func (db *DB) createTestUsers(orgID uuid.UUID) error {
	now := time.Now()

	testUsers := []struct {
		userID uuid.UUID
		email  string
		name   string
		role   string
	}{
		{
			userID: uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			email:  "admin@sesamefs.local",
			name:   "Admin User",
			role:   "admin",
		},
		{
			userID: uuid.MustParse("00000000-0000-0000-0000-000000000002"),
			email:  "user@sesamefs.local",
			name:   "Test User",
			role:   "user",
		},
		{
			userID: uuid.MustParse("00000000-0000-0000-0000-000000000003"),
			email:  "readonly@sesamefs.local",
			name:   "Read-Only User",
			role:   "readonly",
		},
		{
			userID: uuid.MustParse("00000000-0000-0000-0000-000000000004"),
			email:  "guest@sesamefs.local",
			name:   "Guest User",
			role:   "guest",
		},
	}

	for _, user := range testUsers {
		batch := db.Session().Batch(gocql.LoggedBatch)

		// Insert into users table
		batch.Query(`
			INSERT INTO users (
				org_id, user_id, email, name, role, status,
				quota_bytes, used_bytes, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			orgID.String(),       // Convert UUID to string
			user.userID.String(), // Convert UUID to string
			user.email,
			user.name,
			user.role,
			"active",
			int64(53687091200), // 50GB for test users
			int64(0),
			now,
		)

		// Insert into users_by_email lookup table
		batch.Query(`
			INSERT INTO users_by_email (email, user_id, org_id)
			VALUES (?, ?, ?)
		`,
			user.email,
			user.userID.String(), // Convert UUID to string
			orgID.String(),       // Convert UUID to string
		)

		if err := batch.Exec(); err != nil {
			log.Printf("✗ Failed to create test user %s: %v", user.email, err)
			return err
		}

		log.Printf("✓ Created test user: %s (%s) with role '%s'", user.email, user.userID, user.role)
	}

	return nil
}
