// Package openscad reads the projects of the OpenSCAD workbench (openscad_viewer) over its REST API.
package openscad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound is returned for a project the workbench doesn't have.
var ErrNotFound = errors.New("OpenSCAD project not found")

// ErrNotRendered is returned for a project with no rendered model yet.
var ErrNotRendered = errors.New("OpenSCAD project has no rendered model yet")

// Project is one workbench project, as the UI lists it.
type Project struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Updated     any    `json:"updated"`
	Version     any    `json:"version"`
	Rendered    bool   `json:"rendered"`
}

// Client talks to the workbench at BaseURL (for example http://openscad:8020).
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

// New returns a client with a timeout on every request.
func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) get(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	return c.HTTP.Do(req)
}

// List returns the projects, each with whether it has a rendered model.
func (c *Client) List(ctx context.Context) ([]Project, error) {
	res, err := c.get(ctx, http.MethodGet, "/api/projects")
	if err != nil {
		return nil, fmt.Errorf("OpenSCAD workbench: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenSCAD workbench: %s", res.Status)
	}
	var list []Project
	if err := json.NewDecoder(res.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("OpenSCAD workbench: %w", err)
	}
	for i := range list {
		list[i].Rendered = c.rendered(ctx, list[i].Name)
	}
	return list, nil
}

func (c *Client) rendered(ctx context.Context, name string) bool {
	res, err := c.get(ctx, http.MethodHead, "/api/projects/"+url.PathEscape(name)+"/model.stl")
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// Project finds one project by name.
func (c *Client) Project(ctx context.Context, name string) (Project, error) {
	list, err := c.List(ctx)
	if err != nil {
		return Project{}, err
	}
	for _, p := range list {
		if p.Name == name {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}

// STL returns the last rendered model of a project (at most 64 MB).
func (c *Client) STL(ctx context.Context, name string) ([]byte, error) {
	res, err := c.get(ctx, http.MethodGet, "/api/projects/"+url.PathEscape(name)+"/model.stl")
	if err != nil {
		return nil, fmt.Errorf("OpenSCAD workbench: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrNotRendered
	default:
		return nil, fmt.Errorf("OpenSCAD workbench: %s", res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 64<<20))
}
