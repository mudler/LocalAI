# Face-recognition e2e fixtures

The face-recognition e2e tests (`tests/e2e-backends/backend_test.go`)
don't require committed fixture JPEGs. They follow the same pattern as
`BACKEND_TEST_AUDIO_URL` (whisper): the Makefile target passes
HTTP URLs via `BACKEND_TEST_FACE_IMAGE_*_URL`, and the suite downloads
them at `BeforeAll` time.

For the Makefile targets in `Makefile`, the defaults point at NASA's
public-domain astronaut portraits on nasa.gov. NASA images are released
into the public domain by U.S. federal work (see
<https://www.nasa.gov/nasa-brand-center/images-and-media/>).

If you want to run the suite fully offline, drop three JPEGs into this
directory with the names the Makefile expects and flip the env vars to
the `_FILE` variants:

```
tests/fixtures/faces/person_a_1.jpg   # person A, photo 1
tests/fixtures/faces/person_a_2.jpg   # person A, photo 2 (different angle/lighting)
tests/fixtures/faces/person_b.jpg     # a different person
```

The suite asserts *relative* ordering only (`d(a1,a2) < d(a1,b)`) — the
absolute distance ceiling is set per-model via
`BACKEND_TEST_VERIFY_DISTANCE_CEILING` so SFace (which uses a wider
distance distribution than ArcFace) can share the same suite.
