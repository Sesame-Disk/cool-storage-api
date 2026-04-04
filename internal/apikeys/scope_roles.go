package apikeys

var orgRoleRanks = map[string]int{
	"superadmin": 5,
	"owner":      4,
	"admin":      3,
	"user":       2,
	"readonly":   1,
	"guest":      0,
}

// ConstrainRoleForScope caps the effective org role granted through an API key.
// A narrower key scope must never expand the authority of the owning user.
func ConstrainRoleForScope(actualRole, scope string) string {
	switch scope {
	case ScopeRead:
		return minRole(actualRole, "readonly")
	case ScopeReadWrite:
		return minRole(actualRole, "user")
	case ScopeAdmin:
		return actualRole
	default:
		return actualRole
	}
}

func minRole(actualRole, maxRole string) string {
	actualRank, actualOK := orgRoleRanks[actualRole]
	maxRank, maxOK := orgRoleRanks[maxRole]
	if !actualOK || !maxOK {
		return actualRole
	}
	if actualRank > maxRank {
		return maxRole
	}
	return actualRole
}
