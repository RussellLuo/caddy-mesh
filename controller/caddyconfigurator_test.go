package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"testing"

	"github.com/google/go-cmp/cmp"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	testLogger = zap.New(zap.WriteTo(io.Discard))
	testGetter = func(ctx context.Context, name, namespace string) (*Service, error) {
		return nil, nil
	}
)

func testMakeServicePortsFromServers(servers map[Port]*CaddyServer) map[Key]Port {
	m := make(map[Key]Port)
	for _, s := range servers {
		for _, ts := range s.trafficSplits {
			m[ts.Key] = s.port
		}
		for _, svc := range s.services {
			m[svc.Key] = s.port
		}
	}
	return m
}

func TestCaddyConfigurator_Upsert(t *testing.T) {
	tests := []struct {
		name        string
		servers     map[Port]*CaddyServer
		service     *Service
		wantChanged bool
		wantServers map[Port]*CaddyServer
	}{
		{
			name:    "add new service",
			servers: nil,
			service: &Service{
				Key:     Key{Name: "service-1", Namespace: "test"},
				Port:    Port(80),
				PodPort: 80,
				PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
			},
			wantChanged: true,
			wantServers: map[Port]*CaddyServer{
				Port(80): {
					port: 80,
					services: map[Key]*Service{
						Key{Name: "service-1", Namespace: "test"}: {
							Key:     Key{Name: "service-1", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
						},
					},
				},
			},
		},
		{
			name: "add duplicate service",
			servers: map[Port]*CaddyServer{
				Port(80): {
					port: 80,
					services: map[Key]*Service{
						Key{Name: "service-1", Namespace: "test"}: {
							Key:     Key{Name: "service-1", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
						},
					},
				},
			},
			service: &Service{
				Key:     Key{Name: "service-1", Namespace: "test"},
				Port:    Port(80),
				PodPort: 80,
				PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
			},
			wantChanged: false,
			wantServers: map[Port]*CaddyServer{
				Port(80): {
					port: 80,
					services: map[Key]*Service{
						Key{Name: "service-1", Namespace: "test"}: {
							Key:     Key{Name: "service-1", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
						},
					},
				},
			},
		},
		{
			name: "change service port",
			servers: map[Port]*CaddyServer{
				Port(80): {
					port: 80,
					services: map[Key]*Service{
						Key{Name: "service-1", Namespace: "test"}: {
							Key:     Key{Name: "service-1", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
						},
					},
				},
			},
			service: &Service{
				Key:     Key{Name: "service-1", Namespace: "test"},
				Port:    Port(8080),
				PodPort: 80,
				PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
			},
			wantChanged: true,
			wantServers: map[Port]*CaddyServer{
				Port(8080): {
					port: 8080,
					services: map[Key]*Service{
						Key{Name: "service-1", Namespace: "test"}: {
							Key:     Key{Name: "service-1", Namespace: "test"},
							Port:    Port(8080),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
						},
					},
				},
			},
		},
		{
			name: "change service-2 pod-ips",
			servers: map[Port]*CaddyServer{
				Port(80): {
					port: 80,
					trafficSplits: map[Key]*TrafficSplit{
						Key{Name: "service", Namespace: "test"}: {
							Service: &Service{
								Key:     Key{Name: "service", Namespace: "test"},
								Port:    Port(80),
								PodPort: 80,
								PodIPs:  []string{"127.0.0.2", "127.0.0.3", "127.0.0.4", "127.0.0.5"},
								Annotations: map[string]string{
									TrafficSplitExpr: "false",
									TrafficSplitNew:  "service-2",
									TrafficSplitOld:  "service-1",
								},
							},
							Expression: "false",
							NewService: &Service{
								Key:     Key{Name: "service-2", Namespace: "test"},
								Port:    Port(80),
								PodPort: 80,
								PodIPs:  []string{"127.0.0.4", "127.0.0.5"},
							},
							OldService: &Service{
								Key:     Key{Name: "service-1", Namespace: "test"},
								Port:    Port(80),
								PodPort: 80,
								PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
							},
						},
					},
					services: map[Key]*Service{
						Key{Name: "service", Namespace: "test"}: {
							Key:     Key{Name: "service", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3", "127.0.0.4", "127.0.0.5"},
						},
						Key{Name: "service-1", Namespace: "test"}: {
							Key:     Key{Name: "service-1", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
						},
						Key{Name: "service-2", Namespace: "test"}: {
							Key:     Key{Name: "service-2", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.4", "127.0.0.5"},
						},
					},
				},
			},
			service: &Service{
				Key:     Key{Name: "service-2", Namespace: "test"},
				Port:    Port(80),
				PodPort: 80,
				PodIPs:  []string{"127.0.0.6", "127.0.0.7"},
			},
			wantChanged: true,
			wantServers: map[Port]*CaddyServer{
				Port(80): {
					port: 80,
					trafficSplits: map[Key]*TrafficSplit{
						Key{Name: "service", Namespace: "test"}: {
							Service: &Service{
								Key:     Key{Name: "service", Namespace: "test"},
								Port:    Port(80),
								PodPort: 80,
								PodIPs:  []string{"127.0.0.2", "127.0.0.3", "127.0.0.4", "127.0.0.5"},
								Annotations: map[string]string{
									TrafficSplitExpr: "false",
									TrafficSplitNew:  "service-2",
									TrafficSplitOld:  "service-1",
								},
							},
							Expression: "false",
							NewService: &Service{
								Key:     Key{Name: "service-2", Namespace: "test"},
								Port:    Port(80),
								PodPort: 80,
								PodIPs:  []string{"127.0.0.6", "127.0.0.7"},
							},
							OldService: &Service{
								Key:     Key{Name: "service-1", Namespace: "test"},
								Port:    Port(80),
								PodPort: 80,
								PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
							},
						},
					},
					services: map[Key]*Service{
						Key{Name: "service", Namespace: "test"}: {
							Key:     Key{Name: "service", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3", "127.0.0.4", "127.0.0.5"},
						},
						Key{Name: "service-1", Namespace: "test"}: {
							Key:     Key{Name: "service-1", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
						},
						Key{Name: "service-2", Namespace: "test"}: {
							Key:     Key{Name: "service-2", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.6", "127.0.0.7"},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCaddyConfigurator(testLogger, testGetter)
			if len(tt.servers) > 0 {
				c.servers = tt.servers
				c.servicePorts = testMakeServicePortsFromServers(tt.servers)
			}

			changed := c.Upsert(tt.service)
			if changed != tt.wantChanged {
				t.Errorf("Got (%v) != Want (%v)", changed, tt.wantChanged)
			}

			got := fmt.Sprintf("%v", c.servers)
			want := fmt.Sprintf("%v", tt.wantServers)
			if got != want {
				diff := cmp.Diff(got, want)
				t.Errorf("Want - Got: %s", diff)
			}
		})
	}
}

func TestCaddyConfigurator_Delete(t *testing.T) {
	tests := []struct {
		name        string
		servers     map[Port]*CaddyServer
		service     *Service
		wantChanged bool
		wantServers map[Port]*CaddyServer
	}{
		{
			name:    "delete non-existent service",
			servers: nil,
			service: &Service{
				Key:     Key{Name: "service-1", Namespace: "test"},
				Port:    Port(80),
				PodPort: 80,
				PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
			},
			wantChanged: false,
			wantServers: nil,
		},
		{
			name: "delete existent service",
			servers: map[Port]*CaddyServer{
				Port(80): {
					port: 80,
					services: map[Key]*Service{
						Key{Name: "service-1", Namespace: "test"}: {
							Key:     Key{Name: "service-1", Namespace: "test"},
							Port:    Port(80),
							PodPort: 80,
							PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
						},
					},
				},
			},
			service: &Service{
				Key:     Key{Name: "service-1", Namespace: "test"},
				Port:    Port(80),
				PodPort: 80,
				PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
			},
			wantChanged: true,
			wantServers: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCaddyConfigurator(testLogger, testGetter)
			if len(tt.servers) > 0 {
				c.servers = tt.servers
				c.servicePorts = testMakeServicePortsFromServers(tt.servers)
			}

			changed := c.Delete(tt.service)
			if changed != tt.wantChanged {
				t.Errorf("Got (%v) != Want (%v)", changed, tt.wantChanged)
			}

			got := fmt.Sprintf("%v", c.servers)
			want := fmt.Sprintf("%v", tt.wantServers)
			if got != want {
				diff := cmp.Diff(got, want)
				t.Errorf("Want - Got: %s", diff)
			}
		})
	}
}

func TestCaddyConfigurator_Build(t *testing.T) {
	services := []*Service{
		{
			Key:     Key{Name: "service", Namespace: "test"},
			Port:    Port(80),
			PodPort: 80,
			PodIPs:  []string{"127.0.0.2", "127.0.0.3", "127.0.0.4", "127.0.0.5"},
			Annotations: map[string]string{
				TrafficSplitExpr: "false",
				TrafficSplitNew:  "service-2",
				TrafficSplitOld:  "service-1",
			},
		},
		{
			Key:     Key{Name: "service-1", Namespace: "test"},
			Port:    Port(80),
			PodPort: 80,
			PodIPs:  []string{"127.0.0.2", "127.0.0.3"},
		},
		{
			Key:     Key{Name: "service-2", Namespace: "test"},
			Port:    Port(80),
			PodPort: 80,
			PodIPs:  []string{"127.0.0.4", "127.0.0.5"},
		},
		{
			Key:     Key{Name: "service-3", Namespace: "test"},
			Port:    Port(8080),
			PodPort: 8080,
			PodIPs:  []string{"127.0.0.6", "127.0.0.7"},
		},
	}

	c := NewCaddyConfigurator(testLogger, func(ctx context.Context, name, namespace string) (*Service, error) {
		key := Key{Name: name, Namespace: namespace}
		for _, svc := range services {
			if svc.Key == key {
				return svc, nil
			}
		}
		return nil, nil
	})
	for _, svc := range services {
		c.Upsert(svc)
	}

	config := Builder{}.Build(c.servers)

	wantJSON, err := ioutil.ReadFile("./testdata/config.json")
	if err != nil {
		t.Fatalf("err: %v\n", err)
	}
	var wantConfig map[string]interface{}
	if err := json.Unmarshal(wantJSON, &wantConfig); err != nil {
		t.Fatalf("err: %v\n", err)
	}

	got := fmt.Sprintf("%+v", config)
	want := fmt.Sprintf("%+v", wantConfig)
	if got != want {
		diff := cmp.Diff(got, want)
		t.Errorf("Want - Got: %s", diff)
	}
}

func TestNextMapValueInOrder(t *testing.T) {
	want := []string{"1", "2", "3", "4", "5"}

	m1 := map[Key]string{
		Key{Name: "5", Namespace: "test"}: "5",
		Key{Name: "2", Namespace: "test"}: "2",
		Key{Name: "1", Namespace: "test"}: "1",
		Key{Name: "3", Namespace: "test"}: "3",
		Key{Name: "4", Namespace: "test"}: "4",
	}
	nextV1 := NextMapValueInOrder[map[Key]string](m1)
	var got1 []string
	for {
		v, ok := nextV1()
		if !ok {
			break
		}
		got1 = append(got1, v)
	}
	if !cmp.Equal(got1, want) {
		t.Errorf("Got1 (%+v) != Want (%+v)", got1, want)
	}

	m2 := map[Port]string{
		Port(5): "5",
		Port(2): "2",
		Port(1): "1",
		Port(3): "3",
		Port(4): "4",
	}
	var got2 []string
	nextV2 := NextMapValueInOrder[map[Port]string](m2)
	for {
		v, ok := nextV2()
		if !ok {
			break
		}
		got2 = append(got2, v)
	}
	if !cmp.Equal(got2, want) {
		t.Errorf("Got2 (%+v) != Want (%+v)", got2, want)
	}
}
