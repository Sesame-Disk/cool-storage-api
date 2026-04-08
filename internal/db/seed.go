package db

import (
	"fmt"
	"log"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

type seedUserSpec struct {
	UserID     uuid.UUID
	Email      string
	Name       string
	Role       string
	QuotaBytes int64
}

const devSeedUserQuotaBytes = int64(53687091200) // 50GB

func platformDevSeedUsers() []seedUserSpec {
	return []seedUserSpec{
		{
			UserID:     uuid.MustParse("00000000-0000-0000-0000-000000000099"),
			Email:      "superadmin@sesamefs.local",
			Name:       "Platform Super Admin",
			Role:       "superadmin",
			QuotaBytes: int64(-2),
		},
	}
}

func platformSeedUsers(devMode bool) []seedUserSpec {
	if !devMode {
		return nil
	}
	return platformDevSeedUsers()
}

func defaultDevSeedUsers() []seedUserSpec {
	return []seedUserSpec{
		{
			UserID:     uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			Email:      "admin@sesamefs.local",
			Name:       "Admin User",
			Role:       "admin",
			QuotaBytes: devSeedUserQuotaBytes,
		},
		{
			UserID:     uuid.MustParse("00000000-0000-0000-0000-000000000002"),
			Email:      "user@sesamefs.local",
			Name:       "Test User",
			Role:       "user",
			QuotaBytes: devSeedUserQuotaBytes,
		},
		{
			UserID:     uuid.MustParse("00000000-0000-0000-0000-000000000003"),
			Email:      "readonly@sesamefs.local",
			Name:       "Read-Only User",
			Role:       "readonly",
			QuotaBytes: devSeedUserQuotaBytes,
		},
		{
			UserID:     uuid.MustParse("00000000-0000-0000-0000-000000000004"),
			Email:      "guest@sesamefs.local",
			Name:       "Guest User",
			Role:       "guest",
			QuotaBytes: devSeedUserQuotaBytes,
		},
	}
}

func defaultSeedUsers(devMode bool) []seedUserSpec {
	if !devMode {
		return nil
	}
	return defaultDevSeedUsers()
}

// SeedDatabase creates platform org, default organization, and admin users if
// they don't exist. This runs automatically on application startup.
//
// firstSuperAdminEmail: if non-empty, seeds a superadmin in the platform org
// with this email so the user can log in via OIDC and be matched to the
// superadmin account on first login.
//
// Each org-scoped seed runs in a single LoggedBatch so canonical rows and
// admin read-model projections are written atomically — there is no state
// where the org/user exists but its projection is missing.
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
		if !devMode {
			return nil
		}
	} else {
		log.Println("→ Seeding database with default data...")
	}

	if !platformExists {
		if err := db.seedPlatform(platformOrgID, devMode, firstSuperAdminEmail); err != nil {
			return err
		}
	} else if devMode {
		if err := db.ensureSeedUsers(platformOrgID, platformSeedUsers(devMode)); err != nil {
			return err
		}
	}

	if !defaultExists {
		if err := db.seedDefault(defaultOrgID, cfg.GetOrganizationTemplate(""), devMode); err != nil {
			return err
		}
	} else if devMode {
		if err := db.ensureSeedUsers(defaultOrgID, defaultSeedUsers(devMode)); err != nil {
			return err
		}
	}

	log.Println("✓ Database seeding completed successfully")
	return nil
}

