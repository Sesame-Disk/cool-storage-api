package v2

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

var (
	errInvalidStatusFilter  = errors.New("invalid status filter")
	errInvalidActiveFilter  = errors.New("invalid active filter")
	errInvalidExpiredFilter = errors.New("invalid expired filter")
	errAdminLinkNotFound    = errors.New("admin link not found")
	errAdminLinkWrongType   = errors.New("wrong type")
	errAdminLinkWrongOrg    = errors.New("wrong org")
)

type adminLinkListFilters struct {
	HasActiveFilter  bool
	ActiveFilter     bool
	HasExpiredFilter bool
	ExpiredFilter    bool
	Search           string
}

func parseAdminLinkListFiltersFromContext(c *gin.Context, includeActive bool) (adminLinkListFilters, error) {
	statusParam := ""
	activeParam := ""
	if includeActive {
		statusParam = c.DefaultQuery("status", "")
		activeParam = c.DefaultQuery("active", "")
	}

	return parseAdminLinkListFilters(
		statusParam,
		activeParam,
		c.DefaultQuery("expired", ""),
		c.DefaultQuery("search", ""),
	)
}

func parseAdminLinkListFilters(statusParam, activeParam, expiredParam, searchParam string) (adminLinkListFilters, error) {
	filters := adminLinkListFilters{
		Search: strings.ToLower(strings.TrimSpace(searchParam)),
	}

	statusFilter := strings.TrimSpace(strings.ToLower(statusParam))
	activeValue := strings.TrimSpace(strings.ToLower(activeParam))
	expiredValue := strings.TrimSpace(strings.ToLower(expiredParam))

	if statusFilter == "active" {
		activeValue = "true"
	} else if statusFilter == "inactive" {
		activeValue = "false"
	} else if statusFilter != "" && statusFilter != "all" {
		return filters, errInvalidStatusFilter
	}

	if activeValue != "" && activeValue != "all" {
		parsed, err := strconv.ParseBool(activeValue)
		if err != nil {
			return filters, errInvalidActiveFilter
		}
		filters.HasActiveFilter = true
		filters.ActiveFilter = parsed
	}

	if expiredValue != "" && expiredValue != "all" {
		parsed, err := strconv.ParseBool(expiredValue)
		if err != nil {
			return filters, errInvalidExpiredFilter
		}
		filters.HasExpiredFilter = true
		filters.ExpiredFilter = parsed
	}

	return filters, nil
}

func (filters adminLinkListFilters) MatchesState(active, expired bool) bool {
	if filters.HasActiveFilter && active != filters.ActiveFilter {
		return false
	}
	if filters.HasExpiredFilter && expired != filters.ExpiredFilter {
		return false
	}
	return true
}

func (filters adminLinkListFilters) MatchesSearch(values ...string) bool {
	return matchesAdminLinkSearch(filters.Search, values...)
}

func matchesAdminLinkSearch(search string, values ...string) bool {
	needle := strings.ToLower(strings.TrimSpace(search))
	if needle == "" {
		return true
	}

	for _, value := range values {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}

	return false
}

func validateAdminLinkScope(actualOrgID, expectedOrgID, actualType, expectedType string) error {
	if expectedOrgID != "" && actualOrgID != expectedOrgID {
		return errAdminLinkWrongOrg
	}
	if expectedType != "" && actualType != expectedType {
		return errAdminLinkWrongType
	}
	return nil
}

func parseAdminLinkPageParams(pageParam, perPageParam string, defaultPerPage, maxPerPage int) (int, int) {
	page, err := strconv.Atoi(pageParam)
	if err != nil || page < 1 {
		page = 1
	}

	perPage, err := strconv.Atoi(perPageParam)
	if err != nil || perPage < 1 {
		perPage = defaultPerPage
	}
	if maxPerPage > 0 && perPage > maxPerPage {
		perPage = maxPerPage
	}

	return page, perPage
}

func paginateAdminLinks(links []gin.H, page, perPage int) ([]gin.H, int, bool) {
	total := len(links)
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = total
		if perPage < 1 {
			perPage = 1
		}
	}

	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	return links[start:end], total, end < total
}

func sortAdminLinks(links []gin.H, sortBy, direction string) {
	if sortBy == "" {
		sortBy = "ctime"
		if direction == "" {
			direction = "desc"
		}
	} else if direction == "" {
		direction = "asc"
	}

	sort.Slice(links, func(i, j int) bool {
		if sortBy == "view_cnt" {
			vi, _ := links[i]["view_cnt"].(int)
			vj, _ := links[j]["view_cnt"].(int)
			if direction == "desc" {
				return vi > vj
			}
			return vi < vj
		}

		var vi, vj string
		switch sortBy {
		case "ctime":
			vi, _ = links[i]["ctime"].(string)
			vj, _ = links[j]["ctime"].(string)
		case "creator":
			vi, _ = links[i]["creator_email"].(string)
			vj, _ = links[j]["creator_email"].(string)
		case "name":
			vi, _ = links[i]["obj_name"].(string)
			vj, _ = links[j]["obj_name"].(string)
		default:
			vi, _ = links[i]["ctime"].(string)
			vj, _ = links[j]["ctime"].(string)
		}
		if direction == "desc" {
			return vi > vj
		}
		return vi < vj
	})
}
