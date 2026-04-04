package templates

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io"
	"strings"
)

//go:embed html/*.html
var htmlFS embed.FS

// pageTemplates maps page name to its parsed template (base + page combined).
var pageTemplates map[string]*template.Template

func init() {
	pageTemplates = make(map[string]*template.Template)

	// Read the base template source
	baseContent, err := htmlFS.ReadFile("html/base.html")
	if err != nil {
		panic("templates: failed to read base.html: " + err.Error())
	}

	// Read all page template files and parse each with the base
	entries, err := htmlFS.ReadDir("html")
	if err != nil {
		panic("templates: failed to read html dir: " + err.Error())
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "base.html" || !strings.HasSuffix(name, ".html") {
			continue
		}

		pageContent, err := htmlFS.ReadFile("html/" + name)
		if err != nil {
			panic("templates: failed to read " + name + ": " + err.Error())
		}

		// Parse base first, then the page template (which defines blocks that override base defaults)
		t := template.Must(template.New("base.html").Parse(string(baseContent)))
		template.Must(t.New(name).Parse(string(pageContent)))
		pageTemplates[name] = t
	}
}

// Render executes the named page template with data, rendering through the base layout.
// The template is invoked via the "base" block defined in base.html.
func Render(w io.Writer, name string, data interface{}) error {
	t, ok := pageTemplates[name]
	if !ok {
		return fmt.Errorf("template %q not found", name)
	}
	return t.ExecuteTemplate(w, "base", data)
}

// RenderString is like Render but returns the result as a string.
func RenderString(name string, data interface{}) (string, error) {
	var buf bytes.Buffer
	if err := Render(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// FilePreviewData holds data for file_preview.html and file_preview_historic.html
type FilePreviewData struct {
	Filename       string
	DownloadURL    string
	PreviewContent template.HTML // pre-built HTML snippet, trusted
}

// OnlyOfficeData holds data for onlyoffice_editor.html
type OnlyOfficeData struct {
	Filename   string
	APIJSURL   string
	ConfigJSON template.JS // raw JSON for JS config, trusted
}

// ErrorPageData holds data for error_page.html
type ErrorPageData struct {
	Title   string
	Message string
}

// LoginSuccessData holds data for login_success.html
type LoginSuccessData struct {
	ReturnURL string
}
