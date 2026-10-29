CONTROLLER_GEN ?= controller-gen

.PHONY: generate
generate: ## Regenerate deepcopy code, CRD and the chart copy of the CRD (controller-gen v0.23.0)
	$(CONTROLLER_GEN) object paths=./pkg/network-controller/apis/...
	$(CONTROLLER_GEN) crd paths=./pkg/network-controller/apis/... output:crd:dir=config/crd/bases