// seedPlatform writes the platform organization together with any bootstrap
// superadmins (production first-admin and/or dev-mode superadmin) in a single
// atomic batch, including all admin read-model projections.
func (db *DB) seedPlatform(orgID uuid.UUID, devMode bool, firstSuperAdminEmail string) error {
	now := time.Now()

	orgName := "SesameFS Platform"
	plan := "platform"
	storageQuota := int64(-1)
	settings := map[string]string{
		"theme":    "default",
		"features": "all",
	}
	storageConfig := map[string]string{
		"default_backend": "s3",
	}
	periodEnd := config.QuotaPeriodEnd(now)

	// Collect the superadmins we will create so the org projection can be
	// written with the correct users_count + owner fields in a single pass.
	var admins []seedUserSpec
	if firstSuperAdminEmail != "" {
		admins = append(admins, seedUserSpec{
			UserID:     uuid.New(),
			Email:      firstSuperAdminEmail,
			Name:       "System Administrator",
			Role:       "superadmin",
			QuotaBytes: int64(-2),
		})
	}
	if devMode {
		admins = append(admins, platformSeedUsers(devMode)...)
	}

	batch := db.Session().Batch(gocql.LoggedBatch)

	batch.Query(`
		INSERT INTO organizations (
			org_id, name, status, settings, storage_quota, storage_used,
			chunking_polynomial, storage_config, created_at,
			plan, quota_policy, billing_cycle,
			traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
			current_period_started_at, current_period_ends_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		orgID.String(), orgName, "active", settings,
		storageQuota, int64(0), int64(17592186044415), storageConfig, now,
		plan, "soft", "monthly",
		int64(-1), int64(-1), int64(-1), int(-1),
		now, periodEnd,
	)

	for _, u := range admins {
		batch.Query(`
			INSERT INTO users (
				org_id, user_id, email, name, role, status,
				quota_bytes, used_bytes, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID.String(), u.UserID.String(), u.Email, u.Name,
			u.Role, "active", u.QuotaBytes, int64(0), now)
		batch.Query(`
			INSERT INTO users_by_email (email, user_id, org_id)
			VALUES (?, ?, ?)
		`, u.Email, u.UserID.String(), orgID.String())
		AddUpsertAdminUserReadModelQuery(batch, AdminUserProjectionRow{
			OrgID:      orgID.String(),
			UserID:     u.UserID.String(),
			Email:      u.Email,
			Name:       u.Name,
			Role:       u.Role,
			Status:     "active",
			QuotaBytes: u.QuotaBytes,
			QuotaUsage: int64(0),
			CreatedAt:  now,
		})
	}

	orgProjection := AdminOrganizationProjectionRow{
		OrgID:        orgID.String(),
		Name:         orgName,
		Status:       "active",
		Plan:         plan,
		StorageQuota: storageQuota,
		CreatedAt:    now,
		UsersCount:   len(admins),
	}
	if len(admins) > 0 {
		orgProjection.OwnerEmail = admins[0].Email
		orgProjection.OwnerName = admins[0].Name
	}
	AddUpsertAdminOrganizationReadModelQuery(batch, orgProjection)

	if err := batch.Exec(); err != nil {
		log.Printf("✗ Failed to seed platform org %s: %v", orgID, err)
		return fmt.Errorf("seed platform org: %w", err)
	}

	log.Printf("✓ Created platform organization: %s", orgID)
	for _, u := range admins {
		log.Printf("✓ Created superadmin: %s (%s) in platform org", u.Email, u.UserID)
	}
	return nil
}

