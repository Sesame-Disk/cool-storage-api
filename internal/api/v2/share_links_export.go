package v2

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/gin-gonic/gin"
	"github.com/xuri/excelize/v2"
)

// ExportShareLinksExcel exports selected share links as an Excel file
// Implements: GET /share/link/export-excel/?token=tok1&token=tok2
func (h *ShareLinkHandler) ExportShareLinksExcel(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	tokens := c.QueryArray("token")
	if len(tokens) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no tokens specified"})
		return
	}

	f := excelize.NewFile()
	sheet := "Share Links"
	f.SetSheetName("Sheet1", sheet)

	// Headers
	f.SetCellValue(sheet, "A1", "Link URL")
	f.SetCellValue(sheet, "B1", "Path")
	f.SetCellValue(sheet, "C1", "Has Password")
	f.SetCellValue(sheet, "D1", "Expiration Date")

	row := 2
	for _, token := range tokens {
		var filePath, passwordHash string
		var expiresAt *time.Time
		err := h.db.Session().Query(
			`SELECT file_path, password_hash, expires_at FROM share_links WHERE link_token = ?`, token,
		).Scan(&filePath, &passwordHash, &expiresAt)
		if err != nil {
			continue
		}

		linkURL := fmt.Sprintf("%s/d/%s", httputil.GetBrowserURL(c, h.serverURL), token)
		hasPassword := "No"
		if passwordHash != "" {
			hasPassword = "Yes"
		}
		expDate := ""
		if expiresAt != nil {
			expDate = expiresAt.Format("2006-01-02 15:04:05")
		}

		f.SetCellValue(sheet, fmt.Sprintf("A%d", row), linkURL)
		f.SetCellValue(sheet, fmt.Sprintf("B%d", row), filePath)
		f.SetCellValue(sheet, fmt.Sprintf("C%d", row), hasPassword)
		f.SetCellValue(sheet, fmt.Sprintf("D%d", row), expDate)
		row++
	}

	c.Header("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	c.Header("Content-Disposition", "attachment; filename=share-links.xlsx")
	f.Write(c.Writer)
}
