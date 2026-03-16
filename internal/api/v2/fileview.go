package v2

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	htmltemplate "html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/templates"
	"github.com/gin-gonic/gin"
)

// FileViewHandler handles file viewing pages
type FileViewHandler struct {
	db             *db.DB
	config         *config.Config
	storage        *storage.S3Store
	storageManager *storage.Manager
	tokenCreator   TokenCreator
	serverURL      string
	permMiddleware *middleware.PermissionMiddleware
}

// RegisterFileViewRoutes registers routes for file viewing
func RegisterFileViewRoutes(router *gin.Engine, database *db.DB, cfg *config.Config, s3Store *storage.S3Store, storageManager *storage.Manager, tokenCreator TokenCreator, serverURL string, authMiddleware gin.HandlerFunc, permMW ...*middleware.PermissionMiddleware) {
	h := &FileViewHandler{
		db:             database,
		config:         cfg,
		storage:        s3Store,
		storageManager: storageManager,
		tokenCreator:   tokenCreator,
		serverURL:      serverURL,
	}
	if len(permMW) > 0 {
		h.permMiddleware = permMW[0]
	}

	// File view uses a wrapper that promotes ?token= query param to Authorization header,
	// then delegates to the server's standard auth middleware (which supports dev tokens,
	// OIDC sessions, and anonymous access).
	fileViewAuth := fileViewAuthWrapper(authMiddleware)

	libGroup := router.Group("/lib")
	libGroup.Use(fileViewAuth)
	{
		libGroup.GET("/:repo_id/file/*filepath", h.ViewFile)
	}

	// Raw file endpoint for serving files inline (images, etc.)
	repoGroup := router.Group("/repo")
	repoGroup.Use(fileViewAuth)
	{
		repoGroup.GET("/:repo_id/raw/*filepath", h.ServeRawFile)
		repoGroup.GET("/:repo_id/history/download", h.DownloadHistoricFile)
		repoGroup.GET("/:repo_id/history/view", h.ViewHistoricFile)
		repoGroup.GET("/:repo_id/history/raw", h.ServeHistoricFileRaw)
	}
}

// fileViewAuthWrapper wraps the server's standard auth middleware to also accept
// tokens from the ?token= query parameter or the sesamefs_auth cookie.
// Browser-navigated URLs (window.open, <a href>) can't set Authorization headers,
// so we extract the token from alternative sources and promote it to the header.
func fileViewAuthWrapper(serverAuth gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			// 1. Check ?token= query parameter
			if token := c.Query("token"); token != "" {
				c.Request.Header.Set("Authorization", "Token "+token)
			} else if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
				// 2. Check sesamefs_auth cookie (format: "email@token")
				// The token is everything after the last "@" since email may contain "@"
				if idx := strings.LastIndex(cookie, "@"); idx > 0 && idx < len(cookie)-1 {
					token := cookie[idx+1:]
					c.Request.Header.Set("Authorization", "Token "+token)
				}
			}
		}

		// Delegate to the server's standard auth middleware
		serverAuth(c)
	}
}

// setCacheHeaders sets ETag and Cache-Control headers for file serving.
// Returns true if the client already has a fresh copy (304 Not Modified was sent).
func setCacheHeaders(c *gin.Context, fsID string) bool {
	etag := `"` + fsID + `"`
	c.Header("ETag", etag)
	c.Header("Cache-Control", "private, no-cache")

	if match := c.GetHeader("If-None-Match"); match == etag {
		c.Status(http.StatusNotModified)
		return true
	}
	return false
}

// ViewFile serves the file viewer page
// For OnlyOffice-supported files, it renders an HTML page with the OnlyOffice editor
// For previewable files (PDF, images, video, audio, text), it renders an inline preview
// For other files, it redirects to download
// If dl=1 query parameter is present, always download instead of opening in editor
func (h *FileViewHandler) ViewFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Param("filepath")

	// Clean the file path
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}

	filename := filepath.Base(filePath)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))

	// Check if download is explicitly requested (dl=1 parameter)
	if c.Query("dl") == "1" {
		h.redirectToDownload(c, repoID, filePath, filename)
		return
	}

	// Check if OnlyOffice is enabled and file is supported
	if h.config.OnlyOffice.Enabled && h.isOnlyOfficeFile(ext) {
		h.serveOnlyOfficeEditor(c, repoID, filePath, filename)
		return
	}

	// For previewable files, serve an inline preview page
	if isInlinePreviewable(ext) {
		h.serveInlinePreview(c, repoID, filePath, filename, ext)
		return
	}

	// For other files, redirect to download
	h.redirectToDownload(c, repoID, filePath, filename)
}

