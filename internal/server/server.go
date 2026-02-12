package server

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/aws"
	"github.com/EliLillyCo/work-dashboard/internal/broadcaster"
	"github.com/EliLillyCo/work-dashboard/internal/config"
)

//go:embed templates/*
var templatesFS embed.FS

type Server struct {
	awsManager *aws.ClientManager
	templates  map[string]*template.Template
	staticFS   fs.FS
	cfg        *config.Config
	mu         sync.RWMutex

	activeBroadcaster        *broadcaster.Broadcaster[[]aws.Execution]
	failuresBroadcaster      *broadcaster.Broadcaster[[]aws.Execution]
	stateMachinesBroadcaster *broadcaster.Broadcaster[[]StateMachineItem]

	activeCacheMu sync.Mutex
	activeCache   map[string][]aws.Execution
	activeCacheAt map[string]time.Time

	jokeMu      sync.Mutex
	chuckJoke   string
	chuckJokeAt time.Time
	jeffJoke    string
	jeffJokeAt  time.Time

	searchMu     sync.Mutex
	searchStates map[string]*searchState

	notifyMu            sync.Mutex
	notifyActiveReady   bool
	notifyFailuresReady bool
	seenActive          map[string]time.Time
	seenFailures        map[string]time.Time
}

func (s *Server) templateSet(name string) *template.Template {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.templates[name]
}

func NewServer(awsManager *aws.ClientManager, staticFS fs.FS, cfg *config.Config) *Server {
	s := &Server{
		awsManager:    awsManager,
		staticFS:      staticFS,
		cfg:           cfg,
		activeCache:   make(map[string][]aws.Execution),
		activeCacheAt: make(map[string]time.Time),
		searchStates:  make(map[string]*searchState),
		seenActive:    make(map[string]time.Time),
		seenFailures:  make(map[string]time.Time),
	}
	s.parseTemplates()
	s.initBroadcasters()
	return s
}

func (s *Server) initBroadcasters() {
	activeInterval := 10 * time.Second
	failuresInterval := 180 * time.Second
	stateMachinesInterval := 10 * time.Minute
	if s.cfg != nil {
		activeInterval = s.cfg.Polling.ActiveInterval
		failuresInterval = s.cfg.Polling.FailuresInterval
		stateMachinesInterval = s.cfg.Polling.StateMachinesInterval
	}
	s.activeBroadcaster = broadcaster.NewBroadcaster(s.fetchActiveExecutions, activeInterval)
	s.failuresBroadcaster = broadcaster.NewBroadcaster(s.fetchRecentFailures, failuresInterval)
	s.stateMachinesBroadcaster = broadcaster.NewBroadcaster(s.fetchStateMachines, stateMachinesInterval)
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
