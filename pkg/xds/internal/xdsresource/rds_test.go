package xdsresource

import (
	"github.com/cloudwego/kitex/internal/test"
	v3routepb "github.com/cloudwego/kitex/pkg/xds/internal/api/github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/cloudwego/kitex/pkg/xds/internal/testutil"
	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"reflect"
	"testing"
)

func TestUnmarshalRDSError(t *testing.T) {
	tests := []struct {
		name         string
		rawResources []*any.Any
		want         map[string]*RouteConfigResource
		wantErr      bool
	}{
		{
			name:         "resource is nil",
			rawResources: nil,
			want:         nil,
			wantErr:      true,
		},
		{
			name: "incorrect resource type url",
			rawResources: []*any.Any{
				{TypeUrl: EndpointTypeUrl, Value: []byte{}},
			},
			want:    map[string]*RouteConfigResource{},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UnmarshalRDS(tt.rawResources)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalRDS() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UnmarshalRDS() got = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnmarshalRDSSuccess(t *testing.T) {
	var (
		routeConfigName = "route_config"
		vhName = "vh"
		path = "test"
	)
	rawResources := []*any.Any{
		testutil.MarshalAny(&v3routepb.RouteConfiguration{
			Name: routeConfigName,
			VirtualHosts: []*v3routepb.VirtualHost{
				{
					Name: vhName,
					Routes: []*v3routepb.Route{
						{
							Match: &v3routepb.RouteMatch{
								PathSpecifier: &v3routepb.RouteMatch_Path{
									Path: path,
								},
							},
							Action: &v3routepb.Route_Route{
								Route: &v3routepb.RouteAction{
									ClusterSpecifier: &v3routepb.RouteAction_WeightedClusters{
										WeightedClusters: &v3routepb.WeightedCluster{
											Clusters: []*v3routepb.WeightedCluster_ClusterWeight{
												{
													Name: "cluster1",
													Weight: &wrapperspb.UInt32Value{
														Value: uint32(50),
													},
												},
												{
													Name: "cluster2",
													Weight: &wrapperspb.UInt32Value{
														Value: uint32(50),
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}),
	}
	got, err := UnmarshalRDS(rawResources)
	test.Assert(t, err == nil)
	test.Assert(t, len(got) == 1)
	routeConfig := got[routeConfigName]
	test.Assert(t, routeConfig != nil)
	test.Assert(t, len(routeConfig.VirtualHosts) == 1)
	vh := routeConfig.VirtualHosts[0]
	test.Assert(t, vh.Name == vhName)
	test.Assert(t, vh.Routes != nil)
	test.Assert(t, vh.Routes[0].Match.Path == path)
	wcs := vh.Routes[0].WeightedClusters
	test.Assert(t, wcs != nil)
	test.Assert(t, len(wcs) == 2)
	test.Assert(t, wcs[0].Weight() == 50)
	test.Assert(t, wcs[1].Weight() == 50)
}

func TestRouteMatch_Matched(t *testing.T) {
	type fields struct {
		Path          string
	}
	type args struct {
		path string
		tags map[string]string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name:   "matched empty route path",
			fields: fields{
				Path: "",
			},
			args:   args{
				path: "p1",
				tags: map[string]string{},
			},
			want:   true,
		},
		{
			name:   "matched route path",
			fields: fields{
				Path: "p1",
			},
			args:   args{
				path: "p1",
				tags: map[string]string{},
			},
			want:   true,
		},
		{
			name:   "not matched",
			fields: fields{
				Path: "p2",
			},
			args:   args{
				path: "p1",
				tags: map[string]string{},
			},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rm := &RouteMatch{
				Path:          tt.fields.Path,
			}
			if got := rm.Matched(tt.args.path, tt.args.tags); got != tt.want {
				t.Errorf("Matched() = %v, want %v", got, tt.want)
			}
		})
	}
}