// isInlinePreviewable returns true for file types that can be previewed inline
func isInlinePreviewable(ext string) bool {
	switch ext {
	// PDF
	case "pdf":
		return true
	// Images
	case "png", "jpg", "jpeg", "gif", "bmp", "webp", "svg", "ico", "tiff", "tif":
		return true
	// Video
	case "mp4", "webm", "ogg", "mov":
		return true
	// Audio
	case "mp3", "wav", "flac", "aac":
		return true
	// Text / code files
	case "txt", "md", "markdown", "json", "yaml", "yml", "xml", "csv",
		"html", "htm", "css", "js", "ts", "jsx", "tsx",
		"py", "go", "rs", "java", "c", "cpp", "h", "hpp",
		"sh", "bash", "zsh", "fish",
		"toml", "ini", "cfg", "conf", "env",
		"sql", "graphql", "proto",
		"dockerfile", "makefile",
		"rb", "php", "swift", "kt", "scala", "r", "lua", "pl",
		"log", "diff", "patch":
		return true
	}
	return false
}

// isTextFile returns true for file types that should be displayed as text
func isTextFile(ext string) bool {
	switch ext {
	case "txt", "md", "markdown", "json", "yaml", "yml", "xml", "csv",
		"html", "htm", "css", "js", "ts", "jsx", "tsx",
		"py", "go", "rs", "java", "c", "cpp", "h", "hpp",
		"sh", "bash", "zsh", "fish",
		"toml", "ini", "cfg", "conf", "env",
		"sql", "graphql", "proto",
		"dockerfile", "makefile",
		"rb", "php", "swift", "kt", "scala", "r", "lua", "pl",
		"log", "diff", "patch":
		return true
	}
	return false
}

