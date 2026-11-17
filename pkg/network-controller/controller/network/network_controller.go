// Copyright (c) 2021-2025 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
//
// The original file created by the Operator SDK has been modified to add Carbyne Stack Ephemeral network controller
// logic.
package network

import (
	"context"
	"fmt"

	mpcv1alpha1 "github.com/carbynestack/ephemeral/pkg/network-controller/apis/mpc/v1alpha1"
	configtypes "github.com/carbynestack/ephemeral/pkg/types"
	istioapi "istio.io/api/networking/v1alpha3"
	istiov1 "istio.io/client-go/pkg/apis/networking/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var podLabel = "mpc.podName"

// Add creates a new Network Controller and registers it with the manager.
func Add(mgr ctrl.Manager, config *configtypes.NetworkControllerConfig) error {
	r := &ReconcileNetwork{client: mgr.GetClient(), scheme: mgr.GetScheme(), config: config}
	return r.SetupWithManager(mgr)
}

// SetupWithManager registers the Istio types with the scheme and configures the controller: the Network resource is
// the primary resource, Services and VirtualServices owned by a Network are watched as secondary resources.
func (r *ReconcileNetwork) SetupWithManager(mgr ctrl.Manager) error {
	if err := istiov1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		Named("network-controller").
		For(&mpcv1alpha1.Network{}).
		Owns(&corev1.Service{}).
		Owns(&istiov1.VirtualService{}).
		Complete(r)
}

// ReconcileNetwork reconciles a Network object
type ReconcileNetwork struct {
	client client.Client
	scheme *runtime.Scheme
	config *configtypes.NetworkControllerConfig
}

// Reconcile reads that state of the cluster for a Network object and makes changes based on the state read
// and what is in the Network.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileNetwork) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx).WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Network")

	// Fetch the Network instance
	instance := &mpcv1alpha1.Network{}
	err := r.client.Get(ctx, request.NamespacedName, instance)
	if err != nil {
		if apierrs.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// TODO: this is a pretty hacky solution, if the pod is reconciled after an update, the label will be removed.
	// Label the pod would like to assign a service to.

	podName := instance.Labels[podLabel]
	pod := &corev1.Pod{}
	err = r.client.Get(ctx, k8stypes.NamespacedName{Name: podName, Namespace: instance.Namespace}, pod)
	if err != nil {
		reqLogger.Error(err, "not able to retrieve the Pod for the service")
		return ctrl.Result{}, err
	}

	if _, ok := pod.Labels[podLabel]; !ok {
		pod.Labels[podLabel] = podName
		err = r.client.Update(ctx, pod)
		if err != nil {
			reqLogger.Error(err, "not able to save the updated Pod definition")
			return ctrl.Result{}, err
		}
	}

	service := newServiceForKnativePod(instance)

	// Set Network instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, service, r.scheme); err != nil {
		return ctrl.Result{}, err
	}

	// Check if the Service already exists
	found := &corev1.Service{}
	err = r.client.Get(ctx, k8stypes.NamespacedName{Name: service.Name, Namespace: service.Namespace}, found)
	if err != nil && apierrs.IsNotFound(err) {
		reqLogger.Info("Creating a new Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
		err = r.client.Create(ctx, service)
		if err != nil {
			reqLogger.Error(err, "not able to create a gateway")
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	reqLogger.Info(fmt.Sprintf("Reconcile with the config:\n%+v", r.config))

	// Check if the gateway already exist, create it otherwise.

	gw := newGateway(instance, instance.Spec.Port, r.config)

	if err := controllerutil.SetControllerReference(instance, gw, r.scheme); err != nil {
		return ctrl.Result{}, err
	}

	gwName := gatewayName(instance.Name)
	err = r.client.Get(ctx, k8stypes.NamespacedName{Name: gwName, Namespace: request.Namespace}, &istiov1.Gateway{})
	if err != nil && apierrs.IsNotFound(err) {
		reqLogger.Info(fmt.Sprintf("Creating a new gateway \"%s\"", gwName))
		if err := r.client.Create(ctx, gw); err != nil {
			reqLogger.Error(err, "not able to create the gateway")
			return ctrl.Result{}, err
		}
	}

	vs := newVirtualService(instance, instance.Spec.Port, gwName)

	if err := controllerutil.SetControllerReference(instance, vs, r.scheme); err != nil {
		return ctrl.Result{}, err
	}

	err = r.client.Get(ctx, k8stypes.NamespacedName{Name: vsName(request.Name), Namespace: request.Namespace}, &istiov1.VirtualService{})
	if apierrs.IsNotFound(err) {
		reqLogger.Info("Creating a new Virtual Service", "VirtualService.Namespace", service.Namespace, "VirtualService.Name", vs.Name)
		if err := r.client.Create(ctx, vs); err != nil {
			reqLogger.Error(err, "can't create a virtual service")
			return ctrl.Result{}, err
		}
	} else if err != nil {
		reqLogger.Error(err, "error retrieving a virtual service")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func serviceName(base string) string {
	return base + "-mpc-service"
}

func vsName(base string) string {
	return base + "-mpc-vs"
}

func gatewayName(base string) string {
	return base + "-mpc-gateway"
}

func newServer(name string, number int) *istioapi.Server {
	return &istioapi.Server{
		Port: &istioapi.Port{
			Number:   uint32(number),
			Protocol: "TCP",
			Name:     name,
		},
		Hosts: []string{"*"},
	}
}

func newServiceForKnativePod(cr *mpcv1alpha1.Network) *corev1.Service {
	// Network name is the same as the pod's 'app' label.
	labels := map[string]string{
		podLabel: cr.Labels[podLabel],
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(cr.Name),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Protocol:   corev1.ProtocolTCP,
					Name:       "tcp",
					Port:       cr.Spec.TargetPort,
					TargetPort: intstr.IntOrString{IntVal: cr.Spec.TargetPort},
				},
			},
		},
	}
	return service
}

func newVirtualService(cr *mpcv1alpha1.Network, port int32, gateway string) *istiov1.VirtualService {
	return &istiov1.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      vsName(cr.Name),
			Namespace: cr.Namespace,
			Labels:    cr.Labels,
		},
		Spec: istioapi.VirtualService{
			Hosts:    []string{"*"},
			Gateways: []string{gateway},
			Tcp: []*istioapi.TCPRoute{
				{
					Match: []*istioapi.L4MatchAttributes{
						{Port: uint32(port)},
					},
					Route: []*istioapi.RouteDestination{
						{
							Destination: &istioapi.Destination{
								Host: serviceName(cr.Name) + "." + cr.Namespace + ".svc.cluster.local",
								Port: &istioapi.PortSelector{
									Number: uint32(cr.Spec.TargetPort),
								},
							},
							Weight: 100,
						},
					},
				},
			},
		}}
}

func newGateway(cr *mpcv1alpha1.Network, port int32, config *configtypes.NetworkControllerConfig) *istiov1.Gateway {
	gwlb := cr.Labels
	gwlb["mpc.gateway"] = "true"
	selectors := map[string]string{}
	selectors["istio"] = "ingressgateway"
	srv := newServer(cr.Name, int(port))
	if config.TlsEnabled {
		// Istio >= 1.2x rejects TLS settings on a TCP server ("server cannot have TLS settings for non
		// HTTPS/TLS ports"); a TLS server terminates (m)TLS and the TCP route of the VirtualService still applies.
		srv.Port.Protocol = "TLS"
		srv.Tls = &istioapi.ServerTLSSettings{
			Mode:           istioapi.ServerTLSSettings_MUTUAL, // Use mutual mode for mTLS
			CredentialName: config.TlsSecret,                  // Name of the Kubernetes secret holding the certificate
		}
	}
	return &istiov1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      gatewayName(cr.Name),
			Namespace: cr.Namespace,
			Labels:    gwlb,
		},
		Spec: istioapi.Gateway{
			Selector: selectors,
			Servers:  []*istioapi.Server{srv},
		},
	}
}
