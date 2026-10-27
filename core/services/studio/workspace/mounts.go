package workspace

import "strings"

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
