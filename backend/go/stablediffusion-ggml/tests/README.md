# Native result API regression checks

Run from the backend directory after fetching the pinned upstream sources:

```sh
sh tests/result_api.sh
```

Alternatively, pass an existing stable-diffusion.cpp checkout:

```sh
sh tests/result_api.sh /path/to/stable-diffusion.cpp
```

The test compiles the binding against the upstream header and stubs inference;
no models or native libraries are required. It checks image batch cleanup,
empty/failed generation, missing pixels, PNG write failures, video result cleanup
(including audio and metadata), and upscaler validation and exception cleanup.
It does not run successful video encoding or inference. The linker discards
unused binding functions so only the exercised native API calls need stubs.

To check memory ownership with AddressSanitizer and UndefinedBehaviorSanitizer:

```sh
CXXFLAGS='-fsanitize=address,undefined -fno-omit-frame-pointer -g' \
LDFLAGS='-fsanitize=address,undefined' sh tests/result_api.sh
```

Set `TMPDIR` to choose where the runner creates its temporary files. `CXX`
selects the compiler. Do not pass `-DNDEBUG`: the checks use assertions.
