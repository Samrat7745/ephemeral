// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package network

import (
	"context"
	"errors"
	"testing"

	mpcv1alpha1 "github.com/carbynestack/ephemeral/pkg/network-controller/apis/mpc/v1alpha1"
	configtypes "github.com/carbynestack/ephemeral/pkg/types"
	istiov1 "istio.io/client-go/pkg/apis/networking/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type fixture struct {
	t      *testing.T
	c      client.Client
	r      *ReconcileNetwork
	scheme *runtime.Scheme
	req    ctrl.Request
}

func newScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, mpcv1alpha1.AddToScheme, istiov1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func newFixture(t *testing.T, tls bool, spec mpcv1alpha1.NetworkSpec, funcs *interceptor.Funcs, extra ...client.Object) *fixture {
	scheme := newScheme(t)
	nw := &mpcv1alpha1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default", UID: "uid-1", Labels: map[string]string{podLabel: "pod-1"}},
		Spec:       spec,
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"}}
	b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nw, pod).WithObjects(extra...)
	if funcs != nil {
		b = b.WithInterceptorFuncs(*funcs)
	}
	c := b.Build()
	return &fixture{
		t: t, c: c, scheme: scheme,
		r:   &ReconcileNetwork{client: c, scheme: scheme, config: &configtypes.NetworkControllerConfig{TlsEnabled: tls, TlsSecret: "sec"}},
		req: ctrl.Request{NamespacedName: k8stypes.NamespacedName{Name: "pod-1", Namespace: "default"}},
	}
}

var validSpec = mpcv1alpha1.NetworkSpec{Port: 31000, TargetPort: 5000}

func (f *fixture) reconcile() (ctrl.Result, error) {
	return f.r.Reconcile(context.Background(), f.req)
}

func (f *fixture) count(list client.ObjectList) int {
	if err := f.c.List(context.Background(), list, client.InNamespace("default")); err != nil {
		f.t.Fatal(err)
	}
	return meta.LenList(list)
}