// serveInlinePreview renders an HTML page with inline file preview
func (h *FileViewHandler) serveInlinePreview(c *gin.Context, repoID, filePath, filename, ext string) {
	// Build the raw file URL (served inline with correct MIME type)
	// Pass the auth token so the raw endpoint can authenticate
	token := c.Query("token")
	if token == "" {
		// Extract from Authorization header (set by fileViewAuthWrapper)
		auth := c.GetHeader("Authorization")
		if strings.HasPrefix(auth, "Token ") {
			token = strings.TrimPrefix(auth, "Token ")
		} else if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	// Fallback: if still no token (e.g. anonymous/dev mode), use first dev token
	if token == "" && h.config.Auth.DevMode && len(h.config.Auth.DevTokens) > 0 {
		token = h.config.Auth.DevTokens[0].Token
	}
	rawURL := fmt.Sprintf("/repo/%s/raw%s?token=%s", repoID, filePath, url.QueryEscape(token))
	downloadURL := fmt.Sprintf("/lib/%s/file%s?dl=1&token=%s", repoID, filePath, url.QueryEscape(token))

	previewContent := buildPreviewContent(ext, rawURL, filename)

	data := templates.FilePreviewData{
		Filename:       filename,
		DownloadURL:    downloadURL,
		PreviewContent: htmltemplate.HTML(previewContent),
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	if err := templates.Render(c.Writer, "file_preview.html", data); err != nil {
		log.Printf("[serveInlinePreview] template error: %v", err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
	}
}

// buildPreviewContent returns an HTML snippet for the preview area based on file type.
func buildPreviewContent(ext, rawURL, filename string) string {
	safeRawURL := html.EscapeString(rawURL)
	safeFilename := html.EscapeString(filename)

	switch {
	case ext == "pdf":
		return fmt.Sprintf(`<embed src="%s" type="application/pdf" width="100%%" height="100%%" style="border:none;" />`, safeRawURL)

	case ext == "png" || ext == "jpg" || ext == "jpeg" || ext == "gif" || ext == "bmp" || ext == "webp" || ext == "svg" || ext == "ico" || ext == "tiff" || ext == "tif":
		return fmt.Sprintf(`<div style="display:flex;align-items:center;justify-content:center;height:100%%;padding:20px;overflow:auto;">
			<img src="%s" alt="%s" style="max-width:100%%;max-height:100%%;object-fit:contain;" />
		</div>`, safeRawURL, safeFilename)

	case ext == "mp4" || ext == "webm" || ext == "ogg" || ext == "mov":
		return fmt.Sprintf(`<div style="display:flex;align-items:center;justify-content:center;height:100%%;background:#000;">
			<video controls style="max-width:100%%;max-height:100%%;" src="%s">Your browser does not support video playback.</video>
		</div>`, safeRawURL)

	case ext == "mp3" || ext == "wav" || ext == "flac" || ext == "aac":
		return fmt.Sprintf(`<div style="display:flex;align-items:center;justify-content:center;height:100%%;background:#f8f9fa;">
			<audio controls src="%s" style="width:80%%;max-width:600px;">Your browser does not support audio playback.</audio>
		</div>`, safeRawURL)

	case isTextFile(ext):
		// In <script> tags, HTML entities are NOT interpreted by the browser.
		// html.EscapeString would turn & into &amp;, breaking multi-param query strings.
		// Only escape characters that could break the JS string literal.
		jsURL := strings.ReplaceAll(rawURL, `\`, `\\`)
		jsURL = strings.ReplaceAll(jsURL, `'`, `\'`)
		return fmt.Sprintf(`<div id="text-preview" style="height:100%%;overflow:auto;background:#1e1e1e;padding:0;">
			<pre style="margin:0;padding:20px;color:#d4d4d4;font-family:'SF Mono',Monaco,'Cascadia Code','Roboto Mono',Consolas,'Courier New',monospace;font-size:13px;line-height:1.6;tab-size:4;white-space:pre-wrap;word-wrap:break-word;"><code>Loading...</code></pre>
		</div>
		<script>
		fetch('%s',{cache:'no-cache'}).then(function(r){return r.text()}).then(function(text){
			var el=document.querySelector('#text-preview code');
			el.textContent=text;
		}).catch(function(e){
			document.querySelector('#text-preview code').textContent='Failed to load file: '+e.message;
		});
		</script>`, jsURL)

	default:
		return `<div style="display:flex;align-items:center;justify-content:center;height:100%;color:#666;">
			<p>Preview not available for this file type.</p>
		</div>`
	}
}

// redirectToDownload generates a download token and redirects to the seafhttp download endpoint
func (h *FileViewHandler) redirectToDownload(c *gin.Context, repoID, filePath, filename string) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Generate download token
	token, err := h.tokenCreator.CreateDownloadToken(orgID, repoID, filePath, userID)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusInternalServerError, errorPageHTML("Download Error", "Failed to generate download link."))
		return
	}

	// Redirect to seafhttp download endpoint which sets Content-Disposition: attachment
	// Use browser-reachable URL (not internal serverURL which may be on a different port)
	downloadURL := getBrowserURL(c, h.serverURL) + "/seafhttp/files/" + token + "/" + filename
	c.Redirect(http.StatusFound, downloadURL)
}

// isOnlyOfficeFile checks if the file extension is supported by OnlyOffice
func (h *FileViewHandler) isOnlyOfficeFile(ext string) bool {
	for _, viewExt := range h.config.OnlyOffice.ViewExtensions {
		if ext == viewExt {
			return true
		}
	}
	return false
}

// serveOnlyOfficeEditor renders the OnlyOffice editor page
func (h *FileViewHandler) serveOnlyOfficeEditor(c *gin.Context, repoID, filePath, filename string) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Get OnlyOffice handler to generate config
	ooHandler := &OnlyOfficeHandler{
		db:           h.db,
		config:       h.config,
		storage:      h.storage,
		tokenCreator: h.tokenCreator,
		serverURL:    h.serverURL,
	}

	// Get file ID
	fileID, err := ooHandler.getFileID(repoID, orgID, filePath)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusNotFound, errorPageHTML("File Not Found", "The requested file could not be found."))
		return
	}

	// Generate document key
	docKey := generateDocKey(repoID, filePath, fileID)

	// Determine edit mode
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	mode := "view"
	if ooHandler.canEditFile(filename) {
		mode = "edit"
	}

	// Generate download URL
	downloadToken, err := h.tokenCreator.CreateDownloadToken(orgID, repoID, filePath, userID)
	if err != nil {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusInternalServerError, errorPageHTML("Internal Error", "Failed to generate download token."))
		return
	}

	// Use OnlyOffice-specific server URL if configured, otherwise fall back to general server URL
	// This allows configuring a public URL that OnlyOffice server can reach
	ooServerURL := h.config.OnlyOffice.ServerURL
	if ooServerURL == "" {
		ooServerURL = h.serverURL
	}
	downloadURL := ooServerURL + "/seafhttp/files/" + downloadToken + "/" + filename

	// Generate callback URL (URL-encode file_path to handle spaces and special chars)
	callbackURL := fmt.Sprintf("%s/onlyoffice/editor-callback/?repo_id=%s&file_path=%s&doc_key=%s",
		ooServerURL, repoID, url.QueryEscape(filePath), docKey)

	// Get user info
	userName := strings.Split(userID, "@")[0]
	if userName == userID {
		userName = userID
	}

	// Build OnlyOffice configuration (minimal, like Seahub)
	canEdit := mode == "edit"
	docConfig := OnlyOfficeConfig{
		Document: OnlyOfficeDocument{
			FileType: ext,
			Key:      docKey,
			Title:    filename,
			URL:      downloadURL,
			Permissions: &OnlyOfficePermissions{
				Edit:      canEdit,
				Download:  true,
				Print:     true,
				Copy:      true,
				Review:    canEdit,
				Comment:   canEdit,
				FillForms: canEdit,
			},
		},
		DocumentType: getDocumentType(filename),
		EditorConfig: OnlyOfficeEditorConfig{
			CallbackURL: callbackURL,
			Mode:        mode,
			User: OnlyOfficeUser{
				ID:   userID,
				Name: userName,
			},
			Customization: &OnlyOfficeCustomization{
				Forcesave:  canEdit,
				SubmitForm: canEdit,
			},
		},
	}

	// Sign JWT if secret is configured
	if h.config.OnlyOffice.JWTSecret != "" {
		token, err := ooHandler.signJWT(docConfig)
		if err == nil {
			docConfig.Token = token
		}
	}

	// Save doc key mapping
	_ = ooHandler.saveDocKeyMapping(docKey, userID, repoID, filePath)

	// Render the OnlyOffice editor page
	html := onlyOfficeEditorHTML(h.config.OnlyOffice.APIJSURL, docConfig, filename)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, html)
}

