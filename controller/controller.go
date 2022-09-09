package controller

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/discovery/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/RussellLuo/caddy-mesh/dnspatcher"
)

type Config struct {
	ProxyNamespace    string
	IgnoredNamespaces []string
}

type Controller struct {
	logger       logr.Logger
	manager      manager.Manager
	configurator *CaddyConfigurator
	client       client.Client
	config       *Config
}

func New(logger logr.Logger, cfg *Config) (*Controller, error) {
	mgr, err := manager.New(config.GetConfigOrDie(), manager.Options{
		ClientDisableCacheFor: []client.Object{
			&corev1.ConfigMap{},
			&corev1.Secret{},
		},
	})
	if err != nil {
		return nil, err
	}

	c := &Controller{
		logger:  logger,
		manager: mgr,
		client:  mgr.GetClient(),
		config:  cfg,
	}
	c.configurator = NewCaddyConfigurator(logger, c.getService)

	err = builder.
		ControllerManagedBy(mgr).
		WithEventFilter(IgnoreNamespaces(metav1.NamespaceSystem)).
		WithEventFilter(IgnoreNamespaces(cfg.IgnoredNamespaces...)).
		WithEventFilter(IgnoreService(metav1.NamespaceDefault, "kubernetes")).
		WithEventFilter(IgnoreLabel("app", "caddy-mesh")).
		For(&corev1.Service{}).
		Owns(&v1beta1.EndpointSlice{}). // Watch for EndpointSlice events
		Complete(reconcile.Func(c.Reconcile))
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Controller) Run() error {
	return c.manager.Start(signals.SetupSignalHandler())
}

// Reconcile implements the business logic.
func (c *Controller) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	c.logger.Info("Reconciling service", "name", req.Name, "namespace", req.Namespace)

	upstreamService := &corev1.Service{}
	err := c.client.Get(ctx, req.NamespacedName, upstreamService)
	if err != nil && !errors.IsNotFound(err) {
		return reconcile.Result{}, err
	}

	proxyService := &corev1.Service{}
	if err1 := c.client.Get(ctx, client.ObjectKey{Name: dnspatcher.CaddyMeshProxyName, Namespace: c.config.ProxyNamespace}, proxyService); err1 != nil {
		return reconcile.Result{}, err1
	}

	proxyIPs, err1 := c.getPodIPs(ctx, proxyService)
	if err != nil {
		return reconcile.Result{}, err1
	}

	if errors.IsNotFound(err) {
		svc := &Service{Key: Key{Name: req.Name, Namespace: req.Namespace}}
		if c.configurator.Delete(svc) {
			c.logger.Info("Deleting Caddy upstream backends", "host", fullHost(upstreamService.Name, upstreamService.Namespace))
			_, err = c.configurator.Apply(proxyIPs)
			return reconcile.Result{}, err
		}

		c.logger.Info("No changes made, since all Caddy instances are in-sync")
		return reconcile.Result{}, nil
	}

	svc, err := c.toService(ctx, upstreamService)
	if err != nil {
		return reconcile.Result{}, err
	}
	if c.configurator.Upsert(svc) {
		n, err := c.configurator.Apply(proxyIPs)
		c.logger.Info(fmt.Sprintf("%d/%d Caddy instances haven been synchronized successfully", n, len(proxyIPs)))
		return reconcile.Result{}, err
	}

	c.logger.Info("No changes made, since all Caddy instances are in-sync")
	return reconcile.Result{}, nil
}

func (c *Controller) getService(ctx context.Context, name, namespace string) (*Service, error) {
	svc := &corev1.Service{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, svc); err != nil {
		return nil, err
	}
	return c.toService(ctx, svc)
}

func (c *Controller) toService(ctx context.Context, svc *corev1.Service) (*Service, error) {
	ips, err := c.getPodIPs(ctx, svc)
	if err != nil {
		return nil, err
	}

	definitions, err := NewDefinitions(svc.Annotations)
	if err != nil {
		c.logger.Error(err, "bad service annotations")
	}

	port := svc.Spec.Ports[0] // TODO: Add support for multiple ports per Service
	return &Service{
		Key: Key{
			Name:      svc.Name,
			Namespace: svc.Namespace,
		},
		Port:        Port(int(port.Port)),
		PodPort:     int(port.TargetPort.IntVal),
		PodIPs:      ips,
		Definitions: definitions,
	}, nil
}

func (c *Controller) getPodIPs(ctx context.Context, svc *corev1.Service) ([]string, error) {
	pods := &corev1.PodList{}
	if err := c.client.List(ctx, pods, client.InNamespace(svc.Namespace), client.MatchingLabels(svc.Spec.Selector)); err != nil {
		return nil, err
	}

	var ips []string
	for _, pod := range pods.Items {
		ips = append(ips, pod.Status.PodIP)
	}
	sort.Strings(ips) // Keep the ips in a fixed order.

	return ips, nil
}
