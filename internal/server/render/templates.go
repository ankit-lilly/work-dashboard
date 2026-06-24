package render

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Renderer struct {
	mu        sync.RWMutex
	templates map[string]*template.Template
}

func NewRenderer(templateFS fs.FS) (*Renderer, error) {
	funcs := template.FuncMap{
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid dict call")
			}
			dict := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"idSafe": func(s string) string {
			if s == "" {
				return ""
			}
			var b strings.Builder
			b.Grow(len(s))
			for _, r := range s {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
					b.WriteRune(r)
				} else {
					b.WriteByte('-')
				}
			}
			return b.String()
		},
		"add": func(a, b int) int { return a + b },
		"formatTimeLocal": func(t time.Time) string {
			return t.In(time.Local).Format("2006-01-02 15:04:05 MST")
		},
		"truncate": func(s string, length int) string {
			if len(s) <= length {
				return s
			}
			return s[:length] + "..."
		},
		"cpuColorClass": func(cpu float64) string {
			if cpu >= 80 {
				return "text-error"
			}
			if cpu >= 60 {
				return "text-warning"
			}
			return "text-success"
		},
		"float64": func(i int) float64 { return float64(i) },
		"mulf":    func(a, b float64) float64 { return a * b },
		"divf": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"contains": func(s, substr string) bool { return strings.Contains(s, substr) },
	}

	parseSet := func(name string, patterns ...string) (*template.Template, error) {
		t := template.New("base").Funcs(funcs)
		return t.ParseFS(templateFS, patterns...)
	}

	index, err := parseSet("index", "templates/layout.html", "templates/index.html", "templates/fragments/*.html")
	if err != nil {
		return nil, err
	}
	jsonView, err := parseSet("json_view", "templates/layout.html", "templates/json_view.html", "templates/fragments/*.html")
	if err != nil {
		return nil, err
	}

	return &Renderer{
		templates: map[string]*template.Template{
			"index":     index,
			"json_view": jsonView,
		},
	}, nil
}

func (r *Renderer) Render(w http.ResponseWriter, name string, data any) {
	r.mu.RLock()
	tmpl := r.templates[name]
	r.mu.RUnlock()
	if tmpl == nil {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	if err := tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (r *Renderer) ExecuteTemplate(setName, templateName string, data any) (string, error) {
	r.mu.RLock()
	tmpl := r.templates[setName]
	r.mu.RUnlock()
	if tmpl == nil {
		return "", fmt.Errorf("template set not found: %s", setName)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, templateName, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
