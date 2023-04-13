package dnsrunner

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-kit/log"
	"github.com/patrickmn/go-cache"
	"github.com/thanos-io/thanos/pkg/discovery/dns"
	"sigs.k8s.io/controller-runtime/pkg/event"
	clog "sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var (
	logger = clog.Log.WithName("dnsrunner")
)

type ResolverCache struct {
	lock    sync.RWMutex
	Runners map[string]*Runner
	Cache   *cache.Cache
}

func NewResolverCache() *ResolverCache {
	return &ResolverCache{
		Runners: make(map[string]*Runner),
		Cache:   cache.New(cache.NoExpiration, cache.NoExpiration),
	}
}

func (r *ResolverCache) StartRunner(ctx context.Context, namespacedName types.NamespacedName, addr []string, refreshTime int, trigger chan event.GenericEvent) error {
	runner := NewRunner(refreshTime, namespacedName, addr)
	if err := runner.Run(ctx, r.Cache, trigger); err != nil {
		return err
	}
	r.addRunner(namespacedName, runner)
	return nil
}

func (r *ResolverCache) addRunner(namespacedName types.NamespacedName, runner *Runner) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.Runners[namespacedName.String()] = runner
}

// CheckRunner, checks if we need to restart the runner
func (r *ResolverCache) CheckRunner(namespacedName types.NamespacedName, addr []string) bool {
	runner, found := r.GetRunner(namespacedName)
	if !found {
		return false
	}
	runnerAddresses := runner.Addr
	if len(runnerAddresses) != len(addr) {
		return false
	}
	for i := range runnerAddresses {
		if runnerAddresses[i] != addr[i] {
			return false
		}
	}
	return true
}

func (r *ResolverCache) RemoveRunner(namespacedName types.NamespacedName) {
	r.lock.Lock()
	defer r.lock.Unlock()
	clog.Log.Info(fmt.Sprintf("Removing runner for %s", namespacedName.String()))
	r.Runners[namespacedName.String()].Stop()
	delete(r.Runners, namespacedName.String())
}

func (s *ResolverCache) GetRunner(namespacedName types.NamespacedName) (*Runner, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	runner, found := s.Runners[namespacedName.String()]
	return runner, found
}

func (r *ResolverCache) Get(namespacedName types.NamespacedName) (*Srv, bool) {
	svc, found := r.Cache.Get(namespacedName.String())
	if !found {
		return nil, false
	}
	return svc.(*Srv), true
}

type Runner struct {
	stop chan bool
	// Ticker
	Ticker         *time.Ticker
	namespacedName types.NamespacedName
	Addr           []string
}

func (r *Runner) Stop() {
	r.Ticker.Stop()
	r.stop <- true
}

func NewRunner(refreshTime int, namespacedName types.NamespacedName, addr []string) *Runner {
	return &Runner{
		stop:           make(chan bool),
		Ticker:         time.NewTicker(time.Duration(refreshTime) * time.Second),
		namespacedName: namespacedName,
		Addr:           addr,
	}
}

// Restart the runner
func (r *Runner) Restart(ctx context.Context, c *cache.Cache, addrs []string, trigger chan event.GenericEvent) error {
	r.Stop()
	r.Addr = addrs
	if err := r.Run(ctx, c, trigger); err != nil {
		return err
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, c *cache.Cache, trigger chan event.GenericEvent) error {
	var (
		s *Srv
	)
	dnsSrvResolver := dns.NewProvider(log.NewLogfmtLogger(log.NewSyncWriter(os.Stderr)), nil, dns.MiekgdnsResolverType)
	// Do an initial resolve
	err := dnsSrvResolver.Resolve(ctx, r.Addr)
	if err != nil {
		logger.Error(err, "failed to resolve dns")
		return err
	}
	if dnsSrvResolver.Addresses() == nil {
		logger.Error(err, "failed to resolve", "addresses", r.Addr)
		return fmt.Errorf("failed to resolve %s", r.Addr)
	}
	s = &Srv{
		Adresses: dnsSrvResolver.Addresses(),
	}
	c.Set(r.namespacedName.String(), s, cache.NoExpiration)

	// Start the ticker
	go func() {
		for {
			select {
			case <-r.Ticker.C:
				err := dnsSrvResolver.Resolve(ctx, r.Addr)
				if err != nil {
					logger.Error(err, "failed to resolve dns")
					continue
				}
				s = &Srv{
					Adresses: dnsSrvResolver.Addresses(),
				}
				if srv, found := c.Get(r.namespacedName.String()); found {
					if !srv.(*Srv).IsEqual(s) {
						logger.Info("Srv changed", "key", r.namespacedName)
						c.Set(r.namespacedName.String(), s, cache.NoExpiration)
						trigger <- event.GenericEvent{Object: &corev1.Service{
							ObjectMeta: metav1.ObjectMeta{
								Name:      r.namespacedName.Name,
								Namespace: r.namespacedName.Namespace,
								Labels: map[string]string{
									"srvsvc.ksdns.io/enabled": "true",
								},
							},
						}}
					}
				} else {
					logger.Info("Srv added", "key", r.namespacedName)
					c.Set(r.namespacedName.String(), s, cache.NoExpiration)
					trigger <- event.GenericEvent{Object: &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      r.namespacedName.Name,
							Namespace: r.namespacedName.Namespace,
							Labels: map[string]string{
								"srvsvc.ksdns.io/enabled": "true",
							},
						},
					}}
				}

			case <-r.stop:
				logger.V(1).Info("stopping runner", "namespacedName", r.namespacedName)
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return nil
}

type Srv struct {
	Adresses []string
}

func (s *Srv) IsEqual(srv *Srv) bool {
	if len(s.Adresses) != len(srv.Adresses) {
		return false
	}
	for i := range s.Adresses {
		if s.Adresses[i] != srv.Adresses[i] {
			return false
		}
	}
	return true
}
