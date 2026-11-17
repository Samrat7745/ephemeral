// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0

// Package charts contains render tests for the Helm chart. They need the `helm` binary and are skipped without it.
package charts

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carbynestack/ephemeral/pkg/network-controller/apis/mpc/v1alpha1"
	"github.com/carbynestack/ephemeral/pkg/types"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

const chartDir = "ephemeral"

// required values that have no default in values.yaml but end up in JSON config files.
var required = []string{
	"ephemeral.playerId=1",
	"ephemeral.spdz.gf2nBitLength=40",
	"ephemeral.spdz.gf2nStorageSize=16",
	"ephemeral.spdz.prime=198766463529478683931867765928436695041",
	"ephemeral.spdz.rInv=133854242216446749056083838363708373830",
	"ephemeral.spdz.gfpMacKey=1113507028231509545",
	"ephemeral.spdz.gf2nMacKey=0xf0",
	"ephemeral.frontendUrl=10.0.0.1",
	"discovery.frontendUrl=10.0.0.1",
}

func helm(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed")
	}
	return p
}

// render runs `helm template` and returns the documents keyed by "Kind/name".
func render(t *testing.T, sets ...string) (map[string]map[string]interface{}, []byte, error) {
	t.Helper()
	args := []string{"template", "rel", chartDir, "--namespace", "ns"}
	for _, s := range append(append([]string{}, required...), sets...) {
		args = append(args, "--set", s)
	}
	var out, errOut bytes.Buffer
	cmd := exec.Command(helm(t), args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, nil, &renderError{errOut.String()}
	}
	docs := map[string]map[string]interface{}{}
	for _, part := range strings.Split(out.String(), "\n---\n") {
		var doc map[string]interface{}
		if err := yaml.Unmarshal([]byte(part), &doc); err != nil {
			t.Fatalf("rendered output is not valid YAML: %v\n%s", err, part)
		}
		if doc == nil {
			continue
		}
		name, _ := get(doc, "metadata", "name").(string)
		docs[doc["kind"].(string)+"/"+name] = doc
	}
	return docs, out.Bytes(), nil
}

type renderError struct{ msg string }

func (e *renderError) Error() string { return e.msg }

func mustRender(t *testing.T, sets ...string) map[string]map[string]interface{} {
	t.Helper()
	docs, _, err := render(t, sets...)
	if err != nil {
		t.Fatalf("helm template failed: %v", err)
	}
	return docs
}

