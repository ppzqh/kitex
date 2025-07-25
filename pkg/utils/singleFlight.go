package utils

import "golang.org/x/sync/singleflight"

type SingleflightGroup struct {
	sfg singleflight.Group
}

func (g *SingleflightGroup) CheckAndDo(key string, loadFn func() (any, bool), fn func() (any, error)) (any, error, bool) {
	if v, loaded := loadFn(); loaded {
		return v, nil, true
	}
	return g.sfg.Do(key, func() (any, error) {
		if v, loaded := loadFn(); loaded {
			return v, nil
		}
		return fn()
	})
}
