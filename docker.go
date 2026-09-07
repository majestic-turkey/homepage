package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// dockerClient speaks the Docker Engine API over either a unix socket or TCP.
// Only GET /containers/json is ever used, so a socket proxy limited to
// CONTAINERS=1 is enough to run this.
type dockerClient struct {
	http *http.Client
	base string
}

func newDockerClient(host string) (*dockerClient, error) {
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	u, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("parse docker host %q: %w", host, err)
	}

	switch u.Scheme {
	case "unix":
		sock := u.Path
		if sock == "" {
			sock = u.Opaque
		}
		return &dockerClient{
			// The host part of the URL is ignored for unix sockets, but net/http
			// still requires a syntactically valid one.
			base: "http://docker",
			http: &http.Client{
				Timeout: 10 * time.Second,
				Transport: &http.Transport{
					DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						var d net.Dialer
						return d.DialContext(ctx, "unix", sock)
					},
				},
			},
		}, nil
	case "tcp", "http":
		return &dockerClient{
			base: "http://" + u.Host,
			http: &http.Client{Timeout: 10 * time.Second},
		}, nil
	case "https":
		return &dockerClient{
			base: "https://" + u.Host,
			http: &http.Client{Timeout: 10 * time.Second},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported docker host scheme %q", u.Scheme)
	}
}

type port struct {
	IP          string `json:"IP"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort"`
	Type        string `json:"Type"`
}

type container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	Ports   []port            `json:"Ports"`
	Created int64             `json:"Created"`
}

// containers lists containers. all=false restricts the result to running ones.
func (c *dockerClient) containers(ctx context.Context, all bool) ([]container, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}

	endpoint := c.base + "/containers/json?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query docker: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("docker returned %s: %s", resp.Status, body)
	}

	var out []container
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode docker response: %w", err)
	}
	return out, nil
}

// ping verifies the daemon (or socket proxy) is reachable and permits the one
// call this service makes, so misconfiguration surfaces at startup.
func (c *dockerClient) ping(ctx context.Context) error {
	_, err := c.containers(ctx, false)
	return err
}
