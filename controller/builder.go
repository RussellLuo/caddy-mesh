package controller

import (
	"fmt"
	"sort"

	"github.com/RussellLuo/caddy-mesh/dnspatcher"
)

type Route map[string]interface{}

type Builder struct{}

func (b Builder) Build(servers map[Port]*CaddyServer) map[string]interface{} {
	cfgServers := make(map[string]interface{})

	nextServer := NextMapValueInOrder(servers)
	for {
		s, ok := nextServer()
		if !ok {
			break
		}

		nextTs := NextMapValueInOrder(s.trafficSplits)
		var tsRoutes []Route
		for {
			ts, ok := nextTs()
			if !ok {
				break
			}
			tsRoutes = append(tsRoutes, b.buildTrafficSplit(ts))
		}

		nextSvc := NextMapValueInOrder(s.services)
		var svcRoutes []Route
		for {
			svc, ok := nextSvc()
			if !ok {
				break
			}
			svcRoutes = append(svcRoutes, b.buildService(svc))
		}

		var routes []Route
		if len(tsRoutes) > 0 {
			routes = append(routes, b.buildSubRoute(tsRoutes, nil))
		}
		if len(svcRoutes) > 0 {
			routes = append(routes, b.buildSubRoute(svcRoutes, nil))
		}

		cfgServers[fmt.Sprintf("server-%d", s.port)] = map[string]interface{}{
			"automatic_https": map[string]interface{}{
				"disable": true,
			},
			"listen": []string{fmt.Sprintf(":%d", s.port)},
			"routes": routes,
		}
	}

	return map[string]interface{}{
		"admin": map[string]interface{}{
			"listen": "0.0.0.0:2019",
		},
		"apps": map[string]interface{}{
			"http": map[string]interface{}{
				"servers": cfgServers,
			},
		},
	}
}

func (b Builder) buildTrafficSplit(ts *TrafficSplit) Route {
	matchExpr := map[string]interface{}{
		"expression": ts.Expression,
	}
	routes := []Route{
		b.buildReverseProxy(ts.NewService, matchExpr),
		b.buildReverseProxy(ts.OldService, nil),
	}

	matchHost := map[string]interface{}{
		"host": []string{fullHost(ts.Name, ts.Namespace)},
	}
	return b.buildSubRoute(routes, matchHost)
}

func (b Builder) buildService(svc *Service) Route {
	match := map[string]interface{}{
		"host": []string{fullHost(svc.Name, svc.Namespace)},
	}
	return b.buildReverseProxy(svc, match)
}

func (b Builder) buildSubRoute(routes []Route, match map[string]interface{}) Route {
	r := Route{
		"handle": []map[string]interface{}{
			{
				"handler": "subroute",
				"routes":  routes,
			},
		},
	}
	if len(match) > 0 {
		r["match"] = []map[string]interface{}{match}
	}
	return r
}

func (b Builder) buildReverseProxy(svc *Service, match map[string]interface{}) Route {
	var upstreams []map[string]interface{}
	for _, ip := range svc.PodIPs {
		upstreams = append(upstreams, map[string]interface{}{
			"dial": fmt.Sprintf("%s:%d", ip, svc.PodPort),
		})
	}

	loadBalancing := map[string]interface{}{
		"selection_policy": map[string]interface{}{
			"policy": "round_robin",
		},
	}
	if d := svc.Definitions; d != nil {
		if d.RetryCount > 0 {
			loadBalancing["retries"] = d.RetryCount
		}
		if d.RetryDuration > 0 {
			loadBalancing["try_duration"] = d.RetryDuration
		}
		if d.RetryOn != "" {
			loadBalancing["retry_match"] = []map[string]interface{}{
				{
					"expression": d.RetryOn,
				},
			}
		}
	}

	var transport map[string]interface{}
	if d := svc.Definitions; d != nil {
		if d.TimeoutDialTimeout > 0 || d.TimeoutReadTimeout > 0 || d.TimeoutWriteTimeout > 0 {
			transport = map[string]interface{}{
				"protocol": "http",
			}
			if d.TimeoutDialTimeout > 0 {
				transport["dial_timeout"] = d.TimeoutDialTimeout
			}
			if d.TimeoutReadTimeout > 0 {
				transport["read_timeout"] = d.TimeoutReadTimeout
			}
			if d.TimeoutWriteTimeout > 0 {
				transport["write_timeout"] = d.TimeoutWriteTimeout
			}
		}
	}

	reverseProxy := map[string]interface{}{
		"handler":        "reverse_proxy",
		"load_balancing": loadBalancing,
		"upstreams":      upstreams,
	}
	if len(transport) > 0 {
		reverseProxy["transport"] = transport
	}

	r := Route{
		"handle": []map[string]interface{}{reverseProxy},
	}
	if len(match) > 0 {
		r["match"] = []map[string]interface{}{match}
	}

	return r
}

func fullHost(name, namespace string) string {
	return name + "." + namespace + "." + dnspatcher.CaddyMeshDomain
}

func makeURL(ip string) string {
	return fmt.Sprintf("http://%s:2019/load", ip)
}

type SortStringer interface {
	comparable
	SortString() string
}

// NextMapValueInOrder iterates the given map in ascending order of its key, and
// returns the corresponding value. It will return ok=false if there's no value left.
func NextMapValueInOrder[T map[K]V, K SortStringer, V any](m T) func() (value V, ok bool) {
	var keys []K
	for k := range m {
		keys = append(keys, k)
	}
	sortSlice[K](keys)
	var i int

	return func() (V, bool) {
		if i >= len(keys) {
			var zero V
			return zero, false
		}

		value, ok := m[keys[i]]
		i++
		return value, ok
	}
}

func sortSlice[T SortStringer](s []T) {
	sort.Slice(s, func(i, j int) bool {
		return s[i].SortString() < s[j].SortString()
	})
}
