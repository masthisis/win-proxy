package main

// service_proxy.go — the live proxy view (groups, selection, latency,
// connections, rules) exposed to the frontend.

import (
	"context"
	"strings"

	"github.com/neurorift/neurorift/internal/engine"
	"github.com/neurorift/neurorift/internal/nettest"
	"github.com/neurorift/neurorift/internal/nr"
)

// ProxyService reads and drives the running core.
type ProxyService struct{ c *Container }

// GroupUI is a proxy group with its members and their last known latency.
type GroupUI struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Now    string   `json:"now"`
	All    []NodeUI `json:"all"`
	Hidden bool     `json:"hidden"` // internal groups the UI collapses
}

// NodeUI is a single node inside a group.
type NodeUI struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Alive bool   `json:"alive"`
	Delay int    `json:"delay"` // last known ms (0 = unknown)
}

// GetGroups returns the group tree (groups first, hidden ones flagged).
// Provider-backed nodes are not top-level entries in /proxies, so their
// types are merged in from /providers/proxies.
func (s *ProxyService) GetGroups() ([]GroupUI, error) {
	api := s.c.Engine.API()
	if api == nil {
		return []GroupUI{}, nil
	}
	proxies, err := api.Proxies(context.Background())
	if err != nil {
		return nil, err
	}
	nodeTypes := map[string]string{}
	if providers, err := api.Providers(context.Background()); err == nil {
		for _, prov := range providers {
			for _, p := range prov.Proxies {
				nodeTypes[p.Name] = p.Type
			}
		}
	}
	var groups []GroupUI
	for _, p := range proxies {
		if !isGroupType(p.Type) {
			continue
		}
		g := GroupUI{
			Name: p.Name, Type: p.Type, Now: p.Now,
			Hidden: p.Name == nr.GroupSpeedTest,
			All:    []NodeUI{},
		}
		for _, member := range p.All {
			m := proxies[member]
			mtype := m.Type
			if mtype == "" {
				mtype = nodeTypes[member]
			}
			delay := s.c.NetTest.LastLatency(member)
			if delay == 0 && len(m.History) > 0 {
				delay = m.History[len(m.History)-1].Delay
			}
			g.All = append(g.All, NodeUI{
				Name: member, Type: mtype, Alive: m.Alive, Delay: delay,
			})
		}
		groups = append(groups, g)
	}
	// Stable order: primary group first, then alphabetical.
	rank := func(g GroupUI) int {
		switch {
		case g.Name == nr.GroupNodeSelect:
			return 0
		case g.Type == "Selector":
			return 1
		case g.Type == "URLTest":
			return 2
		default:
			return 3
		}
	}
	for i := 0; i < len(groups); i++ {
		for j := i + 1; j < len(groups); j++ {
			ri, rj := rank(groups[i]), rank(groups[j])
			if rj < ri || (rj == ri && groups[j].Name < groups[i].Name) {
				groups[i], groups[j] = groups[j], groups[i]
			}
		}
	}
	return groups, nil
}

func isGroupType(t string) bool {
	switch t {
	case "Selector", "URLTest", "Fallback", "LoadBalance", "Relay":
		return true
	}
	return false
}

// SelectProxy switches a group to a member.
func (s *ProxyService) SelectProxy(group, node string) error {
	api := s.c.Engine.API()
	if api == nil {
		return errEngineDown
	}
	return api.SelectProxy(context.Background(), group, node)
}

// TestGroup batch-tests every member of a group.
func (s *ProxyService) TestGroup(group string) (*nettest.LatencyBatch, error) {
	return s.c.NetTest.TestGroupLatency(context.Background(), group)
}

// TestAllNodes tests every node of the active profile.
func (s *ProxyService) TestAllNodes() (*nettest.LatencyBatch, error) {
	return s.c.NetTest.TestAllNodes(context.Background())
}

// TestNode tests a single node.
func (s *ProxyService) TestNode(node string) (*nettest.LatencySample, error) {
	res, err := s.c.NetTest.TestNodeLatency(context.Background(), node)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// GetMode returns the routing mode.
func (s *ProxyService) GetMode() (string, error) {
	api := s.c.Engine.API()
	if api == nil {
		return "", errEngineDown
	}
	return api.GetMode(context.Background())
}

// SetMode switches routing mode: rule | global | direct.
func (s *ProxyService) SetMode(mode string) error {
	api := s.c.Engine.API()
	if api == nil {
		return errEngineDown
	}
	if mode != "rule" && mode != "global" && mode != "direct" {
		mode = "rule"
	}
	if err := api.SetMode(context.Background(), mode); err != nil {
		return err
	}
	// Keep settings in sync so restarts preserve the mode.
	cur := s.c.Settings.Get()
	if cur.Mode != mode {
		cur.Mode = mode
		_, _ = s.c.Settings.Replace(cur)
	}
	return nil
}

// ConnectionsSnapshot returns the live connections snapshot.
func (s *ProxyService) Connections() (*engine.ConnectionsSnapshot, error) {
	api := s.c.Engine.API()
	if api == nil {
		return nil, errEngineDown
	}
	env, err := api.Connections(context.Background())
	if err != nil {
		return nil, err
	}
	items := make([]engine.ConnItem, 0, len(env.Connections))
	for _, cn := range env.Connections {
		host := cn.Metadata.Host
		if host == "" {
			host = cn.Metadata.DestinationIP + ":" + cn.Metadata.DestinationPort
		}
		chain := strings.Join(reverse(cn.Chains), " → ")
		items = append(items, engine.ConnItem{
			ID: cn.ID, Host: host, Network: cn.Metadata.Network, Type: cn.Metadata.Type,
			Process: cn.Metadata.Process, Chain: chain, Rule: cn.Rule,
			Down: cn.Download, Up: cn.Upload, Start: cn.Start,
		})
	}
	return &engine.ConnectionsSnapshot{
		Items: items, DownloadTotal: env.DownloadTotal, UploadTotal: env.UploadTotal,
	}, nil
}

func reverse(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// CloseConnection terminates one connection.
func (s *ProxyService) CloseConnection(id string) error {
	api := s.c.Engine.API()
	if api == nil {
		return errEngineDown
	}
	return api.CloseConnection(context.Background(), id)
}

// CloseAllConnections terminates every connection.
func (s *ProxyService) CloseAllConnections() error {
	api := s.c.Engine.API()
	if api == nil {
		return errEngineDown
	}
	return api.CloseAllConnections(context.Background())
}

// Rules returns the active rules.
func (s *ProxyService) Rules() ([]engine.Rule, error) {
	api := s.c.Engine.API()
	if api == nil {
		return nil, errEngineDown
	}
	return api.Rules(context.Background())
}
