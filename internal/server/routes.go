package server

import (
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/EliLillyCo/work-dashboard/internal/server/render"
)

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Render the page shell immediately — no blocking AWS calls.
	// The SSE connection (data-init on the page) pushes a full state
	// snapshot as soon as the client subscribes, so data arrives
	// within moments without delaying the initial page load.
	s.renderer.Render(w, "index", render.DashboardPageData{ActiveNav: "dashboard"})
}

func parseIntOrDefault(val string, def int) int {
	if val == "" {
		return def
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func sanitizeFilename(filename string) string {
	filename = path.Base(filename)
	filename = strings.ReplaceAll(filename, "\"", "")
	filename = strings.ReplaceAll(filename, "\n", "")
	filename = strings.ReplaceAll(filename, "\r", "")
	filename = strings.ReplaceAll(filename, "\\", "")
	return filename
}
