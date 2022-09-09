package controller

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

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
								Definitions: &Definitions{
									TrafficSplitExpression: "false",
									TrafficSplitNewService: "service-2",
									TrafficSplitOldService: "service-1",
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
								Definitions: &Definitions{
									TrafficSplitExpression: "false",
									TrafficSplitNewService: "service-2",
									TrafficSplitOldService: "service-1",
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

func TestNewDefinitions(t *testing.T) {
	tests := []struct {
		name    string
		in      map[string]string
		want    *Definitions
		wantErr string
	}{
		{
			name: "nil",
			in:   nil,
			want: &Definitions{},
		},
		{
			name: "empty",
			in:   map[string]string{},
			want: &Definitions{},
		},
		{
			name: "timeout",
			in: map[string]string{
				"mesh.caddyserver.com/timeout-dial-timeout":  "10s",
				"mesh.caddyserver.com/timeout-read-timeout":  "10s",
				"mesh.caddyserver.com/timeout-write-timeout": "10s",
			},
			want: &Definitions{
				TimeoutDialTimeout:  10 * time.Second,
				TimeoutReadTimeout:  10 * time.Second,
				TimeoutWriteTimeout: 10 * time.Second,
			},
		},
		{
			name: "bad timeout",
			in: map[string]string{
				"mesh.caddyserver.com/timeout-dial-timeout": "5",
			},
			want:    nil,
			wantErr: "1 error(s) decoding:\n\n* error decoding 'mesh.caddyserver.com/timeout-dial-timeout': time: missing unit in duration \"5\"",
		},
		{
			name: "traffic split",
			in: map[string]string{
				"mesh.caddyserver.com/traffic-split-expression":  "false",
				"mesh.caddyserver.com/traffic-split-new-service": "service-2",
				"mesh.caddyserver.com/traffic-split-old-service": "service-1",
			},
			want: &Definitions{
				TrafficSplitExpression: "false",
				TrafficSplitNewService: "service-2",
				TrafficSplitOldService: "service-1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewDefinitions(tt.in)

			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if gotErr != tt.wantErr {
				t.Errorf("Err: Got (%q) != Want (%q)", gotErr, tt.wantErr)
			}

			if !cmp.Equal(got, tt.want) {
				diff := cmp.Diff(got, tt.want)
				t.Errorf("Want - Got: %s", diff)
			}
		})
	}
}
