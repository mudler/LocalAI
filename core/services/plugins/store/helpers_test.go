package store_test

import "os"

func writeFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }
