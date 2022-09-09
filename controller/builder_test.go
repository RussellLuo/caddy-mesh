package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestBuilder_Build(t *testing.T) {
	services := []*Service{
		{
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
			Definitions: &Definitions{
				TimeoutDialTimeout:  10 * time.Second,
				TimeoutReadTimeout:  10 * time.Second,
				TimeoutWriteTimeout: 10 * time.Second,
			},
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
	got, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatalf("err: %v\n", err)
	}

	want, err := ioutil.ReadFile("./testdata/config.json")
	if err != nil {
		t.Fatalf("err: %v\n", err)
	}

	if !bytes.Equal(got, want) {
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
