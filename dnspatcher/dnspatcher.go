package dnspatcher

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	CaddyMeshDomain    = "caddy.mesh"
	CaddyMeshProxyName = "caddy-mesh-proxy"

	caddyMeshDNSBegin = "### Caddy Mesh Begin"
	caddyMeshDNSEnd   = "### Caddy Mesh End"
)

type DNSPatcher struct {
	logger logr.Logger
	client client.Client
}

func New(logger logr.Logger) (*DNSPatcher, error) {
	cli, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		return nil, err
	}

	return &DNSPatcher{
		logger: logger,
		client: cli,
	}, nil
}

func (p *DNSPatcher) Patch(ctx context.Context, proxyNamespace string) error {
	p.logger.Info("Patching CoreDNS config")

	deployment := &appsv1.Deployment{}
	if err := p.client.Get(ctx, key(metav1.NamespaceSystem, "coredns"), deployment); err != nil {
		return err
	}

	proxyService := &corev1.Service{}
	if err := p.client.Get(ctx, key(proxyNamespace, CaddyMeshProxyName), proxyService); err != nil {
		return err
	}
	clusterIP := proxyService.Spec.ClusterIP
	if clusterIP == "" {
		return fmt.Errorf("service %q in namespace %q has no ClusterIP", CaddyMeshProxyName, proxyNamespace)
	}

	patched, err := p.patch(ctx, deployment, clusterIP)
	if err != nil {
		return fmt.Errorf("could not patch CoreDNS config: %w", err)
	}
	if !patched {
		p.logger.Info("No changes made, since CoreDNS config has already been patched")
		return nil
	}
	p.logger.Info("CoreDNS config has been patched successfully")

	return p.restartPods(ctx, deployment)
}

func (p *DNSPatcher) patch(ctx context.Context, deployment *appsv1.Deployment, clusterIP string) (bool, error) {
	cm, err := p.getConfigMap(ctx, deployment, "coredns")
	if err != nil {
		return false, err
	}

	corefile, changed := addStubDomain(cm.Data["Corefile"], clusterIP)
	if !changed {
		return false, nil
	}

	cm.Data["Corefile"] = corefile
	if err := p.client.Update(ctx, cm); err != nil {
		return false, err
	}

	return true, nil
}

func (p *DNSPatcher) restartPods(ctx context.Context, deployment *appsv1.Deployment) error {
	p.logger.Info("Restarting pods", "deployment", deployment.Name)

	annotations := deployment.Spec.Template.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations["caddy-mesh-hash"] = uuid.New().String()
	deployment.Spec.Template.Annotations = annotations

	return p.client.Update(ctx, deployment)
}

// getConfigMap parses the deployment and returns the ConfigMap with the given name.
func (p *DNSPatcher) getConfigMap(ctx context.Context, deployment *appsv1.Deployment, name string) (*corev1.ConfigMap, error) {
	volumeSrc, err := getConfigMapVolumeSource(deployment, name)
	if err != nil {
		return nil, err
	}

	cm := &corev1.ConfigMap{}
	if err := p.client.Get(ctx, key(deployment.Namespace, volumeSrc.Name), cm); err != nil {
		return nil, err
	}

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}

	return cm, nil
}

// getConfigMapVolumeSource returns the ConfigMapVolumeSource corresponding to the ConfigMap with the given name.
func getConfigMapVolumeSource(deployment *appsv1.Deployment, name string) (*corev1.ConfigMapVolumeSource, error) {
	for _, volume := range deployment.Spec.Template.Spec.Volumes {
		if volume.ConfigMap != nil && volume.ConfigMap.Name == name {
			return volume.ConfigMap, nil
		}
	}

	return nil, fmt.Errorf("configmap %q cannot be found", name)
}

func addStubDomain(config, clusterIP string) (string, bool) {
	// Get and remove the existing stub domain.
	var existing string
	re := regexp.MustCompile(fmt.Sprintf(`(?s)(%s.*%s)`, caddyMeshDNSBegin, caddyMeshDNSEnd))
	config = re.ReplaceAllStringFunc(config, func(s string) string {
		existing = s
		return ""
	})

	// Any subdomain of ".caddy.mesh" will resolve to the ClusterIP of caddy-mesh-proxy service.
	format := `%[1]s
%[3]s:53 {
    errors
    template IN A %[3]s {
        match .*\.%[4]s
        answer "{{ .Name }} 60 IN A %[5]s"
        fallthrough
    }
    kubernetes cluster.local in-addr.arpa ip6.arpa {
        pods insecure
        fallthrough in-addr.arpa ip6.arpa
    }
    forward . /etc/resolv.conf
    cache 30
    loop
    reload
    loadbalance
}
%[2]s`

	stubDomain := fmt.Sprintf(format,
		caddyMeshDNSBegin,
		caddyMeshDNSEnd,
		CaddyMeshDomain,
		strings.ReplaceAll(CaddyMeshDomain, ".", "\\."),
		clusterIP,
	)

	return config + "\n" + stubDomain + "\n", existing != stubDomain
}

func key(namespace, name string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
}
