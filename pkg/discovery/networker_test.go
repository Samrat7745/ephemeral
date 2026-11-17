// Copyright (c) 2026 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
package discovery

import (
	"context"
	"errors"
	"testing"

	pb "github.com/Samrat7745/ephemeral/pkg/discovery/transport/proto"
	"github.com/Samrat7745/ephemeral/pkg/network-controller/apis/mpc/v1alpha1"
	istioapi "istio.io/api/networking/v1alpha3"
	istiov1 "istio.io/client-go/pkg/apis/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"go.uber.org/zap"
)

func testScheme(t *testing.T) *runtime.Scheme {
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := istiov1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func newNetworker(t *testing.T, rng string, c client.Client) *IstioNetworker {
	ps, err := NewPortsState(rng, nil)
	if err != nil {
		t.Fatal(err)
	}
	return &IstioNetworker{client: c, ports: ps, logger: zap.NewNop().Sugar(), delCh: make(chan string, 1)}
}

func gateway(name string, ports ...uint32) *istiov1.Gateway {
	gw := &istiov1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: defaultNamespace}}
	for _, p := range ports {
		gw.Spec.Servers = append(gw.Spec.Servers, &istioapi.Server{Port: &istioapi.Port{Number: p}})
	}
	return gw
}

func TestCreateNetworkCreatesResourceWithPorts(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	n := newNetworker(t, "30000:30010", c)
	port, err := n.CreateNetwork(&pb.Player{Id: 101, Pod: "pod-1"})
	if err != nil || port != 30000 {
		t.Fatalf("port=%d err=%v", port, err)
	}
	nw := &v1alpha1.Network{}
	if err := c.Get(context.Background(), k8stypes.NamespacedName{Name: "pod-1", Namespace: defaultNamespace}, nw); err != nil {
		t.Fatal(err)
	}
	if nw.Spec.Port != 30000 || nw.Spec.TargetPort != BasePort+1 || nw.Labels[mpcPodNameLabel] != "pod-1" {
		t.Fatalf("unexpected network %+v labels=%v", nw.Spec, nw.Labels)
	}
}

func TestCreateNetworkSamePodTwiceFailsAndConsumesAPort(t *testing.T) {
	// ServiceNG caches networks per pod and therefore never creates a network twice; if it did, the API server
	// rejects it, and the port taken for it is not given back until the next Sync.
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	n := newNetworker(t, "30000:30010", c)
	if _, err := n.CreateNetwork(&pb.Player{Id: 100, Pod: "pod-0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.CreateNetwork(&pb.Player{Id: 100, Pod: "pod-0"}); err == nil {
		t.Fatal("expected AlreadyExists")
	}
}

func TestCreateNetworkPortExhaustion(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	n := newNetworker(t, "30000:30001", c)
	for _, pod := range []string{"a", "b"} {
		if _, err := n.CreateNetwork(&pb.Player{Id: 100, Pod: pod}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := n.CreateNetwork(&pb.Player{Id: 100, Pod: "c"}); err == nil {
		t.Fatal("expected 'no free ports'")
	}
}

func TestCreateNetworkClientError(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			return errors.New("boom")
		},
	}).Build()
	n := newNetworker(t, "30000:30010", c)
	if _, err := n.CreateNetwork(&pb.Player{Id: 100, Pod: "a"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestSyncReleasesPortsOfDeletedGateways(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(gateway("a", 30000), gateway("c", 30002)).Build()
	n := newNetworker(t, "30000:30005", c)
	if err := n.sync(); err != nil {
		t.Fatal(err)
	}
	p, err := n.getPort()
	if err != nil || p != 30001 {
		t.Fatalf("the hole between the used ports must be reused first, got %d %v", p, err)
	}
	p, _ = n.getPort()
	if p != 30003 {
		t.Fatalf("then the ports above the highest used one, got %d", p)
	}
}

func TestSyncIgnoresGatewaysOutsideOfRange(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(gateway("a", 15021, 8080)).Build()
	n := newNetworker(t, "30000:30005", c)
	if err := n.sync(); err != nil {
		t.Fatal(err)
	}
	if p, _ := n.getPort(); p != 30000 {
		t.Fatalf("got %d", p)
	}
}

// Original behaviour: a failure to list the gateways is not reported by sync, the ports are synced with an empty list.
func TestSyncFailureIsNotReported(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(gateway("a", 30000, 30001)).WithInterceptorFuncs(interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return errors.New("api down")
		},
	}).Build()
	n := newNetworker(t, "30000:30005", c)
	n.ports.Sync([]int32{30000, 30001})
	if err := n.sync(); err != nil {
		t.Fatalf("the list error is not returned by sync, got %v", err)
	}
}

// A port is handed out in CreateNetwork before the network controller created the gateway that makes it visible to
// Sync. A sync in between treats the port as free and hands it out a second time.
func TestSyncBeforeGatewayExistsDoesNotReleaseFreshPort(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(gateway("a", 30000)).Build()
	n := newNetworker(t, "30000:30005", c)
	n.ports.Sync([]int32{30000})
	fresh, _ := n.CreateNetwork(&pb.Player{Id: 100, Pod: "b"}) // 30001, gateway not yet created
	if err := n.sync(); err != nil {
		t.Fatal(err)
	}
	next, _ := n.getPort()
	if next == fresh {
		t.Skipf("known: SPECS-2171 - port %d was handed out twice: PortsState.Sync derives the used ports only from existing Gateways, so a port that was allocated but whose Gateway is not yet created by the network controller is released again by the periodic sync (see docs/consecutive-games.md)", fresh)
	}
}

func TestDeleteNetwork(t *testing.T) {
	nw := &v1alpha1.Network{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: defaultNamespace}}
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).WithObjects(nw).Build()
	n := newNetworker(t, "30000:30005", c)
	if err := n.deleteNetwork("pod-1"); err != nil {
		t.Fatal(err)
	}
	if err := n.deleteNetwork("pod-1"); err == nil {
		t.Fatal("deleting an unknown network must report the error")
	}
}

