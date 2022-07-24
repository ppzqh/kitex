package xdsresource

import (
	"github.com/cloudwego/kitex/internal/test"
	"github.com/cloudwego/kitex/pkg/xds/internal/testutil"
	v3listenerpb "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	v3routepb "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	v3httppb "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	v3thrift_proxy "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/thrift_proxy/v3"
	v3matcher "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/golang/protobuf/ptypes/any"
	"reflect"
	"testing"
)

func TestUnmarshalLDSError(t *testing.T) {
	tests := []struct {
		name         string
		rawResources []*any.Any
		want         map[string]*ListenerResource
		wantErr      bool
	}{
		{
			name:         "resource is nil",
			rawResources: nil,
			want:         map[string]*ListenerResource{},
			wantErr:      false,
		},
		{
			name: "incorrect resource type url",
			rawResources: []*any.Any{
				{TypeUrl: EndpointTypeUrl, Value: []byte{}},
			},
			want:    map[string]*ListenerResource{},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UnmarshalLDS(tt.rawResources)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalLDS() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UnmarshalLDS() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnmarshalLDSHttpConnectionManager(t *testing.T) {
	ln1, ln2 := "listener1", "listener2"
	rn := "route_config"
	rawResources := []*any.Any{
		testutil.MarshalAny(&v3listenerpb.Listener{
			Name: ln1,
			FilterChains: []*v3listenerpb.FilterChain{
				{
					Filters: []*v3listenerpb.Filter{
						{
							ConfigType: &v3listenerpb.Filter_TypedConfig{
								TypedConfig: testutil.MarshalAny(&v3httppb.HttpConnectionManager{
									RouteSpecifier: &v3httppb.HttpConnectionManager_Rds{
										Rds: &v3httppb.Rds{
											RouteConfigName: rn,
										},
									},
								}),
							},
						},
					},
				},
			},
		}),
		testutil.MarshalAny(&v3listenerpb.Listener{
			Name: ln2,
			FilterChains: []*v3listenerpb.FilterChain{
				{
					Filters: []*v3listenerpb.Filter{
						{
							ConfigType: &v3listenerpb.Filter_TypedConfig{
								TypedConfig: testutil.MarshalAny(&v3httppb.HttpConnectionManager{
									RouteSpecifier: &v3httppb.HttpConnectionManager_RouteConfig{
										RouteConfig: &v3routepb.RouteConfiguration{
											Name: rn,
										},
									},
								}),
							},
						},
					},
				},
			},
		}),
	}
	res, err := UnmarshalLDS(rawResources)
	test.Assert(t, err == nil)
	test.Assert(t, len(res) == 2)
	// rds
	lis1 := res[ln1]
	test.Assert(t, lis1 != nil)
	test.Assert(t, lis1.NetworkFilters != nil)
	test.Assert(t, lis1.NetworkFilters[0].RouteConfigName == rn)
	// inline route config
	lis2 := res[ln2]
	test.Assert(t, lis2 != nil)
	test.Assert(t, lis2.NetworkFilters != nil)
	inlineRcfg := lis2.NetworkFilters[0].InlineRouteConfig
	test.Assert(t, inlineRcfg != nil)
	test.Assert(t, inlineRcfg.HttpRouteConfig != nil)
}

func TestUnmarshallLDSThriftProxy(t *testing.T) {
	ln := "listener"
	rawResources := []*any.Any{
		testutil.MarshalAny(&v3listenerpb.Listener{
			Name: ln,
			FilterChains: []*v3listenerpb.FilterChain{
				{
					Filters: []*v3listenerpb.Filter{
						{
							ConfigType: &v3listenerpb.Filter_TypedConfig{
								TypedConfig: testutil.MarshalAny(&v3thrift_proxy.ThriftProxy{
									RouteConfig: &v3thrift_proxy.RouteConfiguration{
										Routes: []*v3thrift_proxy.Route{
											{
												Match: &v3thrift_proxy.RouteMatch{
													MatchSpecifier: &v3thrift_proxy.RouteMatch_MethodName{
														MethodName: "method",
													},
													Headers: []*v3routepb.HeaderMatcher{
														{
															Name: "k1",
															HeaderMatchSpecifier: &v3routepb.HeaderMatcher_ExactMatch{
																ExactMatch: "v1",
															},
														},
														{
															Name: "k2",
															HeaderMatchSpecifier: &v3routepb.HeaderMatcher_StringMatch{
																StringMatch: &v3matcher.StringMatcher{
																	MatchPattern: &v3matcher.StringMatcher_Exact{
																		Exact: "v2",
																	},
																},
															},
														},
													},
												},
												Route: &v3thrift_proxy.RouteAction{
													ClusterSpecifier: &v3thrift_proxy.RouteAction_Cluster{
														Cluster: "cluster",
													},
												},
											},
										},
									},
								}),
							},
						},
					},
				},
			},
		}),
	}
	res, err := UnmarshalLDS(rawResources)
	test.Assert(t, err == nil)
	test.Assert(t, len(res) == 1)
	lis := res[ln]
	test.Assert(t, lis != nil)
	test.Assert(t, len(lis.NetworkFilters) == 1)
	test.Assert(t, lis.NetworkFilters[0].FilterType == NetworkFilterTypeThrift)
	tp := lis.NetworkFilters[0].InlineRouteConfig.ThriftRouteConfig
	test.Assert(t, tp != nil)
	test.Assert(t, len(tp.Routes) == 1)
	r := tp.Routes[0]
	test.Assert(t, r.Match != nil)
	test.Assert(t, r.Match.MatchPath("method") == true)
	for k, v := range map[string]string{"k1": "v1", "k2": "v2"} {
		test.Assert(t, r.Match.GetTags()[k] == v)
	}
	test.Assert(t, r.WeightedClusters != nil)
}