// onlyOfficeEditorHTML generates the HTML page for OnlyOffice editor.
// Uses json.Marshal for the config to guarantee the JavaScript config object
// exactly matches the JWT payload (html/template escaping can cause mismatches).
// We use template.JS to inject the raw JSON safely into the template.
func onlyOfficeEditorHTML(apiJSURL string, cfg OnlyOfficeConfig, filename string) string {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		errData := templates.ErrorPageData{Title: "Config Error", Message: err.Error()}
		s, _ := templates.RenderString("error_page.html", errData)
		return s
	}

	data := templates.OnlyOfficeData{
		Filename:   filename,
		APIJSURL:   apiJSURL,
		ConfigJSON: htmltemplate.JS(configJSON),
	}

	s, err := templates.RenderString("onlyoffice_editor.html", data)
	if err != nil {
		log.Printf("[onlyOfficeEditorHTML] template error: %v", err)
		return "<html><body><h1>Internal Error</h1></body></html>"
	}
	return s
}

// ServeRawFile serves a file directly (inline) for embedding in pages
// Used for images, videos, PDFs, text files, etc. that need to be displayed in the browser
// Serves with Content-Disposition: inline and correct MIME type
func (h *FileViewHandler) ServeRawFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Param("filepath")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Clean the file path
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}

	// PERMISSION CHECK: User must have at least read access to the library
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccessCtx(c, orgID, userID, repoID, middleware.PermissionR)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
			return
		}
	}

	// CUSTOM PERMISSION CHECK: preview flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlag(c, "preview") {
		c.JSON(http.StatusForbidden, gin.H{"error": "preview is not allowed by your permission"})
		return
	}

	filename := filepath.Base(filePath)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))

	// Traverse to file to get block IDs
	fsHelper := NewFSHelper(h.db)
	result, err := fsHelper.TraverseToPath(repoID, filePath)
	if err != nil || result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	// Get block IDs and file size from the fs_object
	var blockIDs []string
	var fileSize int64
	err = h.db.Session().Query(`
		SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, result.TargetEntry.ID).Scan(&blockIDs, &fileSize)
	if err != nil {
		log.Printf("[ServeRawFile] Failed to get block IDs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file metadata"})
		return
	}

	// ETag-based cache validation: fs_id changes on every file update
	if setCacheHeaders(c, result.TargetEntry.ID) {
		return
	}

	// Guard against loading very large files - use appropriate limit based on file type
	maxSize := h.getMaxFileSizeForPreview(ext)
	if fileSize > maxSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("file too large for inline preview (%d bytes, max %d)", fileSize, maxSize),
		})
		return
	}

	// Get block store
	if h.storageManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not available"})
		return
	}
	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not available"})
		return
	}

	// Check if library is encrypted
	var encrypted bool
	h.db.Session().Query(`SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?`,
		orgID, repoID).Scan(&encrypted)

	var fileKey []byte
	if encrypted {
		fileKey = GetDecryptSessions().GetFileKey(userID, repoID)
		if fileKey == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "library is encrypted but not unlocked"})
			return
		}
	}

	ctx := c.Request.Context()

	// For iWork preview, we need to buffer the content (requires random access for ZIP parsing)
	needsBuffer := c.Query("preview") == "1" && isAppleIWorkFile(ext)

	if needsBuffer {
		// iWork preview: must buffer for ZIP extraction
		var content bytes.Buffer
		iworkResolvedIDs := streaming.BatchResolveBlockIDs(h.db, orgID, blockIDs)
		for idx, _ := range blockIDs {
			internalID := iworkResolvedIDs[idx]
			reader, err := blockStore.GetBlockReader(ctx, internalID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
				return
			}
			if encrypted && fileKey != nil {
				blockData, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
					return
				}
				blockData, err = crypto.DecryptBlock(blockData, fileKey)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "decryption failed"})
					return
				}
				content.Write(blockData)
			} else {
				_, err = io.Copy(&content, reader)
				reader.Close()
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
					return
				}
			}
		}

		previewData, err := extractIWorkPreviewPDF(content.Bytes(), h.config.FileView.MaxIWorkPreviewBytes)
		if err != nil {
			log.Printf("[ServeRawFile] Failed to extract iWork preview: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "no preview available for this file"})
			return
		}
		previewMIME := "application/pdf"
		previewExt := "pdf"
		if len(previewData) > 3 && previewData[0] == 0xFF && previewData[1] == 0xD8 {
			previewMIME = "image/jpeg"
			previewExt = "jpg"
		} else if len(previewData) > 8 && string(previewData[:4]) == "\x89PNG" {
			previewMIME = "image/png"
			previewExt = "png"
		}
		baseName := strings.TrimSuffix(filename, "."+ext)
		c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s.%s"`, sanitizeFilename(baseName), previewExt))
		c.Data(http.StatusOK, previewMIME, previewData)
		return
	}

	// Normal file serving
	mimeType := mime.TypeByExtension("." + ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Batch resolve all block IDs upfront to avoid per-block Cassandra queries
	resolvedIDs := streaming.BatchResolveBlockIDs(h.db, orgID, blockIDs)

	var fileKeyParam []byte
	if encrypted {
		fileKeyParam = fileKey
	}

	// For video/audio files, use BlockReadSeeker so http.ServeContent can handle
	// Range requests (HTTP 206) without buffering the entire file. Only O(1 block) RAM.
	if isVideoFile(ext) || isAudioFile(ext) {
		blockSizes, err := streaming.QueryBlockSizes(ctx, h.db, orgID, blockStore, resolvedIDs)
		if err != nil {
			log.Printf("[ServeRawFile] Failed to query block sizes: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file metadata"})
			return
		}

		rs := streaming.NewBlockReadSeeker(ctx, blockStore, resolvedIDs, blockSizes, fileSize, fileKeyParam)
		c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, sanitizeFilename(filename)))
		http.ServeContent(c.Writer, c.Request, filename, time.Time{}, rs)
		return
	}

	// Non-video/audio: stream block-by-block, O(block_size) RAM
	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, sanitizeFilename(filename)))
	c.Header("Content-Type", mimeType)
	if fileSize > 0 && !encrypted {
		c.Header("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	c.Status(http.StatusOK)

	streaming.StreamBlocks(c, ctx, blockStore, resolvedIDs, fileKeyParam, "ServeRawFile")
}

// sanitizeFilename removes characters that could cause header injection in Content-Disposition.
func sanitizeFilename(name string) string {
	return strings.NewReplacer(`"`, `'`, "\r", "", "\n", "").Replace(name)
}

// isAppleIWorkFile returns true for Apple iWork file extensions
func isAppleIWorkFile(ext string) bool {
	return ext == "pages" || ext == "numbers" || ext == "key"
}

// extractIWorkPreview extracts the embedded preview from an Apple iWork file.
// iWork files (.pages, .numbers, .key) are ZIP archives containing preview images.
// Older versions (pre-2013) use QuickLook/Preview.pdf, modern versions use preview.jpg.
func extractIWorkPreviewPDF(data []byte, maxPreviewSize int64) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a valid zip archive: %w", err)
	}

	// Known preview file locations in order of preference (best quality first)
	candidates := []string{
		"preview.pdf",
		"preview.jpg",
		"preview.jpeg",
		"preview-web.jpg",
		"preview.png",
		"QuickLook/Preview.pdf",
		"QuickLook/preview.pdf",
		"QuickLook/Thumbnail.jpg",
		"QuickLook/Thumbnail.png",
	}
	for _, candidate := range candidates {
		for _, f := range reader.File {
			if strings.EqualFold(f.Name, candidate) {
				return readZipEntry(f, maxPreviewSize)
			}
		}
	}

	// Fallback: find any PDF in the archive
	for _, f := range reader.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".pdf") {
			return readZipEntry(f, maxPreviewSize)
		}
	}

	// Log all files in the archive for debugging
	var names []string
	for _, f := range reader.File {
		names = append(names, f.Name)
	}
	return nil, fmt.Errorf("no preview found in iWork archive (files: %v)", names)
}

