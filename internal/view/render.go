// Package view loads and executes the server-rendered HTML templates
// (pages + HTMX partials) from an embedded filesystem.
package view

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"momopay/templates"
)

var templatesFS = templates.FS

var funcs = template.FuncMap{
	"xaf": formatXAF,
}

// formatXAF renders an integer amount of CFA francs with a
// thousands-separated display, e.g. 12000 -> "12 000 FCFA".
func formatXAF(amount float64) string {
	s := strconv.FormatFloat(amount, 'f', 0, 64)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}

	n := len(s)
	var out strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 && (n-i)%3 == 0 {
			out.WriteByte(' ')
		}
		out.WriteByte(s[i])
	}

	prefix := ""
	if neg {
		prefix = "-"
	}
	return prefix + out.String() + " FCFA"
}

var (
	pages    = map[string]*template.Template{}
	partials *template.Template
)

func init() {
	pageFiles, err := fs.Glob(templatesFS, "pages/*.html")
	if err != nil {
		panic(err)
	}

	for _, pf := range pageFiles {
		name := strings.TrimSuffix(filepath.Base(pf), ".html")
		// Parsed together with every partial so a page's content can embed
		// a partial directly (e.g. the payment page embeds "pay_status",
		// the same fragment the SSE hooks broadcast on their own).
		pages[name] = template.Must(
			template.New("layout").Funcs(funcs).ParseFS(templatesFS, "layout.html", "partials/*.html", pf),
		)
	}

	partials = template.Must(
		template.New("partials").Funcs(funcs).ParseFS(templatesFS, "partials/*.html"),
	)
}

// Page renders a full page (wrapped in layout.html) identified by its
// filename without extension, e.g. Page(w, "pay", data).
func Page(w io.Writer, name string, data any) error {
	t, ok := pages[name]
	if !ok {
		return fmt.Errorf("view: unknown page %q", name)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

// Partial renders a named partial (defined with {{define "name"}}) to a
// string, for embedding in an SSE event or for ad-hoc composition.
func Partial(name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := partials.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