// seedDefault writes the default organization together with any dev-mode test
// users in a single atomic batch, including all admin read-model projections.
func (db *DB) seedDefault(orgID uuid.UUID, template config.OrganizationTemplate, devMode bool) error {
	now := time.Now()
	orgName := "Default Organization"
	periodEnd := template.PeriodEnd(now)

	var users []seedUserSpec
	if devMode {
		users = defaultSeedUsers(devMode)
	}

	batch := db.Session().Batch(gocql.LoggedBatch)

	batch.Query(`
		INSERT INTO organizations (
			org_id, name, status, settings, storage_quota, storage_used,
			chunking_polynomial, storage_config, created_at,
			plan, quota_policy, billing_cycle,
			traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
			current_period_started_at, current_period_ends_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		orgID.String(), orgName, "active",
		template.Settings,
		template.StorageQuota, int64(0),
		template.ChunkingPolynomial, template.StorageConfig, now,
		template.Plan, template.QuotaPolicy, template.BillingCycle,
		template.TrafficQuota, template.TrafficUploadQuota, template.TrafficDownloadQuota,
		template.MaxUsers,
		now, periodEnd,
	)

	for _, u := range users {
		batch.Query(`
			INSERT INTO users (
				org_id, user_id, email, name, role, status,
				quota_bytes, used_bytes, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID.String(), u.UserID.String(), u.Email, u.Name,
			u.Role, "active", u.QuotaBytes, int64(0), now)
		batch.Query(`
			INSERT INTO users_by_email (email, user_id, org_id)
			VALUES (?, ?, ?)
		`, u.Email, u.UserID.String(), orgID.String())
		AddUpsertAdminUserReadModelQuery(batch, AdminUserProjectionRow{
			OrgID:      orgID.String(),
			UserID:     u.UserID.String(),
			Email:      u.Email,
			Name:       u.Name,
			Role:       u.Role,
			Status:     "active",
			QuotaBytes: u.QuotaBytes,
			QuotaUsage: int64(0),
			CreatedAt:  now,
		})
	}

	// Owner fields: prefer a user with an elevated role, else the first user.
	orgProjection := AdminOrganizationProjectionRow{
		OrgID:        orgID.String(),
		Name:         orgName,
		Status:       "active",
		Plan:         template.Plan,
		StorageQuota: template.StorageQuota,
		CreatedAt:    now,
		UsersCount:   len(users),
	}
	for _, u := range users {
		if u.Role == "owner" || u.Role == "admin" || u.Role == "superadmin" {
			orgProjection.OwnerEmail = u.Email
			orgProjection.OwnerName = u.Name
			break
		}
	}
	if orgProjection.OwnerEmail == "" && len(users) > 0 {
		orgProjection.OwnerEmail = users[0].Email
		orgProjection.OwnerName = users[0].Name
	}
	AddUpsertAdminOrganizationReadModelQuery(batch, orgProjection)

	if err := batch.Exec(); err != nil {
		log.Printf("✗ Failed to seed default org %s: %v", orgID, err)
		return fmt.Errorf("seed default org: %w", err)
	}

	log.Printf("✓ Created default organization: %s", orgID)
	for _, u := range users {
		log.Printf("✓ Created test user: %s (%s) with role '%s'", u.Email, u.UserID, u.Role)
	}
	return nil
}

func (db *DB) ensureSeedUsers(orgID uuid.UUID, users []seedUserSpec) error {
	if len(users) == 0 {
		return nil
	}

	for _, u := range users {
		var existingUserID string
		err := db.Session().Query(`
			SELECT user_id FROM users WHERE org_id = ? AND user_id = ?
		`, orgID.String(), u.UserID.String()).Scan(&existingUserID)
		switch err {
		case nil:
			// Canonical row already exists; refresh lookup/projection below.
		case gocql.ErrNotFound:
			now := time.Now()
			if err := db.Session().Query(`
				INSERT INTO users (
					org_id, user_id, email, name, role, status,
					quota_bytes, used_bytes, created_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, orgID.String(), u.UserID.String(), u.Email, u.Name, u.Role, "active", u.QuotaBytes, int64(0), now).Exec(); err != nil {
				return fmt.Errorf("seed: create missing user %s: %w", u.Email, err)
			}
			log.Printf("✓ Repaired missing seed user: %s (%s)", u.Email, u.UserID)
		default:
			return fmt.Errorf("seed: check user %s: %w", u.Email, err)
		}

		if err := db.Session().Query(`
			INSERT INTO users_by_email (email, user_id, org_id)
			VALUES (?, ?, ?)
		`, u.Email, u.UserID.String(), orgID.String()).Exec(); err != nil {
			return fmt.Errorf("seed: upsert email lookup for %s: %w", u.Email, err)
		}
		if err := SyncAdminUserReadModel(db.Session(), orgID.String(), u.UserID.String()); err != nil {
			return fmt.Errorf("seed: sync admin user projection for %s: %w", u.Email, err)
		}
	}

	if err := SyncAdminOrganizationReadModel(db.Session(), orgID.String()); err != nil {
		return fmt.Errorf("seed: sync org projection for %s: %w", orgID, err)
	}

	return nil
}
