package http_test

import "os"

// testHTTPAddr is the loopback address the in-process API server binds in
// these suites. The 9090 default matches what CI has always used; set
// LOCALAI_TEST_HTTP_PORT when something else already listens on 9090 locally,
// otherwise the coverage suite can never pass on that machine (the
// suite would poll whatever service squats the port and time out).
var testHTTPAddr = "127.0.0.1:" + testHTTPPort()

func testHTTPPort() string {
	if p := os.Getenv("LOCALAI_TEST_HTTP_PORT"); p != "" {
		return p
	}
	return "9090"
}
