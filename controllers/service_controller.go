/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/ksdns/srvsvc-controller/pkg/dnsrunner"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceReconciler reconciles a Service object
type ServiceReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	trigger       chan event.GenericEvent
	resolverCache *dnsrunner.ResolverCache
}

//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=services/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Service object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	service := &corev1.Service{}
	err := r.Client.Get(ctx, req.NamespacedName, service)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("service not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get service")
		return ctrl.Result{}, err
	}
	// if this is a new service, start it
	if runner, ok := r.resolverCache.GetRunner(req.NamespacedName); !ok {
		addresses := strings.Split(service.GetAnnotations()["srvsvc.ksdns.io/adresses"], ",")
		if addresses[0] == "" {
			log.Info("No addresses found in annotation. Ignoring")
			return ctrl.Result{}, nil
		}
		if err := r.resolverCache.StartRunner(ctx, req.NamespacedName, addresses, 2, r.trigger); err != nil {
			log.Error(err, "Failed to start runner")
			return ctrl.Result{}, err
		}
	} else { // if this is an existing service, check if the addresses have changed
		addresses := strings.Split(service.GetAnnotations()["srvsvc.ksdns.io/adresses"], ",")
		if addresses[0] == "" {
			log.Info("No addresses found in annotation. Ignoring")
			return ctrl.Result{}, nil
		}
		if !r.resolverCache.CheckRunner(req.NamespacedName, addresses) {
			if err := runner.Restart(ctx, r.resolverCache.Cache, addresses, r.trigger); err != nil {
				log.Error(err, "Failed to restart runner")
				r.resolverCache.RemoveRunner(req.NamespacedName)
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}
	// get addresses from cache
	if a, ok := r.resolverCache.Get(req.NamespacedName); ok {
		var (
			srvAdresses = []corev1.EndpointAddress{}
			ports       = []corev1.EndpointPort{}
			iPort       int
		)

		for _, addr := range a.Adresses {
			// split host:port
			host, port, err := net.SplitHostPort(addr)
			iPort, _ = strconv.Atoi(port)
			if err != nil {
				log.Error(err, "Failed to split host:port")
				return ctrl.Result{}, err
			}
			srvAdresses = append(srvAdresses, corev1.EndpointAddress{
				IP: host,
			})
		}
		ports = append(ports, corev1.EndpointPort{
			Name:     "tcp",
			Port:     int32(iPort),
			Protocol: corev1.ProtocolTCP,
		})
		ep := &corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:      req.Name,
				Namespace: req.Namespace,
				Labels: map[string]string{
					"srvsvc.ksdns.io/service": req.Name,
				},
			},
			TypeMeta: metav1.TypeMeta{
				Kind:       "Endpoints",
				APIVersion: "v1",
			},
			Subsets: []corev1.EndpointSubset{
				{
					Addresses: srvAdresses,
					Ports:     ports,
				},
			},
		}
		// create or update endpoints
		op, err := CreateOrUpdateWithRetries(ctx, r.Client, ep, func() error {
			return controllerutil.SetControllerReference(service, ep, r.Scheme)
		},
		)
		if err != nil {
			log.Error(err, "Failed to create or update endpoint slice")
			return ctrl.Result{}, err
		}
		log.Info("endpoint slice created or updated", "op", op)
	} else {
		log.Info("No addresses found in cache. Ignoring") // Try again later
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// https://github.com/kubernetes-sigs/hierarchical-namespaces/blob/9102c4ddc2bbc145e279624d76d89f314a5485a6/internal/hrq/rqreconciler.go#L289
	r.trigger = make(chan event.GenericEvent)
	r.resolverCache = dnsrunner.NewResolverCache()
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&corev1.Service{}).
		WithEventFilter(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				if _, ok := e.ObjectNew.GetLabels()["srvsvc.ksdns.io/enabled"]; !ok {
					return false
				}
				// ignore statuses
				if e.ObjectOld.GetGeneration() == e.ObjectNew.GetGeneration() {
					return false
				}
				return true
			},
			CreateFunc: func(e event.CreateEvent) bool {
				if _, ok := e.Object.GetLabels()["srvsvc.ksdns.io/enabled"]; !ok {
					return false
				}
				return true
			},
		}).
		Watches(&source.Channel{Source: r.trigger}, &handler.EnqueueRequestForObject{}).
		Owns(&discoveryv1.EndpointSlice{}).
		Complete(r)
}
