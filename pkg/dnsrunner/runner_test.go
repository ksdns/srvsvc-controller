package dnsrunner

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/runtime/inject"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Test NewServices
//func TestNewResolverCache(t *testing.T) {
//	rc := NewResolverCache()
//	if rc == nil {
//		t.Error("ResolverCache should not be nil")
//	}
//}
//
//// Test StartRunner
//func TestStartRunner(t *testing.T) {
//	rc := NewResolverCache()
//	// "dnssrv+_http._tcp.my-svc.my-namespace.blahonga.me"
//	rc.StartRunner(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, []string{"dnssrv+_http._tcp.my-svc.my-namespace.blahonga.me"}, 1, nil)
//	if len(rc.Runners) != 1 {
//		t.Error("services should have 1 runner")
//	}
//	defer rc.RemoveRunner(types.NamespacedName{Name: "test", Namespace: "default"})
//}
//
//// Test RemoveRunner
//func TestRemoveRunner(t *testing.T) {
//	rc := NewResolverCache()
//	rc.StartRunner(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, []string{"dnssrv+_http._tcp.my-svc.my-namespace.blahonga.me"}, 1, nil)
//	rc.RemoveRunner(types.NamespacedName{Name: "test", Namespace: "default"})
//	if len(rc.Runners) != 0 {
//		t.Error("services should have 0 runners")
//	}
//}

// Test Get
var _ = Describe("ResolverCache", func() {
	Describe("NewResolverCache", func() {
		It("Should not be nil", func() {
			rc := NewResolverCache()
			Expect(rc).NotTo(BeNil())
		})
		It("Should have 0 runners", func() {
			rc := NewResolverCache()
			Expect(len(rc.Runners)).To(Equal(0))
		})
		It("Should have a cache", func() {
			rc := NewResolverCache()
			Expect(rc.Cache).NotTo(BeNil())
		})
	})
	Describe("StartRunner", func() {
		It("Should add a runner", func() {
			rc := NewResolverCache()
			err := rc.StartRunner(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, []string{"dnssrv+_http._tcp.my-svc.my-namespace.blahonga.me"}, 1, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(rc.Runners)).To(Equal(1))
			Expect(rc.Runners[types.NamespacedName{Name: "test", Namespace: "default"}.String()]).NotTo(BeNil())
			defer rc.RemoveRunner(types.NamespacedName{Name: "test", Namespace: "default"})
		})
		It("Should error if it cannot resolve", func() {
			rc := NewResolverCache()
			err := rc.StartRunner(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, []string{"dnssrv+foo._tcp.my-svc.my-namespace.blahonga.me"}, 1, nil)
			Expect(err).To(HaveOccurred())
			Expect(len(rc.Runners)).To(Equal(0))
		})
	})
	Describe("RemoveRunner", func() {
		It("Should remove a runner", func() {
			rc := NewResolverCache()
			rc.StartRunner(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, []string{"dnssrv+_http._tcp.my-svc.my-namespace.blahonga.me"}, 1, nil)
			rc.RemoveRunner(types.NamespacedName{Name: "test", Namespace: "default"})
			Expect(len(rc.Runners)).To(Equal(0))
		})
	})
	Describe("Get", func() {
		It("Should add resolved items to cache", func() {
			rc := NewResolverCache()
			trigger := make(chan event.GenericEvent)
			c := make(chan struct{})
			defer close(c)

			// Predicate to filter out empty event
			prct := predicate.Funcs{
				GenericFunc: func(e event.GenericEvent) bool {
					return e.Object != nil
				},
			}
			q := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "test")
			instance := &source.Channel{Source: trigger}
			Expect(inject.StopChannelInto(ctx.Done(), instance)).To(BeTrue())
			err := instance.Start(ctx, handler.Funcs{
				CreateFunc: func(event.CreateEvent, workqueue.RateLimitingInterface) {
					defer GinkgoRecover()
					Fail("Unexpected CreateEvent")
				},
				UpdateFunc: func(event.UpdateEvent, workqueue.RateLimitingInterface) {
					defer GinkgoRecover()
					Fail("Unexpected UpdateEvent")
				},
				DeleteFunc: func(event.DeleteEvent, workqueue.RateLimitingInterface) {
					defer GinkgoRecover()
					Fail("Unexpected DeleteEvent")
				},
				GenericFunc: func(evt event.GenericEvent, q2 workqueue.RateLimitingInterface) {
					defer GinkgoRecover()
					Expect(evt.Object).To(Equal(&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: "default",
							Labels: map[string]string{
								"srvsvc.ksdns.io/enabled": "true",
							},
						},
					}))
				},
			}, q, prct)
			Expect(err).NotTo(HaveOccurred())

			err = rc.StartRunner(context.Background(), types.NamespacedName{Name: "test", Namespace: "default"}, []string{"dnssrv+_http._tcp.my-svc.my-namespace.blahonga.me"}, 1, trigger)
			Expect(err).NotTo(HaveOccurred())
			defer rc.RemoveRunner(types.NamespacedName{Name: "test", Namespace: "default"})
			time.Sleep(3 * time.Second)
			svc, found := rc.Get(types.NamespacedName{Name: "test", Namespace: "default"})
			Expect(found).To(BeTrue())
			Expect(svc).NotTo(BeNil())
		})
	})
})
