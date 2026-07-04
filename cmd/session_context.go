package cmd

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

var sessionContextJSON bool

var sessionContextCmd = &cobra.Command{
	Use:   "context",
	Short: "Print agent-friendly context about DevX sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := buildSessionContext()
		if err != nil {
			return err
		}
		if sessionContextJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(ctx)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Current session: %s\n", ctx.CurrentSession)
		for _, s := range ctx.Sessions {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s (%s)\n  path: %s\n  git: %s (%d changed files)\n", s.Name, s.Branch, s.Path, s.GitStatus, s.ChangedFiles)
			for _, svc := range s.Services {
				fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s", svc.Name, svc.URL)
				if svc.Port != 0 {
					fmt.Fprintf(cmd.OutOrStdout(), " port=%d", svc.Port)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
		}
		return nil
	},
}

type agentSessionContext struct {
	CurrentSession string                    `json:"current_session"`
	Sessions       []agentSessionContextItem `json:"sessions"`
}

type agentSessionContextItem struct {
	Name          string                `json:"name"`
	Branch        string                `json:"branch"`
	Path          string                `json:"path"`
	ProjectAlias  string                `json:"project_alias,omitempty"`
	GitStatus     string                `json:"git_status"`
	ChangedFiles  int                   `json:"changed_files"`
	LastChangedAt time.Time             `json:"last_changed_at,omitempty"`
	Services      []agentSessionService `json:"services,omitempty"`
	Attention     bool                  `json:"attention"`
}

type agentSessionService struct {
	Name string `json:"name"`
	Port int    `json:"port,omitempty"`
	URL  string `json:"url,omitempty"`
}

func buildSessionContext() (*agentSessionContext, error) {
	store, err := session.LoadSessions()
	if err != nil {
		return nil, err
	}
	current := session.GetCurrentSessionName()
	ctx := &agentSessionContext{CurrentSession: current}
	names := make([]string, 0, len(store.Sessions))
	for name := range store.Sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]agentSessionContextItem, len(names))
	var wg sync.WaitGroup
	jobs := make(chan int)
	workers := 8
	if workers > len(names) {
		workers = len(names)
	}
	if workers == 0 {
		return ctx, nil
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				items[i] = buildSessionContextItem(names[i], store.Sessions[names[i]])
			}
		}()
	}
	for i := range names {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	ctx.Sessions = items
	return ctx, nil
}

func buildSessionContextItem(name string, sess *session.Session) agentSessionContextItem {
	if sess == nil {
		return agentSessionContextItem{Name: name, GitStatus: "unknown"}
	}
	gitStatus, changed := gitStatusSummary(sess.Path)
	item := agentSessionContextItem{Name: name, Branch: sess.Branch, Path: sess.Path, ProjectAlias: sess.ProjectAlias, GitStatus: gitStatus, ChangedFiles: changed, LastChangedAt: latest(sess.UpdatedAt, sess.LastAttached, sess.CreatedAt), Attention: sess.AttentionFlag}
	serviceNames := make(map[string]bool)
	for svc := range sess.Ports {
		serviceNames[svc] = true
	}
	for svc := range sess.Routes {
		serviceNames[svc] = true
	}
	var services []string
	for svc := range serviceNames {
		services = append(services, svc)
	}
	sort.Strings(services)
	for _, svc := range services {
		item.Services = append(item.Services, agentSessionService{Name: svc, Port: sess.Ports[svc], URL: routeURL(sess.Routes[svc])})
	}
	return item
}

func gitStatusSummary(path string) (string, int) {
	cmd := exec.Command("git", "status", "--porcelain=v1")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return "unknown", 0
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "clean", 0
	}
	return "dirty", len(strings.Split(trimmed, "\n"))
}

func routeURL(route string) string {
	if route == "" {
		return ""
	}
	if strings.HasPrefix(route, "http://") || strings.HasPrefix(route, "https://") {
		return route
	}
	return "https://" + route
}

func latest(times ...time.Time) time.Time {
	var out time.Time
	for _, t := range times {
		if t.After(out) {
			out = t
		}
	}
	return out
}

func init() {
	sessionCmd.AddCommand(sessionContextCmd)
	sessionContextCmd.Flags().BoolVar(&sessionContextJSON, "json", false, "Output JSON")
}
