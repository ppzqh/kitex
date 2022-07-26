package mock

import (
	"fmt"
	"net"
	"net/http"
	"sync"
)

type testDNSServer struct {
	server http.Server
}

type dnsHandler struct {
}

func (h *dnsHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	fmt.Println(req.Method)
	fmt.Println(req.Header)
	fmt.Println(req.Body)
	w.Write([]byte("OK"))
}

func NewTestDNSServer(address string) *testDNSServer {
	dnsSvr := &testDNSServer{
		server: http.Server{
			Handler: &dnsHandler{},
			Addr: address,
		},
	}
	go dnsSvr.Start()
	return dnsSvr
}

func (svr *testDNSServer) Start() {
	svr.server.ListenAndServe()
}

func (svr *testDNSServer) Stop() {
	svr.server.Shutdown(nil)
}

type clusterIPResolver struct {
	resolver *net.Resolver
	mu       sync.Mutex
}