func get(m map[string]interface{}, path ...string) interface{} {
	var cur interface{} = m
	for _, p := range path {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func convert(t *testing.T, doc map[string]interface{}, into interface{}) {
	t.Helper()
	b, _ := json.Marshal(doc)
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}

func knative(t *testing.T, docs map[string]map[string]interface{}) map[string]interface{} {
	t.Helper()
	for k, d := range docs {
		if strings.HasPrefix(k, "Service/") && d["apiVersion"] == "serving.knative.dev/v1" {
			return d
		}
	}
	t.Fatal("Knative service not rendered")
	return nil
}

func container(t *testing.T, kn map[string]interface{}) map[string]interface{} {
	cs := get(kn, "spec", "template", "spec", "containers").([]interface{})
	return cs[0].(map[string]interface{})
}

func configOf(t *testing.T, docs map[string]map[string]interface{}, name string, into interface{}, strict bool) {
	t.Helper()
	var cm corev1.ConfigMap
	convert(t, docs["ConfigMap/"+name], &cm)
	dec := json.NewDecoder(strings.NewReader(cm.Data["config.json"]))
	if strict {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(into); err != nil {
		t.Fatalf("config of %s does not match the Go configuration type: %v\n%s", name, err, cm.Data["config.json"])
	}
}

func TestDefaultsRenderValidManifests(t *testing.T) {
	docs := mustRender(t)
	for _, want := range []string{
		"Deployment/ephemeral-rel-discovery", "Service/ephemeral-rel-discovery", "ConfigMap/discovery-config",
		"ConfigMap/ephemeral-rel-config1", "Service/ephemeral-generic", "CustomResourceDefinition/networks.mpc.bosch.com",
	} {
		if docs[want] == nil {
			t.Errorf("%s missing, have %v", want, keys(docs))
		}
	}
}

func keys(m map[string]map[string]interface{}) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestConfigMapsMatchTheGoConfigTypes(t *testing.T) {
	docs := mustRender(t, "playerCount=3", "ephemeral.player.stateTimeout=11s", "discovery.stateTimeout=12s",
		"ephemeral.discovery.port=9090", "ephemeral.castor.tupleStock=77")
	var eph types.SPDZEngineConfig
	configOf(t, docs, "ephemeral-rel-config1", &eph, true)
	if eph.PlayerID != 1 || eph.PlayerCount != 3 || eph.StateTimeout != "11s" || eph.DiscoveryConfig.Port != "9090" ||
		eph.CastorConfig.TupleStock != 77 || eph.FrontendURL != "10.0.0.1" || eph.TlsEnabled || eph.AuthUserIdField != "sub" ||
		eph.Gf2nBitLength != 40 || eph.Prime != "198766463529478683931867765928436695041" || eph.AmphoraConfig.Host != "amphora" {
		t.Errorf("unexpected ephemeral config %+v", eph)
	}
	var disc types.DiscoveryConfig
	configOf(t, docs, "discovery-config", &disc, true)
	if disc.PlayerCount != 3 || disc.StateTimeout != "12s" || disc.FrontendURL != "10.0.0.1" || disc.Slave {
		t.Errorf("unexpected discovery config %+v", disc)
	}
}

func TestDiscoveryMasterSlave(t *testing.T) {
	var master, slave types.DiscoveryConfig
	configOf(t, mustRender(t, "discovery.isMaster=true"), "discovery-config", &master, true)
	if master.Slave {
		t.Error("master must not be a slave")
	}
	configOf(t, mustRender(t, "discovery.isMaster=false", "discovery.master.host=m.example.org", "discovery.master.port=31000", "discovery.slave.connectTimeout=5s"),
		"discovery-config", &slave, true)
	if !slave.Slave || slave.MasterHost != "m.example.org" || slave.MasterPort != "31000" || slave.ConnectTimeout != "5s" {
		t.Errorf("unexpected slave config %+v", slave)
	}
}

func TestTLS(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		docs := mustRender(t)
		var cfg types.SPDZEngineConfig
		configOf(t, docs, "ephemeral-rel-config1", &cfg, false)
		kn := knative(t, docs)
		// Original chart: the TLS secret volume is always rendered (with an empty secretName when TLS is off);
		// only the engine config switches TLS off.
		vols := get(kn, "spec", "template", "spec", "volumes").([]interface{})
		if cfg.TlsEnabled || len(vols) != 2 {
			t.Errorf("tls=%v volumes=%v", cfg.TlsEnabled, vols)
		}
	})
	t.Run("enabled with secret", func(t *testing.T) {
		docs := mustRender(t, "tls.enabled=true", "tls.secret=my-secret")
		var cfg types.SPDZEngineConfig
		configOf(t, docs, "ephemeral-rel-config1", &cfg, false)
		kn := knative(t, docs)
		var found bool
		for _, v := range get(kn, "spec", "template", "spec", "volumes").([]interface{}) {
			if s := get(v.(map[string]interface{}), "secret", "secretName"); s == "my-secret" {
				found = true
			}
		}
		var mounted bool
		for _, m := range container(t, kn)["volumeMounts"].([]interface{}) {
			mm := m.(map[string]interface{})
			if mm["mountPath"] == "/etc/tls" && mm["readOnly"] == true {
				mounted = true
			}
		}
		if !cfg.TlsEnabled || !found || !mounted {
			t.Errorf("tls=%v secretVolume=%v mounted=%v", cfg.TlsEnabled, found, mounted)
		}
	})
	t.Run("enabled without secret is not validated", func(t *testing.T) {
		// Original behaviour: the chart does not require tls.secret, the secret volume is rendered without a name.
		if _, _, err := render(t, "tls.enabled=true"); err != nil {
			t.Fatalf("expected the chart to render, got %v", err)
		}
	})
	t.Run("secret without enabled is still mounted", func(t *testing.T) {
		// Original chart: the secret volume does not depend on tls.enabled; only the engine config does.
		docs := mustRender(t, "tls.secret=unused")
		if !strings.Contains(string(mustMarshal(t, knative(t, docs))), "unused") {
			t.Error("original chart renders the tls secret volume regardless of tls.enabled")
		}
	})
}

func mustMarshal(t *testing.T, v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Original behaviour: the chart does not set a security context for the ephemeral container.
func TestNoSecurityContext(t *testing.T) {
	if sc, ok := container(t, knative(t, mustRender(t)))["securityContext"]; ok {
		t.Errorf("unexpected security context %v", sc)
	}
}

func jsonNum(v interface{}) string { b, _ := json.Marshal(v); return string(b) }

// Original behaviour of the annotation templates: the discovery service annotations are guarded by the ephemeral
// service annotations, and ephemeral service annotations are indented wrongly, so setting them breaks the rendering.
func TestServiceAnnotations(t *testing.T) {
	// discovery annotations alone are not rendered
	docs := mustRender(t,
		"discovery.service.annotations.networking\\.istio\\.io/exportTo=*",
		"discovery.service.annotations.foo=bar",
		"ephemeral.minScale=2")
	var svc corev1.Service
	convert(t, docs["Service/ephemeral-rel-discovery"], &svc)
	if len(svc.Annotations) != 0 {
		t.Errorf("discovery annotations %v", svc.Annotations)
	}
	ann := get(knative(t, docs), "spec", "template", "metadata", "annotations").(map[string]interface{})
	if len(ann) != 1 || ann["autoscaling.knative.dev/minScale"] != "2" {
		t.Errorf("ephemeral annotations %v", ann)
	}
	// ephemeral annotations make the rendered YAML invalid
	if _, _, err := render(t, "ephemeral.service.annotations.client\\.example/a=b"); err == nil {
		t.Error("expected the rendering to fail")
	}
	// defaults: no annotations on the discovery service, only the scale annotation on the Knative service
	docs = mustRender(t)
	svc = corev1.Service{}
	convert(t, docs["Service/ephemeral-rel-discovery"], &svc)
	if len(svc.Annotations) != 0 {
		t.Errorf("unexpected default annotations %v", svc.Annotations)
	}
	ann = get(knative(t, docs), "spec", "template", "metadata", "annotations").(map[string]interface{})
	if len(ann) != 1 || ann["autoscaling.knative.dev/minScale"] != "1" {
		t.Errorf("unexpected default knative annotations %v", ann)
	}
}

func TestImages(t *testing.T) {
	docs := mustRender(t)
	var dep appsv1.Deployment
	convert(t, docs["Deployment/ephemeral-rel-discovery"], &dep)
	c := dep.Spec.Template.Spec.Containers[0]
	if c.Image != "ghcr.io/carbynestack/ephemeral/discovery:latest" || c.ImagePullPolicy != "IfNotPresent" || len(dep.Spec.Template.Spec.ImagePullSecrets) != 0 {
		t.Errorf("default discovery image %q %q", c.Image, c.ImagePullPolicy)
	}
	if got := container(t, knative(t, docs))["image"]; got != "ghcr.io/carbynestack/ephemeral/ephemeral:latest" {
		t.Errorf("default ephemeral image %v", got)
	}

	docs = mustRender(t,
		"discovery.image.registry=reg.example", "discovery.image.repository=team/disc", "discovery.image.tag=v1", "discovery.image.pullPolicy=Always",
		"discovery.image.pullSecrets[0]=s1", "discovery.image.pullSecrets[1]=s2",
		"ephemeral.image.registry=reg2", "ephemeral.image.repository=e", "ephemeral.image.tag=v2", "ephemeral.image.pullPolicy=Never")
	convert(t, docs["Deployment/ephemeral-rel-discovery"], &dep)
	c = dep.Spec.Template.Spec.Containers[0]
	if c.Image != "reg.example/team/disc:v1" || c.ImagePullPolicy != "Always" {
		t.Errorf("discovery image %q %q", c.Image, c.ImagePullPolicy)
	}
	if ps := dep.Spec.Template.Spec.ImagePullSecrets; len(ps) != 2 || ps[0].Name != "s1" || ps[1].Name != "s2" {
		t.Errorf("pull secrets %v", ps)
	}
	ec := container(t, knative(t, docs))
	if ec["image"] != "reg2/e:v2" || ec["imagePullPolicy"] != "Never" {
		t.Errorf("ephemeral image %v %v", ec["image"], ec["imagePullPolicy"])
	}
	// network controller image
	docs = mustRender(t, "networkController.image.tag=nc1")
	var found bool
	for _, d := range docs {
		if d["kind"] == "Deployment" && strings.Contains(string(mustMarshal(t, d)), "network-controller:nc1") {
			found = true
		}
	}
	if !found {
		t.Error("network controller image tag override not rendered")
	}
}

func TestPlayerIdAndFrontendURL(t *testing.T) {
	docs := mustRender(t, "ephemeral.playerId=0", "ephemeral.frontendUrl=my.host", "discovery.frontendUrl=other.host")
	var cfg types.SPDZEngineConfig
	configOf(t, docs, "ephemeral-rel-config1", &cfg, false)
	var disc types.DiscoveryConfig
	configOf(t, docs, "discovery-config", &disc, false)
	if cfg.PlayerID != 0 || cfg.FrontendURL != "my.host" || disc.FrontendURL != "other.host" {
		t.Errorf("%+v %+v", cfg, disc)
	}
}

func TestResourcesAndPorts(t *testing.T) {
	kn := knative(t, mustRender(t))
	c := container(t, kn)
	var res corev1.ResourceRequirements
	convert(t, map[string]interface{}{"requests": get(c, "resources", "requests"), "limits": get(c, "resources", "limits")}, &res)
	if res.Requests.Memory().String() != "256Mi" || res.Requests.Cpu().String() != "100m" || len(res.Limits) != 0 {
		t.Errorf("default resources %v", c["resources"])
	}
	kn = knative(t, mustRender(t, "ephemeral.resources.limits.memory=1Gi", "ephemeral.resources.limits.cpu=2"))
	convert(t, map[string]interface{}{"requests": get(container(t, kn), "resources", "requests"), "limits": get(container(t, kn), "resources", "limits")}, &res)
	if res.Limits.Memory().String() != "1Gi" || res.Limits.Cpu().String() != "2" {
		t.Errorf("limits %v", container(t, kn)["resources"])
	}
	if get(kn, "spec", "template", "spec", "containerConcurrency") != float64(1) {
		t.Error("containerConcurrency must be 1: a pod serves a single game at a time")
	}
}

func TestInvalidValuesAreRejectedOrRenderBrokenConfig(t *testing.T) {
	// Without the required values the JSON config is not parseable. Documented behaviour: the chart does not validate
	// them, the services fail at startup instead.
	var out bytes.Buffer
	cmd := exec.Command(helm(t), "template", "rel", chartDir)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Skipf("defaults only do not render: %v", err)
	}
	var cm corev1.ConfigMap
	for _, part := range strings.Split(out.String(), "\n---\n") {
		var d map[string]interface{}
		yaml.Unmarshal([]byte(part), &d)
		if get(d, "metadata", "name") == "ephemeral-rel-config1" {
			convert(t, d, &cm)
		}
	}
	var v types.SPDZEngineConfig
	if err := json.Unmarshal([]byte(cm.Data["config.json"]), &v); err == nil {
		t.Error("expected invalid JSON without playerId/gf2n values")
	}
}

// The CRD, the RBAC rules and the Go API types must agree on the API group and resource names.
func TestNetworkCRDMatchesGoTypes(t *testing.T) {
	docs := mustRender(t)
	crd := docs["CustomResourceDefinition/networks."+v1alpha1.SchemeGroupVersion.Group]
	if crd == nil {
		t.Fatalf("CRD for group %s missing: %v", v1alpha1.SchemeGroupVersion.Group, keys(docs))
	}
	if get(crd, "spec", "group") != v1alpha1.SchemeGroupVersion.Group || get(crd, "spec", "names", "kind") != "Network" ||
		get(crd, "spec", "scope") != "Namespaced" {
		t.Errorf("unexpected CRD spec %v", crd["spec"])
	}
	var served bool
	for _, v := range get(crd, "spec", "versions").([]interface{}) {
		if v.(map[string]interface{})["name"] == v1alpha1.SchemeGroupVersion.Version {
			served = true
		}
	}
	if !served {
		t.Errorf("version %s not defined by the CRD", v1alpha1.SchemeGroupVersion.Version)
	}
	// every role that deals with networks names the group
	var roles int
	for k, d := range docs {
		if !strings.HasPrefix(k, "Role/") {
			continue
		}
		if strings.Contains(string(mustMarshal(t, d)), "networks") {
			roles++
			if !strings.Contains(string(mustMarshal(t, d)), v1alpha1.SchemeGroupVersion.Group) {
				t.Errorf("%s: rules for networks do not name the API group %s", k, v1alpha1.SchemeGroupVersion.Group)
			}
		}
	}
	if roles == 0 {
		t.Error("no role grants access to networks")
	}
}

func TestKubeconform(t *testing.T) {
	kc, err := exec.LookPath("kubeconform")
	if err != nil {
		t.Skip("kubeconform not installed")
	}
	for name, sets := range map[string][]string{
		"defaults": nil,
		"tls":      {"tls.enabled=true", "tls.secret=s"},
		"slave":    {"discovery.isMaster=false", "discovery.master.host=h", "discovery.master.port=1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, out, err := render(t, sets...)
			if err != nil {
				t.Fatal(err)
			}
			f := filepath.Join(t.TempDir(), "out.yaml")
			os.WriteFile(f, out, 0600)
			var res bytes.Buffer
			cmd := exec.Command(kc, "-strict", "-ignore-missing-schemas", "-summary", f)
			cmd.Stdout, cmd.Stderr = &res, &res
			if err := cmd.Run(); err != nil {
				if strings.Contains(res.String(), "failed downloading schema") || strings.Contains(res.String(), "could not find schema") {
					t.Skipf("schemas not available: %s", res.String())
				}
				t.Fatalf("kubeconform failed: %v\n%s", err, res.String())
			}
		})
	}
}
