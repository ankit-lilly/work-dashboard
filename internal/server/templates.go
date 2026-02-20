package server

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

//go:embed templates/*
var templatesFS embed.FS

func (s *Server) templateSet(name string) *template.Template {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.templates[name]
}

func (s *Server) parseTemplates() {
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
		"add": func(a, b int) int {
			return a + b
		},
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
			} else if cpu >= 60 {
				return "text-warning"
			}
			return "text-success"
		},
		"float64": func(i int) float64 {
			return float64(i)
		},
		"mulf": func(a, b float64) float64 {
			return a * b
		},
		"divf": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"contains": func(s, substr string) bool {
			return strings.Contains(s, substr)
		},
	}

	parseSet := func(name string, patterns ...string) *template.Template {
		t := template.New("base").Funcs(funcs)
		tmpl, err := t.ParseFS(templatesFS, patterns...)
		if err != nil {
			panic(fmt.Errorf("failed to parse templates (%s): %w", name, err))
		}
		return tmpl
	}

	templates := map[string]*template.Template{
		"index":     parseSet("index", "templates/layout.html", "templates/index.html", "templates/fragments/*.html"),
		"json_view": parseSet("json_view", "templates/layout.html", "templates/json_view.html", "templates/fragments/*.html"),
	}

	s.mu.Lock()
	s.templates = templates
	s.mu.Unlock()
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	s.mu.RLock()
	tmpl := s.templates[name]
	s.mu.RUnlock()

	if tmpl == nil {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}

	err := tmpl.ExecuteTemplate(w, "layout.html", data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
