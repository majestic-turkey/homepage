package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Label namespace read off each container. Only containers that explicitly set
// homepage.enable=true are ever exposed, so nothing appears on a public page by
// accident.
const (
	labelEnable      = "homepage.enable"
	labelTitle       = "homepage.title"
	labelURL         = "homepage.url"
	labelDescription = "homepage.description"
	labelIcon        = "homepage.icon"
	labelGroup       = "homepage.group"
	labelOrder       = "homepage.order"
)

const defaultGroup = "Services"

// service is the public shape of a container. It deliberately omits the image,
// internal ports and container id: those describe the host's internals and this
// page is served to the open internet.
type service struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Group       string `json:"group"`
	Status      string `json:"status"`
	Uptime      string `json:"uptime,omitempty"`

	order int
}

// group bundles services under a heading for rendering.
type group struct {
	Name     string    `json:"name"`
	Services []service `json:"services"`
}

// buildServices turns raw containers into the opt-in, public-safe list.
// publicHost, when set, lets a container omit homepage.url and have a link
// derived from its first published port instead.
func buildServices(containers []container, publicHost string) []group {
	var found []service

	for _, c := range containers {
		if !isTrue(c.Labels[labelEnable]) {
			continue
		}

		name := containerName(c)
		svc := service{
			Name:        firstNonEmpty(c.Labels[labelTitle], name),
			URL:         strings.TrimSpace(c.Labels[labelURL]),
			Description: strings.TrimSpace(c.Labels[labelDescription]),
			Icon:        strings.TrimSpace(c.Labels[labelIcon]),
			Group:       firstNonEmpty(strings.TrimSpace(c.Labels[labelGroup]), defaultGroup),
			Status:      containerStatus(c),
			Uptime:      uptime(c),
			order:       parseOrder(c.Labels[labelOrder]),
		}

		if svc.URL == "" {
			svc.URL = derivedURL(c, publicHost)
		}
		if svc.URL == "" {
			// A card with no link is just noise; skip it rather than render a
			// dead tile.
			continue
		}

		found = append(found, svc)
	}

	return groupServices(found)
}

func groupServices(services []service) []group {
	byName := map[string][]service{}
	var order []string
	for _, s := range services {
		if _, seen := byName[s.Group]; !seen {
			order = append(order, s.Group)
		}
		byName[s.Group] = append(byName[s.Group], s)
	}

	sort.Strings(order)

	groups := make([]group, 0, len(order))
	for _, name := range order {
		items := byName[name]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].order != items[j].order {
				return items[i].order < items[j].order
			}
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
		groups = append(groups, group{Name: name, Services: items})
	}
	return groups
}

// containerStatus collapses Docker's state and healthcheck into one of
// healthy / starting / unhealthy / running / stopped for the status dot.
func containerStatus(c container) string {
	status := strings.ToLower(c.Status)
	switch {
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(health: starting)"):
		return "starting"
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	}

	if strings.EqualFold(c.State, "running") {
		return "running"
	}
	return "stopped"
}

// derivedURL falls back to the first published TCP port when a container has no
// homepage.url label. Only used when PUBLIC_HOST is configured.
func derivedURL(c container, publicHost string) string {
	if publicHost == "" {
		return ""
	}

	best := 0
	for _, p := range c.Ports {
		if p.PublicPort == 0 || !strings.EqualFold(p.Type, "tcp") {
			continue
		}
		// Prefer the lowest published port for stable output across restarts.
		if best == 0 || p.PublicPort < best {
			best = p.PublicPort
		}
	}
	if best == 0 {
		return ""
	}

	scheme := "http"
	if best == 443 {
		scheme = "https"
	}
	if best == 80 || best == 443 {
		return fmt.Sprintf("%s://%s", scheme, publicHost)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, publicHost, best)
}

func containerName(c container) string {
	if len(c.Names) == 0 {
		return "unknown"
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

func uptime(c container) string {
	if !strings.EqualFold(c.State, "running") || c.Created == 0 {
		return ""
	}
	d := time.Since(time.Unix(c.Created, 0))
	switch {
	case d < time.Minute:
		return "just started"
	case d < time.Hour:
		return fmt.Sprintf("up %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("up %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("up %dd", int(d.Hours()/24))
	}
}

func parseOrder(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 1000
	}
	return n
}

func isTrue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