// getMaxFileSizeForPreview returns the appropriate size limit based on file type.
// Videos get a higher limit (10GB default) since 4K videos and long recordings are commonly >1GB.
// Text files get a lower limit (50MB default) to prevent browser freezing.
// Other files get the general preview limit (1GB default).
func (h *FileViewHandler) getMaxFileSizeForPreview(ext string) int64 {
	// Videos need large limits (4K, long recordings)
	if isVideoFile(ext) {
		return h.config.FileView.MaxVideoBytes
	}
	// Text files should have lower limits to prevent browser freeze
	if isTextFile(ext) {
		return h.config.FileView.MaxTextBytes
	}
	// Everything else uses the general preview limit
	return h.config.FileView.MaxPreviewBytes
}

// isVideoFile returns true for video file extensions
func isVideoFile(ext string) bool {
	switch ext {
	case "mp4", "webm", "ogg", "mov", "avi", "mkv", "flv", "wmv", "m4v", "mpg", "mpeg":
		return true
	}
	return false
}

// isAudioFile returns true for audio file extensions
func isAudioFile(ext string) bool {
	switch ext {
	case "mp3", "wav", "flac", "aac", "m4a", "wma", "ogg":
		return true
	}
	return false
}

func readZipEntry(f *zip.File, maxSize int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(maxSize) {
		return nil, fmt.Errorf("entry %s too large: %d bytes (max %d)", f.Name, f.UncompressedSize64, maxSize)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", f.Name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", f.Name, err)
	}
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("entry %s exceeds max preview size", f.Name)
	}
	return data, nil
}

