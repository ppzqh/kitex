package mock

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestNewTestDNSServer(t *testing.T) {
	dnsSvrAddr := "127.0.0.1:15053"
	NewTestDNSServer(dnsSvrAddr)

	go func() {
		r := &net.Resolver{
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				return net.DialTimeout("tcp", dnsSvrAddr, time.Second)
			},
			PreferGo: true,
		}
		for {
			res, err := r.LookupHost(context.Background(), "kitex-server.default")
			if err != nil {
				fmt.Println(err)
			} else {
				fmt.Println(res)
			}
			time.Sleep(time.Second)
		}
	}()

	ch := make(chan struct{})
	<-ch
	//svr.Stop()
}
