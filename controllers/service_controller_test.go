package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/ksdns/srvsvc-controller/pkg/dnsrunner"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("service controller", func() {
	Context("service controller test", func() {
		const serviceControllerBaseName = "test-service-controller"
		var (
			typeNamespaceName         = types.NamespacedName{}
			serviceContollerNameSpace string
			namespace                 *corev1.Namespace
			srvSvcReconciler          *ServiceReconciler
		)

		ctx := context.Background()

		BeforeEach(func() {
			By("Creating the Namespace to perform the tests")
			serviceContollerNameSpace = fmt.Sprintf("%s-%d", serviceControllerBaseName, time.Now().UnixMilli())
			typeNamespaceName = types.NamespacedName{Name: serviceControllerBaseName, Namespace: serviceContollerNameSpace}
			namespace = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceContollerNameSpace,
					Namespace: serviceContollerNameSpace,
				},
			}
			err := k8sClient.Create(ctx, namespace)
			Expect(err).To(Not(HaveOccurred()))
			By("Namespace created")
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: serviceContollerNameSpace, Namespace: serviceContollerNameSpace}, namespace)
			}, time.Second*10, time.Second*2).Should(Succeed())
			By("Creating a service reconciler")
			srvSvcReconciler = &ServiceReconciler{
				Client:        k8sClient,
				resolverCache: dnsrunner.NewResolverCache(),
				trigger:       make(chan event.GenericEvent),
				Scheme:        k8sClient.Scheme(),
			}
		})

		AfterEach(func() {
			// TODO(user): Attention if you improve this code by adding other context test you MUST
			// be aware of the current delete namespace limitations. More info: https://book.kubebuilder.io/reference/envtest.html#testing-considerations
			By("Deleting the Namespace to perform the tests")
			_ = k8sClient.Delete(ctx, namespace)
		})
		Describe("Services", func() {
			Describe("Without labels and annotations", func() {
				It("Should not reconcilce a service without labels and annotations", func() {
					By("Creating a new clusterIP service without a selector")
					service := &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      serviceControllerBaseName,
							Namespace: serviceContollerNameSpace,
						},
						Spec: corev1.ServiceSpec{
							Type: corev1.ServiceTypeClusterIP,
							Ports: []corev1.ServicePort{
								{
									Name: "http",
									Port: 80,
								},
							},
						},
					}
					err := k8sClient.Create(ctx, service)
					Expect(err).To(Not(HaveOccurred()))
					By("Waiting for the created service")
					Eventually(func() error {
						return k8sClient.Get(ctx, typeNamespaceName, service)
					}, time.Second*10, time.Second*2).Should(Succeed())

					By("Reconciling the service resource created")
					req := reconcile.Request{
						NamespacedName: typeNamespaceName,
					}
					_, err = srvSvcReconciler.Reconcile(ctx, req)
					Expect(err).To(Not(HaveOccurred()))

					By("Checking if the service was not reconciled")
					Eventually(func() error {
						return k8sClient.Get(ctx, typeNamespaceName, service)
					}, time.Second*10, time.Second*2).Should(Succeed())

					By("Checking that no endpoint slice have been created")
					endpointSlice := &discoveryv1.EndpointSlice{}
					Eventually(func() error {
						return k8sClient.Get(ctx, typeNamespaceName, endpointSlice)
					}, time.Second*6, time.Second*2).ShouldNot(Succeed())
					err = k8sClient.Get(ctx, typeNamespaceName, endpointSlice)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("not found"))
				})
			})
			Describe("With labels and annotations", func() {
				It("Should reconcilce a service with annotations", func() {
					By("Creating a new clusterIP service without a selector")
					service := &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      serviceControllerBaseName,
							Namespace: serviceContollerNameSpace,
							Labels: map[string]string{
								"app":                     "test",
								"srvsvc.ksdns.io/enabled": "true",
							},
							Annotations: map[string]string{
								"srvsvc.ksdns.io/adresses": "dnssrv+_http._tcp.my-svc.my-namespace.blahonga.me",
								"srvsvc.ksdns.io/refresh":  "2s",
							},
						},
						Spec: corev1.ServiceSpec{
							Type: corev1.ServiceTypeClusterIP,
							Ports: []corev1.ServicePort{
								{
									Port:       8080,
									TargetPort: intstr.FromInt(80),
									Protocol:   corev1.ProtocolTCP,
								},
							},
							//ClusterIP: corev1.ClusterIPNone,
						},
					}
					err := k8sClient.Create(ctx, service)
					Expect(err).To(Not(HaveOccurred()))

					By("Waiting for the created service")
					Eventually(func() error {
						return k8sClient.Get(ctx, typeNamespaceName, service)
					}, time.Second*10, time.Second*2).Should(Succeed())

					By("Reconciling the custom resource created")
					_, err = srvSvcReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: typeNamespaceName,
					})
					Expect(err).To(Not(HaveOccurred()))
					// TODO, actually start the controller. For now just sleep 2s and re-run the reconciliation
					By("Waiting for the controller to start (sleeping 2s")
					time.Sleep(time.Second * 4)
					By("Reconciling the service")
					_, err = srvSvcReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: typeNamespaceName,
					})
					Expect(err).To(Not(HaveOccurred()))

					By("Checking that an endpoint slice have been created")
					endpointSlice := &discoveryv1.EndpointSlice{}
					Eventually(func() error {
						return k8sClient.Get(ctx, typeNamespaceName, endpointSlice)
					}, time.Second*10, time.Second*2).Should(Succeed())
				})
			})
		})
	})
})
