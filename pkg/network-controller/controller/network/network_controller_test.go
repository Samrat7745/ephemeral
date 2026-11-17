// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package network

import (
	"context"
	"testing"

	mpcv1alpha1 "github.com/Samrat7745/ephemeral/pkg/network-controller/apis/mpc/v1alpha1"
	configtypes "github.com/Samrat7745/ephemeral/pkg/types"
	istiov1 "istio.io/client-go/pkg/apis/networking/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileCreatesServiceGatewayAndVirtualService(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{clientgoscheme.AddToScheme, mpcv1alpha1.AddToScheme, istiov1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	nw := &mpcv1alpha1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default", UID: "uid-1", Labels: map[string]string{podLabel: "pod-1"}},
		Spec:       mpcv1alpha1.NetworkSpec{Port: 31000, TargetPort: 5000},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default", Labels: map[string]string{"app": "mpc"}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nw, pod).Build()
	r := &ReconcileNetwork{client: c, scheme: scheme, config: &configtypes.NetworkControllerConfig{TlsEnabled: true, TlsSecret: "sec"}}

	req := ctrl.Request{NamespacedName: k8stypes.NamespacedName{Name: "pod-1", Namespace: "default"}}
	for i := 0; i < 2; i++ { // second run must be idempotent
		if _, err := r.Reconcile(context.Background(), req); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}

	ctx := context.Background()
	gotPod := &corev1.Pod{}
	_ = c.Get(ctx, k8stypes.NamespacedName{Name: "pod-1", Namespace: "default"}, gotPod)
	if gotPod.Labels[podLabel] != "pod-1" {
		t.Errorf("pod not labelled: %v", gotPod.Labels)
	}
	svc := &corev1.Service{}
	if err := c.Get(ctx, k8stypes.NamespacedName{Name: "pod-1-mpc-service", Namespace: "default"}, svc); err != nil {
		t.Fatal(err)
	}
	if svc.Spec.Ports[0].Port != 5000 || len(svc.OwnerReferences) != 1 {
		t.Errorf("unexpected service: %+v", svc.Spec)
	}
	gw := &istiov1.Gateway{}
	if err := c.Get(ctx, k8stypes.NamespacedName{Name: "pod-1-mpc-gateway", Namespace: "default"}, gw); err != nil {
		t.Fatal(err)
	}
	srv := gw.Spec.Servers[0]
	if srv.Port.Number != 31000 || srv.Tls == nil || srv.Tls.CredentialName != "sec" {
		t.Errorf("unexpected gateway: %+v", gw.Spec.Servers)
	}
	vs := &istiov1.VirtualService{}
	if err := c.Get(ctx, k8stypes.NamespacedName{Name: "pod-1-mpc-vs", Namespace: "default"}, vs); err != nil {
		t.Fatal(err)
	}
	if vs.Spec.Tcp[0].Match[0].Port != 31000 || vs.Spec.Tcp[0].Route[0].Destination.Port.Number != 5000 {
		t.Errorf("unexpected virtual service: %+v", vs.Spec.Tcp[0])
	}
}