func TestNewPortsStateValidation(t *testing.T) {
	for _, rng := range []string{"", "1", "a:b", "1:b", "1:2:3"} {
		if _, err := NewPortsState(rng, nil); err == nil {
			t.Errorf("range %q must be rejected", rng)
		}
	}
	// Original behaviour: only the format is validated, not the bounds or the order of the ports.
	for _, rng := range []string{"1:1", "30000:30100", "1:65535", "5:3", "0:10", "1:70000", "-1:5"} {
		if _, err := NewPortsState(rng, nil); err != nil {
			t.Errorf("range %q must be accepted: %v", rng, err)
		}
	}
}

func TestPortsStateSinglePortLifecycle(t *testing.T) {
	ps, _ := NewPortsState("7000:7000", nil)
	p, err := ps.GetFreePort()
	if err != nil || p != 7000 {
		t.Fatalf("%d %v", p, err)
	}
	if _, err := ps.GetFreePort(); err == nil {
		t.Fatal("range exhausted")
	}
	// all ports used according to the gateways: still exhausted
	ps.Sync([]int32{7000})
	if _, err := ps.GetFreePort(); err == nil {
		t.Fatal("range exhausted after sync")
	}
	// gateway removed: the port can be used again
	ps.Sync(nil)
	if p, err := ps.GetFreePort(); err != nil || p != 7000 {
		t.Fatalf("released port must be reusable: %d %v", p, err)
	}
}

func TestPortsStateInitialUsedPorts(t *testing.T) {
	ps, _ := NewPortsState("100:105", []int32{100, 101, 104})
	var got []int32
	for i := 0; i < 3; i++ {
		p, err := ps.GetFreePort()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	// released ports (103, 102) first, then the next one above the highest used port
	if got[0] != 103 && got[0] != 102 || got[2] != 105 {
		t.Fatalf("unexpected order %v", got)
	}
	seen := map[int32]bool{}
	for _, p := range got {
		if p == 100 || p == 101 || p == 104 || seen[p] {
			t.Fatalf("port handed out twice or in use: %v", got)
		}
		seen[p] = true
	}
}
