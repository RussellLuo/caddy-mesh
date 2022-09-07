package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/RussellLuo/caddy-mesh/dnspatcher"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
)

const (
	TrafficSplitExpr = "mesh.caddyserver.com/trafficsplit-expression"
	TrafficSplitNew  = "mesh.caddyserver.com/trafficsplit-new-service"
	TrafficSplitOld  = "mesh.caddyserver.com/trafficsplit-old-service"
)

type ServiceGetter func(ctx context.Context, name, namespace string) (*Service, error)

type CaddyConfigurator struct {
	logger        logr.Logger
	serviceGetter ServiceGetter

	mu           sync.Mutex
	servers      map[Port]*CaddyServer
	servicePorts map[Key]Port
	client       *http.Client
}

func NewCaddyConfigurator(logger logr.Logger, getter ServiceGetter) *CaddyConfigurator {
	return &CaddyConfigurator{
		logger:        logger,
		serviceGetter: getter,
		servers:       make(map[Port]*CaddyServer),
		servicePorts:  make(map[Key]Port),
		client:        &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *CaddyConfigurator) Upsert(svc *Service) (changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	oldPort, ok := c.servicePorts[svc.Key]
	if ok && oldPort != svc.Port {
		// port has been changed, update the server associated with oldPort.
		if s, ok := c.servers[oldPort]; ok {
			if s.Delete(svc) {
				changed = true
			}
			if s.IsEmpty() {
				delete(c.servers, oldPort)
			}
		}
	}

	c.servicePorts[svc.Key] = svc.Port

	s, ok := c.servers[svc.Port]
	if !ok {
		s = NewCaddyServer(c.logger, c.serviceGetter, svc.Port)
		c.servers[svc.Port] = s
		changed = true
	}

	if s.Upsert(svc) {
		changed = true
	}

	return changed
}

func (c *CaddyConfigurator) Delete(svc *Service) (changed bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	port := c.servicePorts[svc.Key]
	s, ok := c.servers[port]
	if !ok {
		return false
	}

	changed = s.Delete(svc)
	if s.IsEmpty() {
		delete(c.servers, svc.Port)
	}
	return changed
}

func (c *CaddyConfigurator) Apply(ips []string) (n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	config := Builder{}.Build(c.servers)
	data, err := json.Marshal(config)
	if err != nil {
		return 0, err
	}

	for _, ip := range ips {
		if err := c.apply(ip, data); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (c *CaddyConfigurator) apply(ip string, data []byte) error {
	resp, err := c.client.Post(makeURL(ip), "application/json", bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errMsg struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errMsg); err != nil {
			return err
		}
		return fmt.Errorf(errMsg.Error)
	}

	return nil
}

type CaddyServer struct {
	logger        logr.Logger
	serviceGetter ServiceGetter

	port          Port
	trafficSplits map[Key]*TrafficSplit
	services      map[Key]*Service
}

func NewCaddyServer(logger logr.Logger, getter ServiceGetter, port Port) *CaddyServer {
	return &CaddyServer{
		logger:        logger,
		serviceGetter: getter,
		port:          port,
		trafficSplits: make(map[Key]*TrafficSplit),
		services:      make(map[Key]*Service),
	}
}

func (s *CaddyServer) Upsert(svc *Service) (changed bool) {
	ts := s.toTrafficSplit(svc)
	if ts != nil {
		// svc has Traffic-Split definitions, add it as a TrafficSplit.
		existingTs, ok := s.trafficSplits[svc.Key]
		if !ok || !cmp.Equal(ts, existingTs) {
			s.trafficSplits[svc.Key] = ts
			changed = true
		}
	} else {
		// If svc ever has Traffic-Split definitions, we should remove it
		// from the TrafficSplit map since this is no longer the case.
		if _, ok := s.trafficSplits[svc.Key]; ok {
			delete(s.trafficSplits, svc.Key)
			changed = true
		} else {
			// If svc happens to be OldService or NewService of any TrafficSplit,
			// try to update the corresponding values.
			for _, ts := range s.trafficSplits {
				if svc.Key == ts.NewService.Key && !cmp.Equal(svc, ts.NewService) {
					ts.NewService = svc
					changed = true
				}
				if svc.Key == ts.OldService.Key && !cmp.Equal(svc, ts.OldService) {
					ts.OldService = svc
					changed = true
				}
			}
		}
	}

	// Also add svc as a Service.
	existingSvc, ok := s.services[svc.Key]
	if !ok || !cmp.Equal(svc, existingSvc) {
		s.services[svc.Key] = svc
		changed = true
	}

	return changed
}

func (s *CaddyServer) Delete(svc *Service) (changed bool) {
	// Just remove svc if it's a TrafficSplit.
	//
	// NOTE: No need to remove svc if it's OldService or NewService of
	// any TrafficSplit, since typically the deletion occurs in the cleanup
	// stages of canary releases.
	if _, ok := s.trafficSplits[svc.Key]; ok {
		delete(s.trafficSplits, svc.Key)
		changed = true
	}

	if _, ok := s.services[svc.Key]; ok {
		delete(s.services, svc.Key)
		changed = true
	}

	return changed
}

func (s *CaddyServer) IsEmpty() bool {
	return len(s.trafficSplits) == 0 && len(s.services) == 0
}

func (s *CaddyServer) toTrafficSplit(svc *Service) *TrafficSplit {
	var expression, oldName, newName string
	for key, value := range svc.Annotations {
		switch key {
		case TrafficSplitExpr:
			expression = value
		case TrafficSplitNew:
			newName = value
		case TrafficSplitOld:
			oldName = value
		}
	}

	if expression == "" || newName == "" || oldName == "" {
		return nil
	}

	newService, err := s.serviceGetter(context.Background(), newName, svc.Namespace)
	if err != nil {
		s.logger.Error(err, "could not get Kubernetes Service", "name", newName, "namespace", svc.Namespace)
		return nil
	}

	oldService, err := s.serviceGetter(context.Background(), oldName, svc.Namespace)
	if err != nil {
		s.logger.Error(err, "could not get Kubernetes Service", "name", oldName, "namespace", svc.Namespace)
		return nil
	}

	return &TrafficSplit{
		Service:    svc,
		Expression: expression,
		NewService: newService,
		OldService: oldService,
	}
}

// String implements fmt.Stringer. This is mainly used for testing purpose.
func (s *CaddyServer) String() string {
	return fmt.Sprintf("{Port:%d TrafficSplits:%v Services:%v}",
		s.port,
		s.trafficSplits,
		s.services,
	)
}

type Port int

func (p Port) SortString() string {
	return strconv.Itoa(int(p))
}

type Key struct {
	Name      string
	Namespace string
}

func (k Key) SortString() string {
	return k.Name + "." + k.Namespace
}

// Service is a normal Kubernetes Service.
type Service struct {
	Key

	// TODO: Add support for multiple ports per Service
	Port        Port
	PodPort     int
	PodIPs      []string
	Annotations map[string]string
}

// String implements fmt.Stringer. This is mainly used for testing purpose.
func (s *Service) String() string {
	return fmt.Sprintf("%+v", *s)
}

// TrafficSplit a Service with Traffic-Split definitions.
//
// Note that the current implementation is inspired by but a little different with
// the SMI specification (https://github.com/servicemeshinterface/smi-spec/blob/main/apis/traffic-split/v1alpha4/traffic-split.md).
type TrafficSplit struct {
	*Service

	Expression string
	NewService *Service
	OldService *Service
}

// String implements fmt.Stringer. This is mainly used for testing purpose.
func (t *TrafficSplit) String() string {
	return fmt.Sprintf("%+v", *t)
}

type Route map[string]interface{}

type Builder struct{}

func (b Builder) Build(servers map[Port]*CaddyServer) map[string]interface{} {
	cfgServers := make(map[string]interface{})

	nextServer := NextMapValueInOrder[map[Port]*CaddyServer](servers)
	for {
		s, ok := nextServer()
		if !ok {
			break
		}

		nextTs := NextMapValueInOrder[map[Key]*TrafficSplit](s.trafficSplits)
		var tsRoutes []Route
		for {
			ts, ok := nextTs()
			if !ok {
				break
			}
			tsRoutes = append(tsRoutes, b.buildTrafficSplit(ts))
		}

		nextSvc := NextMapValueInOrder[map[Key]*Service](s.services)
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

	r := Route{
		"handle": []map[string]interface{}{
			{
				"handler":   "reverse_proxy",
				"upstreams": upstreams,
			},
		},
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
