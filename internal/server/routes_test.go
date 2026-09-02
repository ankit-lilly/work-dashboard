package server

import (
	"strings"
	"testing"

	"github.com/EliLillyCo/work-dashboard/internal/server/render"
)

func TestDashboardFooterIncludesBuildVersion(t *testing.T) {
	renderer, err := render.NewRenderer(templatesFS)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	html, err := renderer.ExecuteTemplate("index", "layout.html", render.DashboardPageData{
		BuildVersion: "v0.0.9-0-ga4b25a1",
	})
	if err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	if !strings.Contains(html, "Build <span class=\"font-mono\">v0.0.9-0-ga4b25a1</span>") {
		t.Fatalf("dashboard footer is missing the build version")
	}
}