// DownloadHistoricFile serves a file at a specific revision by its FS object ID.
// The frontend file history view calls this with ?obj_id=<fs_id>&p=<path>.
// Unlike normal downloads (which resolve from HEAD commit), this looks up the
// file's blocks directly from the FS object ID.
func (h *FileViewHandler) DownloadHistoricFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	objID := c.Query("obj_id")
	filePath := c.Query("p")

	if objID == "" {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusBadRequest, errorPageHTML("Bad Request", "Missing obj_id parameter."))
		return
	}
	if filePath == "" {
		filePath = "/"
	}

	filename := filepath.Base(filePath)
	if filename == "" || filename == "." || filename == "/" || filename == "\\" {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusBadRequest, errorPageHTML("Bad Request", "Invalid file path."))
		return
	}

	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Check if library is encrypted and get file key
	var encrypted bool
	var fileKey []byte
	err := h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&encrypted)
	if err != nil {
		log.Printf("[DownloadHistoricFile] Failed to query library: %v", err)
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusNotFound, errorPageHTML("Not Found", "Library not found."))
		return
	}

	if encrypted {
		fileKey = GetDecryptSessions().GetFileKey(userID, repoID)
		if fileKey == nil {
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.String(http.StatusForbidden, errorPageHTML("Library Locked", "This library is encrypted. Please unlock it first."))
			return
		}
	}

	// Look up block IDs directly from the FS object ID (skip HEAD commit traversal)
	var blockIDs []string
	err = h.db.Session().Query(`
		SELECT block_ids FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, objID).Scan(&blockIDs)
	if err != nil {
		log.Printf("[DownloadHistoricFile] FS object not found: repo=%s obj=%s err=%v", repoID, objID, err)
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusNotFound, errorPageHTML("Not Found", "The requested file revision could not be found."))
		return
	}

	if h.storageManager == nil {
		log.Printf("[DownloadHistoricFile] Storage manager not available")
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusInternalServerError, errorPageHTML("Internal Error", "Storage not available."))
		return
	}

	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		log.Printf("[DownloadHistoricFile] Block store not available: %v", err)
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusInternalServerError, errorPageHTML("Internal Error", "Block storage not available."))
		return
	}

	// Stream blocks directly to HTTP response
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(filename)))
	c.Header("Content-Type", "application/octet-stream")
	c.Status(http.StatusOK)

	resolvedIDs := streaming.BatchResolveBlockIDs(h.db, orgID, blockIDs)
	streaming.StreamBlocks(c, c.Request.Context(), blockStore, resolvedIDs, fileKey, "DownloadHistoricFile")
}

// ViewHistoricFile serves an HTML preview page for a historic file revision.
// It renders the same inline preview UI as ViewFile but uses the history/raw
// endpoint to fetch the file content by FS object ID instead of HEAD commit.
func (h *FileViewHandler) ViewHistoricFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	objID := c.Query("obj_id")
	filePath := c.Query("p")

	if objID == "" {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusBadRequest, errorPageHTML("Bad Request", "Missing obj_id parameter."))
		return
	}
	if filePath == "" {
		filePath = "/"
	}

	filename := filepath.Base(filePath)
	if filename == "" || filename == "." || filename == "/" || filename == "\\" {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusBadRequest, errorPageHTML("Bad Request", "Invalid file path."))
		return
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))

	// If file is not previewable, fall back to download
	if !isInlinePreviewable(ext) {
		token := c.Query("token")
		params := fmt.Sprintf("obj_id=%s&p=%s", url.QueryEscape(objID), url.QueryEscape(filePath))
		if token != "" {
			params += "&token=" + url.QueryEscape(token)
		}
		c.Redirect(http.StatusFound, fmt.Sprintf("/repo/%s/history/download?%s", repoID, params))
		return
	}

	// Build the raw URL pointing to the historic raw endpoint
	token := c.Query("token")
	if token == "" {
		auth := c.GetHeader("Authorization")
		if strings.HasPrefix(auth, "Token ") {
			token = strings.TrimPrefix(auth, "Token ")
		} else if strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if token == "" && h.config.Auth.DevMode && len(h.config.Auth.DevTokens) > 0 {
		token = h.config.Auth.DevTokens[0].Token
	}

	rawURL := fmt.Sprintf("/repo/%s/history/raw?obj_id=%s&p=%s&token=%s",
		repoID, url.QueryEscape(objID), url.QueryEscape(filePath), url.QueryEscape(token))
	downloadURL := fmt.Sprintf("/repo/%s/history/download?obj_id=%s&p=%s&token=%s",
		repoID, url.QueryEscape(objID), url.QueryEscape(filePath), url.QueryEscape(token))

	previewContent := buildPreviewContent(ext, rawURL, filename)

	data := templates.FilePreviewData{
		Filename:       filename,
		DownloadURL:    downloadURL,
		PreviewContent: htmltemplate.HTML(previewContent),
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	if err := templates.Render(c.Writer, "file_preview_historic.html", data); err != nil {
		log.Printf("[ViewHistoricFile] template error: %v", err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
	}
}

// ServeHistoricFileRaw serves the raw content of a historic file revision inline.
// Unlike DownloadHistoricFile (which forces download), this serves with the correct
// MIME type and Content-Disposition: inline, so browsers can render it for previews.
func (h *FileViewHandler) ServeHistoricFileRaw(c *gin.Context) {
	repoID := c.Param("repo_id")
	objID := c.Query("obj_id")
	filePath := c.Query("p")

	if objID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing obj_id parameter"})
		return
	}
	if filePath == "" {
		filePath = "/"
	}

	filename := filepath.Base(filePath)
	if filename == "" || filename == "." || filename == "/" || filename == "\\" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file path"})
		return
	}

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Check if library is encrypted and get file key
	var encrypted bool
	var fileKey []byte
	err := h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&encrypted)
	if err != nil {
		log.Printf("[ServeHistoricFileRaw] Failed to query library: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	if encrypted {
		fileKey = GetDecryptSessions().GetFileKey(userID, repoID)
		if fileKey == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "library is encrypted but not unlocked"})
			return
		}
	}

	// Look up block IDs and file size from the FS object
	var blockIDs []string
	var fileSize int64
	err = h.db.Session().Query(`
		SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, objID).Scan(&blockIDs, &fileSize)
	if err != nil {
		log.Printf("[ServeHistoricFileRaw] FS object not found: repo=%s obj=%s err=%v", repoID, objID, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "file revision not found"})
		return
	}

	// ETag-based cache validation: obj_id is the fs_id for this historic version
	if setCacheHeaders(c, objID) {
		return
	}

	// Guard against very large files for inline preview
	maxSize := h.getMaxFileSizeForPreview(ext)
	if fileSize > maxSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("file too large for inline preview (%d bytes, max %d)", fileSize, maxSize),
		})
		return
	}

	if h.storageManager == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not available"})
		return
	}
	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage not available"})
		return
	}

	// Determine MIME type
	mimeType := mime.TypeByExtension("." + ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, sanitizeFilename(filename)))
	c.Header("Content-Type", mimeType)
	if fileSize > 0 && !encrypted {
		c.Header("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	c.Status(http.StatusOK)

	resolvedIDs := streaming.BatchResolveBlockIDs(h.db, orgID, blockIDs)
	streaming.StreamBlocks(c, c.Request.Context(), blockStore, resolvedIDs, fileKey, "ServeHistoricFileRaw")
}

// errorPageHTML generates a simple error page
func errorPageHTML(title, message string) string {
	data := templates.ErrorPageData{Title: title, Message: message}
	s, err := templates.RenderString("error_page.html", data)
	if err != nil {
		return "<html><body><h1>" + html.EscapeString(title) + "</h1><p>" + html.EscapeString(message) + "</p></body></html>"
	}
	return s
}
