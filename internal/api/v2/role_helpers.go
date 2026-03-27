package v2

import (
	"fmt"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
)

func canManageLegacyContent(role string) bool {
	return role == string(middleware.RoleSuperAdmin) ||
		role == string(middleware.RoleOwner) ||
		role == string(middleware.RoleAdmin) ||
		role == string(middleware.RoleUser)
}

func applyLegacyStaffToggle(currentRole string, isStaff bool) string {
	if isStaff {
		if currentRole != string(middleware.RoleOwner) &&
			currentRole != string(middleware.RoleAdmin) &&
			currentRole != string(middleware.RoleSuperAdmin) {
			return string(middleware.RoleAdmin)
		}
		return currentRole
	}

	if currentRole == string(middleware.RoleAdmin) {
		return string(middleware.RoleUser)
	}

	return currentRole
}

type ownershipTransferPlan struct {
	DemoteOwnerID  string
	PromoteUserID  string
	NoOp           bool
	BootstrapOwner bool
}

func buildOwnershipTransferPlan(isSuperAdmin bool, callerUserID, existingOwnerUserID, newOwnerUserID string, callerRole, newOwnerRole middleware.OrganizationRole) (ownershipTransferPlan, error) {
	if !isSuperAdmin && callerRole != middleware.RoleOwner {
		return ownershipTransferPlan{}, fmt.Errorf("only the organization owner or a superadmin can transfer ownership")
	}

	if !isSuperAdmin && newOwnerUserID == callerUserID {
		return ownershipTransferPlan{}, fmt.Errorf("cannot transfer ownership to yourself")
	}

	if existingOwnerUserID != "" && newOwnerUserID == existingOwnerUserID {
		return ownershipTransferPlan{NoOp: true, PromoteUserID: newOwnerUserID}, nil
	}

	if !middleware.HasRequiredOrgRole(newOwnerRole, middleware.RoleAdmin) {
		return ownershipTransferPlan{}, fmt.Errorf("new owner must be an admin")
	}

	plan := ownershipTransferPlan{
		DemoteOwnerID:  existingOwnerUserID,
		PromoteUserID:  newOwnerUserID,
		BootstrapOwner: existingOwnerUserID == "",
	}
	return plan, nil
}
