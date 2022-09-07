package main

import (
	"context"

	"github.com/alecthomas/kong"
	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/RussellLuo/caddy-mesh/controller"
	"github.com/RussellLuo/caddy-mesh/dnspatcher"
)

type Context struct {
	logger logr.Logger
}

type RunCmd struct {
	ProxyNamespace    string   `arg:"" name:"proxy-namespace" help:"the namespace of caddy-mesh-proxy service"`
	IgnoredNamespaces []string `name:"ignored-namespace" help:"the namespaces to ignore"`
}

func (r *RunCmd) Run(ctx *Context) error {
	config := &controller.Config{
		ProxyNamespace:    r.ProxyNamespace,
		IgnoredNamespaces: r.IgnoredNamespaces,
	}
	c, err := controller.New(ctx.logger, config)
	if err != nil {
		return err
	}
	return c.Run()
}

type InitCmd struct {
	ProxyNamespace string `arg:"" name:"proxy-namespace" help:"the namespace of caddy-mesh-proxy service"`
}

func (i *InitCmd) Run(ctx *Context) error {
	patcher, err := dnspatcher.New(ctx.logger)
	if err != nil {
		return err
	}
	return patcher.Patch(context.Background(), i.ProxyNamespace)
}

var CLI struct {
	Run  RunCmd  `cmd:"" help:"Run controller."`
	Init InitCmd `cmd:"" help:"Init CoreDNS config."`
}

func main() {
	logf.SetLogger(zap.New())
	logger := logf.Log.WithName("caddy-mesh-controller")

	ctx := kong.Parse(&CLI)
	err := ctx.Run(&Context{logger: logger})
	ctx.FatalIfErrorf(err)
}