func TestReconcileTwiceCreatesNoDuplicates(t *testing.T) {
	f := newFixture(t, false, validSpec, nil)
	for i := 0; i < 3; i++ {
		if _, err := f.reconcile(); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count(&corev1.ServiceList{}); n != 1 {
		t.Errorf("services: %d", n)
	}
	if n := f.count(&istiov1.GatewayList{}); n != 1 {
		t.Errorf("gateways: %d", n)
	}
	if n := f.count(&istiov1.VirtualServiceList{}); n != 1 {
		t.Errorf("virtual services: %d", n)
	}
}

func TestOwnedResourcesReferenceTheNetwork(t *testing.T) {
	// envtest has no garbage collector: assert the owner references that make Kubernetes remove the resources.
	f := newFixture(t, false, validSpec, nil)
	if _, err := f.reconcile(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	svc, gw, vs := &corev1.Service{}, &istiov1.Gateway{}, &istiov1.VirtualService{}
	nn := func(name string) k8stypes.NamespacedName {
		return k8stypes.NamespacedName{Name: name, Namespace: "default"}
	}
	for obj, name := range map[client.Object]string{svc: "pod-1-mpc-service", gw: "pod-1-mpc-gateway", vs: "pod-1-mpc-vs"} {
		if err := f.c.Get(ctx, nn(name), obj); err != nil {
			t.Fatal(err)
		}
		refs := obj.GetOwnerReferences()
		if len(refs) != 1 || refs[0].Kind != "Network" || refs[0].Name != "pod-1" || refs[0].UID != "uid-1" ||
			refs[0].Controller == nil || !*refs[0].Controller || refs[0].BlockOwnerDeletion == nil || !*refs[0].BlockOwnerDeletion {
			t.Errorf("%s: unexpected owner references %+v", name, refs)
		}
	}
}

func TestNetworkLabelsAreNotModified(t *testing.T) {
	f := newFixture(t, false, validSpec, nil)
	if _, err := f.reconcile(); err != nil {
		t.Fatal(err)
	}
	nw := &mpcv1alpha1.Network{}
	f.c.Get(context.Background(), f.req.NamespacedName, nw)
	if _, ok := nw.Labels["mpc.gateway"]; ok {
		t.Error("the gateway label must not leak into the Network")
	}
	vs := &istiov1.VirtualService{}
	f.c.Get(context.Background(), k8stypes.NamespacedName{Name: "pod-1-mpc-vs", Namespace: "default"}, vs)
	if _, ok := vs.Labels["mpc.gateway"]; ok {
		t.Error("the gateway label must not leak into the VirtualService")
	}
}

func TestReconcileDeletedNetworkIsNoop(t *testing.T) {
	f := newFixture(t, false, validSpec, nil)
	if _, err := f.reconcile(); err != nil {
		t.Fatal(err)
	}
	nw := &mpcv1alpha1.Network{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"}}
	if err := f.c.Delete(context.Background(), nw); err != nil {
		t.Fatal(err)
	}
	res, err := f.reconcile()
	if err != nil || res.Requeue || res.RequeueAfter != 0 {
		t.Fatalf("a deleted Network must not be requeued: %v %v", res, err)
	}
	// The owned resources are left to the garbage collector (owner references, see above).
	if n := f.count(&corev1.ServiceList{}); n != 1 {
		t.Errorf("the controller itself must not delete services: %d", n)
	}
}

func TestReconcileTLS(t *testing.T) {
	for _, tls := range []bool{true, false} {
		f := newFixture(t, tls, validSpec, nil)
		if _, err := f.reconcile(); err != nil {
			t.Fatal(err)
		}
		gw := &istiov1.Gateway{}
		f.c.Get(context.Background(), k8stypes.NamespacedName{Name: "pod-1-mpc-gateway", Namespace: "default"}, gw)
		srv := gw.Spec.Servers[0]
		if tls && (srv.Tls == nil || srv.Tls.CredentialName != "sec" || srv.Tls.Mode.String() != "MUTUAL") {
			t.Errorf("tls=true: %+v", srv.Tls)
		}
		if !tls && srv.Tls != nil {
			t.Errorf("tls=false: unexpected TLS settings %+v", srv.Tls)
		}
		if gw.Spec.Selector["istio"] != "ingressgateway" || gw.Labels["mpc.gateway"] != "true" || srv.Hosts[0] != "*" {
			t.Errorf("unexpected gateway servers=%v selector=%v labels=%v", gw.Spec.Servers, gw.Spec.Selector, gw.Labels)
		}
	}
}

func TestReconcileInvalidPortsIsTerminalAndCreatesNothing(t *testing.T) {
	for _, spec := range []mpcv1alpha1.NetworkSpec{
		{Port: 0, TargetPort: 5000}, {Port: 31000, TargetPort: 0}, {Port: -1, TargetPort: 5000},
		{Port: 70000, TargetPort: 5000}, {Port: 31000, TargetPort: 65536}, {},
	} {
		f := newFixture(t, false, spec, nil)
		_, err := f.reconcile()
		if err == nil || !errors.Is(err, reconcile.TerminalError(nil)) {
			t.Errorf("spec %+v: expected a terminal error, got %v", spec, err)
		}
		if f.count(&corev1.ServiceList{})+f.count(&istiov1.GatewayList{})+f.count(&istiov1.VirtualServiceList{}) != 0 {
			t.Errorf("spec %+v: resources were created", spec)
		}
	}
}

func TestReconcileUpdatesServiceWithDifferentSpec(t *testing.T) {
	stale := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1-mpc-service", Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{podLabel: "other-pod"},
			Ports:    []corev1.ServicePort{{Name: "tcp", Protocol: corev1.ProtocolTCP, Port: 1234, TargetPort: intstr.FromInt32(1234)}},
		},
	}
	f := newFixture(t, false, validSpec, nil, stale)
	if _, err := f.reconcile(); err != nil {
		t.Fatal(err)
	}
	svc := &corev1.Service{}
	f.c.Get(context.Background(), k8stypes.NamespacedName{Name: "pod-1-mpc-service", Namespace: "default"}, svc)
	if svc.Spec.Ports[0].Port != 5000 || svc.Spec.Ports[0].TargetPort.IntVal != 5000 || svc.Spec.Selector[podLabel] != "pod-1" {
		t.Errorf("service was not converged: %+v", svc.Spec)
	}
	// and it is stable afterwards
	if _, err := f.reconcile(); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileMissingIstioCRDsRequeues(t *testing.T) {
	noMatch := &meta.NoKindMatchError{GroupKind: istiov1.SchemeGroupVersion.WithKind("Gateway").GroupKind()}
	f := newFixture(t, false, validSpec, &interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			switch obj.(type) {
			case *istiov1.Gateway, *istiov1.VirtualService:
				return noMatch
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})
	res, err := f.reconcile()
	if err == nil || !meta.IsNoMatchError(err) {
		t.Fatalf("expected the no-match error to be returned (controller-runtime requeues with backoff), got %v %v", res, err)
	}
	// the Service was created before, the retry is idempotent
	if n := f.count(&corev1.ServiceList{}); n != 1 {
		t.Errorf("services: %d", n)
	}
}

func TestReconcileErrorPaths(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]interceptor.Funcs{
		"create service": {Create: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.CreateOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return boom
			}
			return c.Create(ctx, obj, o...)
		}},
		"create gateway": {Create: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.CreateOption) error {
			if _, ok := obj.(*istiov1.Gateway); ok {
				return boom
			}
			return c.Create(ctx, obj, o...)
		}},
		"create virtual service": {Create: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.CreateOption) error {
			if _, ok := obj.(*istiov1.VirtualService); ok {
				return boom
			}
			return c.Create(ctx, obj, o...)
		}},
		"get network": {Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, o ...client.GetOption) error {
			if _, ok := obj.(*mpcv1alpha1.Network); ok {
				return boom
			}
			return c.Get(ctx, key, obj, o...)
		}},
		"get service": {Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, o ...client.GetOption) error {
			if _, ok := obj.(*corev1.Service); ok {
				return boom
			}
			return c.Get(ctx, key, obj, o...)
		}},
		"get virtual service": {Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, o ...client.GetOption) error {
			if _, ok := obj.(*istiov1.VirtualService); ok {
				return boom
			}
			return c.Get(ctx, key, obj, o...)
		}},
		"update pod": {Update: func(ctx context.Context, c client.WithWatch, obj client.Object, o ...client.UpdateOption) error {
			return boom
		}},
	}
	for name, funcs := range cases {
		funcs := funcs
		f := newFixture(t, false, validSpec, &funcs)
		if _, err := f.reconcile(); !errors.Is(err, boom) {
			t.Errorf("%s: expected the error to be propagated, got %v", name, err)
		}
	}
}

