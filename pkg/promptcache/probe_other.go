//go:build !linux && !darwin

package promptcache

import "os"

func dropCache(*os.File) error { return ErrNoCacheDrop }
