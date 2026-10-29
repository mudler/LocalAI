package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

// mountPoint validates a mount's At: a relative path of clean names with no
// empty, dot or dot-dot segment. It returns the cleaned slash path.
func mountPoint(at string) (string, error) {
	if strings.HasPrefix(at, "/") || strings.HasSuffix(at, "/") {
		return "", ErrInvalid
	}
	parts := strings.Split(at, "/")
	for i, p := range parts {
		n, err := CleanName(p)
		if err != nil || n != p {
			return "", ErrInvalid
		}
		parts[i] = n
	}
	return strings.Join(parts, "/"), nil
}

// CheckMountPoints validates every readable mount and rejects duplicate and
// nested points. It returns the cleaned points by mount index.
func CheckMountPoints(v View) (map[int]string, error) {
	pts := map[int]string{}
	for i, m := range v.Mounts {
		if m.Mode < ModeRead {
			continue
		}
		p, err := mountPoint(m.At)
		if err != nil {
			return nil, err
		}
		for _, q := range pts {
			if p == q || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
				return nil, ErrInvalid
			}
		}
		pts[i] = p
	}
	return pts, nil
}

// CheckEmptyRoot returns ErrExists when dir holds anything. A Materialize
// builds its tree only in an empty folder.
func CheckEmptyRoot(dir *os.Root) error {
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Readdirnames(1); err == nil {
		return ErrExists
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// MaterializeAt builds the view in the folder dir, which it makes when it is
// missing, through the root of that folder. It is the Materialize of a store
// that implements RootStore.
func MaterializeAt(ctx context.Context, s RootStore, v View, dir string) (int64, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- the materialized tree is read by the sandbox process
		return 0, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	return s.MaterializeRoot(ctx, v, root)
}