func TestReconcileWithoutPodFails(t *testing.T) {
	f := newFixture(t, false, validSpec, nil)
	f.c.Delete(context.Background(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"}})
	if _, err := f.reconcile(); err == nil {
		t.Fatal("a Network without pod must be retried")
	}
}

func TestReconcileKeepsExistingPodLabel(t *testing.T) {
	f := newFixture(t, false, validSpec, nil)
	pod := &corev1.Pod{}
	f.c.Get(context.Background(), f.req.NamespacedName, pod)
	pod.Labels = map[string]string{podLabel: "pod-1", "x": "y"}
	f.c.Update(context.Background(), pod)
	if _, err := f.reconcile(); err != nil {
		t.Fatal(err)
	}
	f.c.Get(context.Background(), f.req.NamespacedName, pod)
	if pod.Labels["x"] != "y" {
		t.Error("labels of the pod must be preserved")
	}
}

func TestAddRegistersWithManagerScheme(t *testing.T) {
	// SetupWithManager registers the Istio types with the scheme of the manager.
	s := runtime.NewScheme()
	if err := istiov1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ObjectKinds(&istiov1.Gateway{}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []int32{1, 65535} {
		if !validPort(p) {
			t.Errorf("%d must be valid", p)
		}
	}
	for _, p := range []int32{0, -5, 65536} {
		if validPort(p) {
			t.Errorf("%d must be invalid", p)
		}
	}
}
