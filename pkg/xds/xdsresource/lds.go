package xdsresource

import "github.com/golang/protobuf/ptypes/any"

type ListenerResource struct {

}

func UnmarshalLDS(rawResources []*any.Any) map[string]ListenerResource {
	return nil
}
