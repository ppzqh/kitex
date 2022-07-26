package manager

type ClusterIPResolver interface {
	lookupHost(host string) ([]string, error)
	updateLookupTable(map[string][]string)
}

//type dnsProxyResolver struct {
//	resolver *net.Resolver
//	mu       sync.Mutex
//}
//
//func newClusterIPResolver() *dnsProxyResolver {
//	r := &net.Resolver{
//		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
//			return net.DialTimeout("tcp", defaultDNSProxyAddress, defaultDNSTimeout)
//		},
//	}
//	return &dnsProxyResolver{
//		resolver: r,
//		mu:       sync.Mutex{},
//	}
//}
//
//func (r *dnsProxyResolver) lookupHost(host string) ([]string, error) {
//	r.mu.Lock()
//	defer r.mu.Unlock()
//	return r.resolver.LookupHost(context.Background(), host)
//}
