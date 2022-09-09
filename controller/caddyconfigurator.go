package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/RussellLuo/structool"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
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
	d := svc.Definitions
	if d == nil {
		return nil
	}

	if d.TrafficSplitExpression == "" || d.TrafficSplitNewService == "" || d.TrafficSplitOldService == "" {
		return nil
	}

	newService, err := s.serviceGetter(context.Background(), d.TrafficSplitNewService, svc.Namespace)
	if err != nil {
		s.logger.Error(err, "could not get Kubernetes Service", "name", d.TrafficSplitNewService, "namespace", svc.Namespace)
		return nil
	}

	oldService, err := s.serviceGetter(context.Background(), d.TrafficSplitOldService, svc.Namespace)
	if err != nil {
		s.logger.Error(err, "could not get Kubernetes Service", "name", d.TrafficSplitOldService, "namespace", svc.Namespace)
		return nil
	}

	return &TrafficSplit{
		Service:    svc,
		Expression: d.TrafficSplitExpression,
		NewService: newService,
		OldService: oldService,
	}
}

// String implements fmt.Stringer. This is mainly used for testing purpose.
func (s *CaddyServer) String() string {
	if s == nil {
		return "<nil>"
	}
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
	Definitions *Definitions
}

// String implements fmt.Stringer. This is mainly used for testing purpose.
func (s *Service) String() string {
	if s == nil {
		return "<nil>"
	}
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
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%+v", *t)
}

type Definitions struct {
	TimeoutDialTimeout  time.Duration `json:"mesh.caddyserver.com/timeout-dial-timeout,omitempty"`
	TimeoutReadTimeout  time.Duration `json:"mesh.caddyserver.com/timeout-read-timeout,omitempty"`
	TimeoutWriteTimeout time.Duration `json:"mesh.caddyserver.com/timeout-write-timeout,omitempty"`

	TrafficSplitExpression string `json:"mesh.caddyserver.com/traffic-split-expression,omitempty"`
	TrafficSplitNewService string `json:"mesh.caddyserver.com/traffic-split-new-service,omitempty"`
	TrafficSplitOldService string `json:"mesh.caddyserver.com/traffic-split-old-service,omitempty"`
}

func NewDefinitions(annotations map[string]string) (*Definitions, error) {
	codec := structool.New().TagName("json").DecodeHook(structool.DecodeStringToDuration)

	d := new(Definitions)
	if err := codec.Decode(annotations, d); err != nil {
		return nil, err
	}

	return d, nil
}

// String implements fmt.Stringer. This is mainly used for testing purpose.
func (d *Definitions) String() string {
	if d == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%+v", *d)
}
