.PHONY: build
build:
	@echo ">> building Caddy Mesh controller"
	@go build -v -o caddy-mesh-controller ./cmd/controller

.PHONY: build-image
build-image:
	@docker build -t caddy-mesh-controller:$(tag) .

.PHONY: helm-install
helm-install:
	@helm install caddy-mesh ./helm/caddy-mesh -n caddy-system --create-namespace

.PHONY: helm-upgrade
helm-upgrade:
	@helm upgrade -i caddy-mesh ./helm/caddy-mesh -n caddy-system
