package api

import (
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/invopop/ctxi18n"
	"github.com/invopop/ctxi18n/i18n"
	"github.com/kakra/ferry/internal/version"
	"github.com/labstack/echo/v4"
)

// TemplateRenderer handles per-page template rendering to avoid namespace collisions.
type TemplateRenderer struct {
	templates map[string]*template.Template
	server    *Server
}

// NewTemplateRenderer loads page, layout, and partial templates from directory.
func NewTemplateRenderer(directory string, s *Server) *TemplateRenderer {
	r := &TemplateRenderer{
		templates: make(map[string]*template.Template),
		server:    s,
	}

	// Try to find the templates directory (handle relative paths in tests)
	if _, err := os.Stat(directory); os.IsNotExist(err) {
		// Try parent directories for tests
		testDir := filepath.Join("../..", directory)
		if _, err := os.Stat(testDir); err == nil {
			directory = testDir
		}
	}

	layout := filepath.Join(directory, "base.html")
	files, _ := filepath.Glob(filepath.Join(directory, "*.html"))
	var partials []string
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "_") {
			partials = append(partials, f)
		}
	}

	for _, page := range files {
		name := filepath.Base(page)
		if name == "base.html" || strings.HasPrefix(name, "_") {
			continue
		}

		tmpl := template.New(name).Funcs(templateFuncMap())

		tmpl = template.Must(tmpl.ParseFiles(page))
		if len(partials) > 0 {
			tmpl = template.Must(tmpl.ParseFiles(partials...))
		}
		tmpl = template.Must(tmpl.ParseFiles(layout))

		r.templates[name] = tmpl
	}

	// Partial templates can be rendered standalone for HTMX fragments.
	for _, partial := range partials {
		name := filepath.Base(partial)
		tmpl := template.New(name).Funcs(templateFuncMap())
		// Parse ALL partials into this set so they can reference each other
		r.templates[name] = template.Must(tmpl.ParseFiles(partials...))
	}

	return r
}

func templateFuncMap() template.FuncMap {
	return template.FuncMap{
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid dict call")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
	}
}

// Render executes a page template inside the shared base layout.
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	tmpl, ok := t.templates[name]
	if !ok {
		return fmt.Errorf("template %s not found", name)
	}

	viewData := t.addViewHelpers(data, c)

	// We always execute the "base" template which contains the layout
	return tmpl.ExecuteTemplate(w, "base", viewData)
}

// RenderFragment executes a standalone partial template for HTMX responses.
func (t *TemplateRenderer) RenderFragment(w io.Writer, name string, data interface{}, c echo.Context) error {
	tmpl, ok := t.templates[name]
	if !ok {
		return fmt.Errorf("template %s not found", name)
	}

	viewData := t.addViewHelpers(data, c)

	// For fragments, we don't use the base layout.
	// We execute the template itself (not "base").
	// Since partials are named after their filename, we can execute that.
	return tmpl.ExecuteTemplate(w, name, viewData)
}

func (t *TemplateRenderer) addViewHelpers(data interface{}, c echo.Context) map[string]interface{} {
	viewData, ok := data.(map[string]interface{})
	if !ok {
		viewData = make(map[string]interface{})
	}

	translate := func(key string, args ...interface{}) string {
		ctx := c.Request().Context()
		var val string
		if len(args) > 0 {
			params := make(i18n.M)
			for i := 0; i < len(args); i += 2 {
				if i+1 < len(args) {
					if k, ok := args[i].(string); ok {
						// Ensure enums/etc are stringified.
						params[k] = fmt.Sprint(args[i+1])
					}
				}
			}
			val = i18n.T(ctx, key, params)
		} else {
			val = i18n.T(ctx, key)
		}

		// Missing keys are logged so translation gaps are visible during testing.
		if val == key {
			localeCode := "unknown"
			if loc := ctxi18n.Locale(ctx); loc != nil {
				localeCode = loc.Code().String()
			}
			log.Printf("i18n: Missing translation key '%s' for locale '%s'", key, localeCode)
		}
		return val
	}

	viewData["T"] = func(key string, args ...interface{}) template.HTML {
		return template.HTML(jsonUnescaper.Replace(translate(key, args...)))
	}

	viewData["TS"] = func(key string, args ...interface{}) string {
		return translate(key, args...)
	}

	if csrfToken := c.Get("csrf"); csrfToken != nil {
		viewData["CSRFToken"] = csrfToken.(string)
	}
	viewData["IsAuthenticated"] = t.server.isAuthenticated(c)
	viewData["Version"] = version.Version

	return viewData
}

var jsonUnescaper = strings.NewReplacer(
	`\u0026`, "&",
	`\u003c`, "<",
	`\u003e`, ">",
)

func setNoStoreHeaders(c echo.Context) {
	c.Response().Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, proxy-revalidate, max-age=0")
	c.Response().Header().Set("Pragma", "no-cache")
	c.Response().Header().Set("Expires", "0")
}
